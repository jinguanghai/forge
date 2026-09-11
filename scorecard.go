package main

// scorecard.go — gate Scorecard: 按 lang 聚合 gate_audit.jsonl 的运行记录。
//
// 目的 (cee "Measured, Not Asserted" 落地): 让"哪个 gate/lang 真正高频使用、有效果"可一眼衡量。
//   - 不写文件, 不碰 Build/forgeGate/auditGate 等执行路径, 纯只读聚合。
//   - 复用 gate_audit.jsonl (FORGE_GATE_AUDIT 开关控制的每 Build 流水), 无需新增埋点。
//
// 命令: /scorecard [N]  — 输出全局排行榜; N>0 时只统计最近 N 条, 0/缺省=全部。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// scorecardEntry gate_audit.jsonl 一行的解析结构 (只取聚合所需字段)
type scorecardEntry struct {
	Lang        string `json:"lang"`
	LangOmitted bool   `json:"lang_omitted"`
	Fallback    bool   `json:"fallback"`
	OK          bool   `json:"ok"`
	DurationMs  int64  `json:"duration_ms"`
	Retries     int    `json:"retries"`
}

// scorecardAgg 某个 lang/gate 的聚合结果
type scorecardAgg struct {
	Lang     string
	Count    int
	OK       int
	Fail     int
	Retries  int
	Fallback int
	Omitted  int
	TotDurMs int64
}

// scorecardAggregate 解析 gate_audit.jsonl 数据, 按 lang 聚合。
// windowN>0 时只统计最近 windowN 条 (从末尾取, 即最新窗口)。
// 纯函数: 无副作用, 解析失败静默, 返回格式化排行榜文本。
func scorecardAggregate(data []byte, windowN int) string {
	if len(data) == 0 {
		return "无数据: gate_audit.jsonl 为空或不存在"
	}
	lines := strings.Split(string(data), "\n")
	valid := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			valid = append(valid, l)
		}
	}
	if len(valid) == 0 {
		return "无数据: gate_audit.jsonl 无运行记录"
	}
	if windowN > 0 && windowN < len(valid) {
		valid = valid[len(valid)-windowN:]
	}
	byLang := make(map[string]*scorecardAgg)
	for _, line := range valid {
		var e scorecardEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		lang := e.Lang
		if lang == "" {
			lang = "unknown"
		}
		agg := byLang[lang]
		if agg == nil {
			agg = &scorecardAgg{Lang: lang}
			byLang[lang] = agg
		}
		agg.Count++
		if e.OK {
			agg.OK++
		} else {
			agg.Fail++
		}
		agg.Retries += e.Retries
		if e.Fallback {
			agg.Fallback++
		}
		if e.LangOmitted {
			agg.Omitted++
		}
		agg.TotDurMs += e.DurationMs
	}
	if len(byLang) == 0 {
		return "无数据: 无法解析任何 gate_audit 行"
	}
	aggs := make([]*scorecardAgg, 0, len(byLang))
	for _, a := range byLang {
		aggs = append(aggs, a)
	}
	sort.Slice(aggs, func(i, j int) bool {
		return aggs[i].Count > aggs[j].Count
	})
	var sb strings.Builder
	fmt.Fprintf(&sb, "gate Scorecard (分析 %d 条运行记录)\n\n", len(valid))
	sb.WriteString("lang       调用  成功  失败  成功率  均耗时ms  重试  回退  省lang\n")
	var totCount, totOK int
	for _, a := range aggs {
		rate := 0.0
		if a.Count > 0 {
			rate = float64(a.OK) / float64(a.Count) * 100
		}
		avg := int64(0)
		if a.Count > 0 {
			avg = a.TotDurMs / int64(a.Count)
		}
		fmt.Fprintf(&sb, "%-10s %5d %5d %5d  %6.0f%%  %7d  %5d  %4d  %5d\n",
			a.Lang, a.Count, a.OK, a.Fail, rate, avg, a.Retries, a.Fallback, a.Omitted)
		totCount += a.Count
		totOK += a.OK
	}
	trate := 0.0
	if totCount > 0 {
		trate = float64(totOK) / float64(totCount) * 100
	}
	fmt.Fprintf(&sb, "\n合计: %d 调用, %d 成功 (%.1f%%)\n", totCount, totOK, trate)
	return sb.String()
}

// cmdScorecard 处理 /scorecard 命令。
// 用法: /scorecard [N]  N>0 只统计最近 N 条, 缺省/0=全部。
func cmdScorecard(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	window := 0
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &window)
	}
	path := filepath.Join(cfg.WorkDir, "gate_audit.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("无数据: 无法读取", path, ":", err)
		return
	}
	if window > 0 {
		fmt.Printf("(窗口: 最近 %d 条)\n", window)
	}
	fmt.Println(scorecardAggregate(data, window))
}
