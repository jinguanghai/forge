package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── DeepSeek 前缀缓存度量 ─────────────────────
// 每请求记录 prompt_cache_hit_tokens / miss_tokens 到 cache_stats.jsonl,
// 供 /cache 命令展示命中率与估算节省。
// 前提: 请求必须带 stream_options.include_usage=true, DeepSeek 才会在
// 流式响应末尾 (choices 为空) 返回 usage 块。
//
// 实测 (20260805): 相同前缀请求 hit≈2944/3016 (97.6%); 切 user_id 首次全 miss;
// 随机 message id 无害 (服务端按 token 前缀匹配, 忽略该字段)。
// v3.0: ① 消息级随机 id 已彻底移除 → 请求体字节级确定性 (DSH 同款, 不发送 id);
//       ② 工具轮次 (finish_reason=tool_calls) 也延迟收尾读 usage → 逐调用记账,
//          不再只统计最终轮。此文件即度量闭环。

type CacheStat struct {
	Time       string `json:"time"`
	Model      string `json:"model"`
	Hit        int    `json:"hit"`
	Miss       int    `json:"miss"`
	SysHash    string `json:"sys_hash,omitempty"`    // system 前缀 SHA-256 前16位 (六西格玛守卫)
	SysChanged bool   `json:"sys_changed,omitempty"` // 进程内 system 前缀发生过变化
}

var (
	cacheStatMu   sync.Mutex
	cacheStatPath = "cache_stats.jsonl"
)

// setCacheStatPath 由 main 启动时用 cfg.WorkDir 设置。
func setCacheStatPath(wd string) {
	cacheStatMu.Lock()
	defer cacheStatMu.Unlock()
	cacheStatPath = filepath.Join(wd, "cache_stats.jsonl")
}

// recordCacheStat 追加一条请求级缓存统计 (幂等, 失败静默)。
func recordCacheStat(model string, hit, miss int, sysHash string, sysChanged bool) {
	if hit <= 0 && miss <= 0 && !sysChanged {
		return
	}
	stat := CacheStat{
		Time:       time.Now().Format(time.RFC3339),
		Model:      model,
		Hit:        hit,
		Miss:       miss,
		SysHash:    sysHash,
		SysChanged: sysChanged,
	}
	data, err := json.Marshal(stat)
	if err != nil {
		return
	}
	cacheStatMu.Lock()
	defer cacheStatMu.Unlock()
	// Windows: 立即 Close, 避免句柄占用阻塞后续 rename
	f, err := os.OpenFile(cacheStatPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(string(data) + "\n")
	_ = f.Close()
}

// ─── 计费价格 (DeepSeek 官网 202608 美元/M tokens) ──────────
// pro: 命中 $0.003625 vs miss $0.435 (120x)
// flash: 命中 $0.0028 vs miss $0.14 (50x)
const (
	priceProHit    = 0.003625
	priceProMiss   = 0.435
	priceFlashHit  = 0.0028
	priceFlashMiss = 0.14
)

func isFlashModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "flash")
}

// cacheStatsSummary 聚合 cache_stats.jsonl, 返回 /cache 命令的展示文本。
// cacheHealth 三色分级 (静态判定): 从最近 n 条算命中率,
// 死程序判级 + 一句话根因 tag, 不靠 LLM 事后复盘。结果只入 /cache 展示, 不进 system/记忆。
//
//	绿  ≥98%: 基线健康                (常态)
//	黄  85~98%: 偏低, 提示根因 tag
//	红  <85%  或 近 n 条前缀多次变更: 需治理
//
// 根因 tag 由死程序按证据判, 非模型猜测。
func cacheHealth(n int) string {
	if n <= 0 {
		n = 20
	}
	type rec struct {
		hit, miss int
		changed   bool
	}
	var rows []rec
	cacheStatMu.Lock()
	f, err := os.Open(cacheStatPath)
	if err != nil {
		cacheStatMu.Unlock()
		return "  无健康数据"
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var s CacheStat
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		rows = append(rows, rec{hit: s.Hit, miss: s.Miss, changed: s.SysChanged})
	}
	_ = f.Close()
	cacheStatMu.Unlock()
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	if len(rows) == 0 {
		return "  无健康数据"
	}
	var hit, miss, changed int
	for _, r := range rows {
		hit += r.hit
		miss += r.miss
		if r.changed {
			changed++
		}
	}
	if hit+miss == 0 {
		return "  无命中/未命中样本"
	}
	rate := float64(hit) * 100 / float64(hit+miss)
	root := "正常"
	if changed*2 >= len(rows) {
		root = "前缀频繁变更(锚点改动/system漂移)"
	} else if rate < 85 && changed == 0 {
		root = "请求体不稳定(headLen/历史回放错位)"
	} else if rate < 85 {
		root = "前缀变更+请求体异常"
	}
	var badge string
	switch {
	case rate >= 98:
		badge = color(ansi.green, "🟢")
	case rate >= 85:
		badge = color(ansi.yellow, "🟡")
	default:
		badge = color(ansi.red, "🔴")
	}
	return fmt.Sprintf("  %s 缓存健康(近%d条): %.1f%% | 前缀变更%d/近%d | 根因: %s",
		badge, len(rows), rate, changed, len(rows), root)
}

