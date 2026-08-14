package main

// health_report.go — 躯壳自检: 消费 events.jsonl, 主动报告躯壳问题
//
// 设计 (20260812 DMAIC 抄作业 exo event log 的"消费者层"):
//   - 信号源: error 事件 + tool_result ok=false 事件 (工具执行失败)
//   - 问题定义: 同模块失败 ≥ 阈值(3次) 才算问题 —— 单次失败多为环境, 防误报
//   - 水位线: .forge\health_watermark 记录已扫描行号, 每轮只报新错误, 杜绝重复报告
//   - 修复信号: 失败后存在 self_modified/self_restart → 标注"(升级后未复现)"
//   - 轮转: events.jsonl 超 5MB/5万行 → 归档 events_archive_{ts}.jsonl (证据链不丢)
//   - 零崩溃: 读失败/坏行/任何异常 → 静默降级, 永不阻塞主流程 (风险清单 #1)

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	healthThreshold  = 3                     // 同模块失败 ≥3 次才算"问题"
	healthMaxBytes   = 5 * 1024 * 1024       // events.jsonl 超 5MB 轮转
	healthMaxLines   = 50000                 // 或超 5 万行轮转
	healthTimeWindow = 24 * time.Hour        // 启动报告只看最近 24h
)

// HealthIssue 一个问题: 同模块失败聚合
type HealthIssue struct {
	Module     string // 模块名 (lang 或 error detail)
	Kind       string // "tool" | "error"
	Count      int    // 窗口内失败次数
	LastTs     string // 最后失败时间 (RFC3339)
	LastLine   int    // 最后失败行号 (文件顺序, 用于修复信号判定)
	FixedAfter bool   // 最后失败之后发生过自改/重启 → 疑似已修复
}

// scanHealthEvents 解析 events.jsonl 中 fromLine(1-based, 0=全部) 之后的事件。
// 返回: 按模块聚合的问题表 + 文件总行数。永不 panic。
func scanHealthEvents(path string, fromLine int, since time.Time) (map[string]*HealthIssue, int) {
	issues := make(map[string]*HealthIssue)
	data, err := os.ReadFile(path)
	if err != nil {
		return issues, 0 // 文件不存在/读失败 → 空, 不阻塞
	}
	cutoff := since.Format(time.RFC3339)
	lines := strings.Split(string(data), "\n")
	total := 0
	lastFixLine := 0
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		total = i + 1
		if total <= fromLine {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue // 坏行(写一半)跳过, 不崩溃
		}
		// 时间窗过滤: 早于 cutoff 的失败不计 (仅当 since 非零值)
		if !since.IsZero() && ev.Ts < cutoff {
			continue
		}
		switch ev.Type {
		case EvSelfModified, EvSelfRestart:
			lastFixLine = total // 修复事件行号 (文件顺序, 天然精确)
		case EvError:
			mod := ev.Detail
			if mod == "" {
				mod = "unknown"
			}
			addIssue(issues, mod, "error", ev.Ts, total)
		case EvToolResult:
			// data.ok == false → 工具执行失败 (躯壳问题主信号)
			if m, ok := ev.Data.(map[string]interface{}); ok {
				if okv, ok2 := m["ok"]; ok2 {
					if b, ok3 := okv.(bool); ok3 && !b {
						mod := ev.Detail
						if mod == "" {
							mod = "tool"
						}
						addIssue(issues, mod, "tool", ev.Ts, total)
					}
				}
			}
		}
	}
	// 修复信号: 修复事件在失败之后(行号更大) → 疑似已修复
	for _, iss := range issues {
		if lastFixLine > 0 && iss.LastLine < lastFixLine {
			iss.FixedAfter = true
		}
	}
	return issues, total
}

func addIssue(issues map[string]*HealthIssue, mod, kind, ts string, line int) {
	iss, ok := issues[mod]
	if !ok {
		iss = &HealthIssue{Module: mod, Kind: kind}
		issues[mod] = iss
	}
	iss.Count++
	if line > iss.LastLine {
		iss.LastLine = line
	}
	if ts > iss.LastTs {
		iss.LastTs = ts
	}
}

