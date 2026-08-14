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

// ─── DeepSeek 前缀缓存度量 (六西格玛 P0) ─────────────────────
// 每请求记录 prompt_cache_hit_tokens / miss_tokens 到 cache_stats.jsonl,
// 供 /cache 命令展示命中率与估算节省。
// 前提: 请求必须带 stream_options.include_usage=true, DeepSeek 才会在
// 流式响应末尾 (choices 为空) 返回 usage 块。
//
// 实测 (20260805): 相同前缀请求 hit≈2944/3016 (97.6%); 切 user_id 首次全 miss;
// 随机 message id 无害 (服务端按 token 前缀匹配)。此文件即度量闭环。

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
	priceProHit   = 0.003625
	priceProMiss  = 0.435
	priceFlashHit = 0.0028
	priceFlashMiss = 0.14
)

func isFlashModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "flash")
}

// cacheStatsSummary 聚合 cache_stats.jsonl, 返回 /cache 命令的展示文本。
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
		// 前缀指纹守卫统计 (六西格玛 Control)
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
	return sb.String()
}