func cacheStatsSummary() string {
	type agg struct {
		reqs int
		hit  int
		miss int
	}
	var (
		byModel = map[string]*agg{}
		order   []string
		total   agg
		// 前缀指纹守卫统计
		sysChangedCount int
		lastSysChanged  string
		sysHashes       = map[string]bool{}
	)

	cacheStatMu.Lock()
	f, err := os.Open(cacheStatPath)
	if err != nil {
		cacheStatMu.Unlock()
		return "  暂无缓存统计数据 (首次请求后生成)。"
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var s CacheStat
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		if s.SysChanged {
			sysChangedCount++
			lastSysChanged = s.Time
		}
		if s.SysHash != "" {
			sysHashes[s.SysHash] = true
		}
		a, ok := byModel[s.Model]
		if !ok {
			a = &agg{}
			byModel[s.Model] = a
			order = append(order, s.Model)
		}
		a.reqs++
		a.hit += s.Hit
		a.miss += s.Miss
		total.reqs++
		total.hit += s.Hit
		total.miss += s.Miss
	}
	_ = f.Close()
	cacheStatMu.Unlock()

	if total.reqs == 0 {
		return "  暂无缓存统计数据 (首次请求后生成)。"
	}

	var sb strings.Builder
	sb.WriteString(cacheHealth(20) + "\n")
	sb.WriteString(fmt.Sprintf("  请求数: %d | 输入 tokens: %d | 命中: %d | 未命中: %d\n",
		total.reqs, total.hit+total.miss, total.hit, total.miss))
	if total.hit+total.miss > 0 {
		sb.WriteString(fmt.Sprintf("  总命中率: %s\n", color(ansi.green, fmt.Sprintf("%.1f%%",
			float64(total.hit)*100/float64(total.hit+total.miss)))))
	}
	sb.WriteString(fmt.Sprintf("  前缀指纹: 进程内变更 %d 次 | 历史不同指纹 %d 个\n", sysChangedCount, len(sysHashes)))
	if lastSysChanged != "" {
		sb.WriteString(fmt.Sprintf("  最近前缀变更: %s (新前缀首次请求将全量未命中)\n", lastSysChanged))
	}
	sb.WriteString("\n  按模型:\n")
	for _, m := range order {
		a := byModel[m]
		rate := 0.0
		if a.hit+a.miss > 0 {
			rate = float64(a.hit) * 100 / float64(a.hit+a.miss)
		}
		sb.WriteString(fmt.Sprintf("    %-22s reqs=%-4d hit=%-7d miss=%-7d rate=%5.1f%%\n",
			m, a.reqs, a.hit, a.miss, rate))
	}

	// 估算节省: 若无缓存 (全按 miss 价) vs 实际 (hit 价 + miss 价)
	var saved float64
	for _, m := range order {
		a := byModel[m]
		var hitP, missP float64
		if isFlashModel(m) {
			hitP, missP = priceFlashHit, priceFlashMiss
		} else {
			hitP, missP = priceProHit, priceProMiss
		}
		noCache := float64(a.hit+a.miss) * missP
		actual := float64(a.hit)*hitP + float64(a.miss)*missP
		saved += noCache - actual
	}
	sb.WriteString(fmt.Sprintf("\n  估算节省: $%.4f (按官网命中/miss 价差计算)\n", saved))
	sb.WriteString("  说明: 命中率≥95% 为达标 (system 前缀进程内恒定); 切 user_id 会全量 miss\n")
	sb.WriteString("  说明: 已计入工具轮次 usage (逐调用记账); 命中率提升要点: 保持 memory.json\n")
	sb.WriteString("        锚点不变 + 历史原样回放 (铸剑炉 v3.0 起请求体字节级确定性)\n")
	// 破缓存归因——关联 anchor_audit 锚点改动
	if data, err := os.ReadFile(anchorAuditPath(filepath.Dir(cacheStatPath))); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) > 0 {
			sb.WriteString(fmt.Sprintf("  锚点改动: 共 %d 次 (anchor_audit.jsonl) | 近 7 天见 /anchor\n", len(lines)))
			last := lines[len(lines)-1]
			var e AnchorAuditEntry
			if json.Unmarshal([]byte(last), &e) == nil && e.Fields != nil {
				sb.WriteString(fmt.Sprintf("  最近一次: %s [%s] %s\n", e.Time, strings.Join(e.Fields, ","), e.Reason))
				if sysChangedCount > 0 {
					sb.WriteString(fmt.Sprintf("  ⚠️ 提示: 前缀已变 %d 次, 若命中率<80%% 检查上述锚点改动 (缓存重置 30 倍价差)\n", sysChangedCount))
				}
			}
		}
	}
	return sb.String()
}

// cacheHitRate returns recent n-request cache hit rate (status bar use).
func cacheHitRate(n int) (rate float64, nRecs int, ok bool) {
	if n <= 0 {
		n = 20
	}
	type rec struct{ hit, miss int }
	var rows []rec
	cacheStatMu.Lock()
	f, err := os.Open(cacheStatPath)
	if err != nil {
		cacheStatMu.Unlock()
		return 0, 0, false
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var s CacheStat
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		rows = append(rows, rec{hit: s.Hit, miss: s.Miss})
	}
	_ = f.Close()
	cacheStatMu.Unlock()
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	if len(rows) == 0 {
		return 0, 0, false
	}
	var hit, miss int
	for _, r := range rows {
		hit += r.hit
		miss += r.miss
	}
	if hit+miss == 0 {
		return 0, len(rows), false
	}
	rate = float64(hit) * 100 / float64(hit+miss)
	return rate, len(rows), true
}