// buildHealthReport 聚合超阈值问题为报告文本。无问题返回 ""。
func buildHealthReport(issues map[string]*HealthIssue, threshold int) string {
	parts := make([]string, 0, len(issues))
	for _, iss := range issues {
		if iss.Count < threshold {
			continue
		}
		s := fmt.Sprintf("%s失败×%d", iss.Module, iss.Count)
		if iss.FixedAfter {
			s += "(升级后未复现)"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts) // 稳定输出顺序
	return strings.Join(parts, " | ")
}

// readWatermark 读取水位线 (已扫描行号)。失败 → 0 (从全量重扫, 可接受)。
func readWatermark(workDir string) int {
	b, err := os.ReadFile(filepath.Join(workDir, ".forge", "health_watermark"))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

// writeWatermark 原子写水位线 (tmp+rename, 记忆纪律)。
func writeWatermark(workDir string, n int) {
	if n < 0 {
		n = 0
	}
	dir := filepath.Join(workDir, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	tmp := filepath.Join(dir, "health_watermark.tmp")
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(n)), 0644); err != nil {
		return
	}
	os.Rename(tmp, filepath.Join(dir, "health_watermark"))
}

// printHealthReport 启动时调用: 从水位线之后增量扫描 (上次会话遗留的新问题),
// 有问题打印 ≤2 行, 无问题完全静默。同时把水位线更新到当前行数。
// 注意: 启动与每轮自检共用同一水位线 → 同一批问题只报一次, 杜绝重复报告。
func printHealthReport(workDir string) {
	defer func() { recover() }() // 零崩溃兜底
	if workDir == "" {
		return
	}
	path := filepath.Join(workDir, ".forge", "events.jsonl")
	last := readWatermark(workDir)
	issues, total := scanHealthEvents(path, last, time.Time{})
	if report := buildHealthReport(issues, healthThreshold); report != "" {
		fmt.Fprintf(os.Stderr, "%s 躯壳自检: %s — 建议检修\n", color(ansi.yellow, "⚙"), report)
	}
	writeWatermark(workDir, total)
	rotateEventsIfNeeded(workDir, healthMaxBytes, healthMaxLines)
}

// maybePrintHealthHint 每轮对话开始时调用: 增量扫描(水位线之后), 新问题超阈值才 1 句话。
func maybePrintHealthHint(workDir string) {
	defer func() { recover() }() // 零崩溃兜底
	if workDir == "" {
		return
	}
	path := filepath.Join(workDir, ".forge", "events.jsonl")
	last := readWatermark(workDir)
	issues, total := scanHealthEvents(path, last, time.Time{})
	if report := buildHealthReport(issues, healthThreshold); report != "" {
		fmt.Fprintf(os.Stderr, "%s 自检: %s — 建议检修\n", color(ansi.yellow, "⚙"), report)
	}
	writeWatermark(workDir, total)
	rotateEventsIfNeeded(workDir, healthMaxBytes, healthMaxLines)
}

// rotateEventsIfNeeded 事件日志超限 → 归档 + 水位线重置。可注入阈值便于测试。
func rotateEventsIfNeeded(workDir string, maxBytes, maxLines int64) {
	defer func() { recover() }() // 零崩溃兜底
	dir := filepath.Join(workDir, ".forge")
	path := filepath.Join(dir, "events.jsonl")
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	if st.Size() < maxBytes {
		// 行数阈值: 读一遍数行 (5MB 上限内可接受)
		if n, _ := countLines(path); n < maxLines {
			return
		}
	}
	// 持锁 rename, 避免与 logEvent 并发写冲突 (Windows rename 打开文件会失败)
	evMu.Lock()
	defer evMu.Unlock()
	ts := time.Now().Format("20060102_150405")
	dst := filepath.Join(dir, "events_archive_"+ts+".jsonl")
	if err := os.Rename(path, dst); err != nil {
		return // 正被写入 → 本次跳过, 下次再轮转
	}
	writeWatermark(workDir, 0)
}

func countLines(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	buf := make([]byte, 64*1024)
	n := int64(0)
	for {
		c, err := f.Read(buf)
		n += int64(countByte(buf[:c], '\n'))
		if err != nil {
			break
		}
	}
	return n, nil
}

func countByte(b []byte, target byte) int {
	n := 0
	for _, c := range b {
		if c == target {
			n++
		}
	}
	return n
}
