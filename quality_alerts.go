package main

// quality_alerts.go — 质量告警出口: 消费 .forge/alerts.jsonl (forge_watchdog.py 产出)
//
// 分工: health_report.go 管"工具执行失败"(躯壳自检), 本文件管"运行质量超线"
// (gate 失败率/耗时/缓存命中率/日志体积)。
// 判定由死程序(watchdog 的死阈值)完成 —— 这里只负责把结论送到人眼前:
// 启动横幅一行摘要 + /health 详情。零崩溃: 读失败/坏行一律静默降级。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// QualityAlert 一条质量告警 (与 forge_watchdog.py 的落盘格式一一对应)。
type QualityAlert struct {
	Ts        string  `json:"ts"`
	Sev       string  `json:"sev"`
	Metric    string  `json:"metric"`
	Key       string  `json:"key"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Msg       string  `json:"msg"`
	// Resolved 恢复标记 —— alert_sink.py 在指标回落时追加的同 metric|key 记录:
	// 最新一条带此标记 = 已解决, 不计入启动横幅与 /health。
	// 没有它, 修好后的旧告警会在窗口内常驻 —— 修好了还喊, 等于训练使用者
	// 忽略告警 (实测 2026-10-01: 结构违规 19:46 已修好, 横幅仍报「3 条(高危 1)」)。
	// 契约单一数据源: defense_system/alert_sink.py 的 resolved_record()。
	Resolved bool `json:"resolved"`
}

const qualityAlertWindow = 24 * time.Hour

func qualityAlertPath(workDir string) string {
	return filepath.Join(workDir, ".forge", "alerts.jsonl")
}

// parseAlertTs 解析告警时间戳, 兼容两种产出格式:
//
//	RFC3339 带时区 (forge_watchdog.py, 2006-01-02T15:04:05+08:00)
//	无时区     (历史数据/其它脚本产出, 视为本地时区)
//
// 都解析不出才返回 ok=false —— 此时该条保留(宁可多报, 不误吞真实告警)。
// 教训: 旧实现用 `perr == nil && ts.Before(cutoff)` 把"解析失败"与"未过期"
// 混为一谈, 一个缺失的 +08:00 就让过期告警永久常驻横幅(时间窗口形同虚设)。
func parseAlertTs(s string) (time.Time, bool) {
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts, true
	}
	if ts, err := time.ParseInLocation("2006-01-02T15:04:05", s, time.Local); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

// readQualityAlerts 读窗口内告警, 按 metric|key 去重(保留最新), 时间倒序。
// sev=info 的记录(如轮转成功)不算告警, 直接跳过。
func readQualityAlerts(workDir string, window time.Duration) []QualityAlert {
	var out []QualityAlert
	if workDir == "" {
		return out
	}
	f, err := os.Open(qualityAlertPath(workDir))
	if err != nil {
		return out
	}
	defer f.Close()
	cutoff := time.Now().Add(-window)
	latest := map[string]QualityAlert{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var a QualityAlert
		if json.Unmarshal([]byte(line), &a) != nil {
			continue
		}
		// 普通 info(轮转/说明类)不算告警, 直接丢弃; 带 resolved 的 info 是
		// 「已恢复」水位, 必须参与去重 —— 它要能压掉同键更早的 warn/high。
		if a.Sev == "info" && !a.Resolved {
			continue
		}
		if ts, ok := parseAlertTs(a.Ts); ok && ts.Before(cutoff) {
			continue
		}
		k := a.Metric + "|" + a.Key
		if prev, ok := latest[k]; !ok || a.Ts > prev.Ts {
			latest[k] = a
		}
	}
	for _, a := range latest {
		if a.Resolved {
			continue // 该键最新水位是「已恢复」-> 不再计入告警
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ts > out[j].Ts })
	return out
}

// qualityAlertBannerLine 横幅用一行摘要; 无告警返回空串。
func qualityAlertBannerLine(workDir string) string {
	as := readQualityAlerts(workDir, qualityAlertWindow)
	if len(as) == 0 {
		return ""
	}
	high := 0
	for _, a := range as {
		if a.Sev == "high" {
			high++
		}
	}
	head := fmt.Sprintf("%d 条", len(as))
	if high > 0 {
		head = fmt.Sprintf("%d 条(高危 %d)", len(as), high)
	}
	return head + " · " + as[0].Msg
}

// printQualityAlerts /health 详情输出。
func printQualityAlerts(workDir string) {
	as := readQualityAlerts(workDir, 7*24*time.Hour)
	if len(as) == 0 {
		fmt.Printf("  %s %s\n", bold("质量:"), color(ansi.green, "无告警 (近7天)"))
		return
	}
	fmt.Printf("  %s %s\n", bold("质量:"), color(ansi.yellow, fmt.Sprintf("%d 条告警 (近7天)", len(as))))
	for _, a := range as {
		c := ansi.yellow
		if a.Sev == "high" {
			c = ansi.red
		}
		fmt.Printf("    %s %s  %s\n", color(c, "["+a.Sev+"]"), dim(a.Ts), a.Msg)
	}
}
