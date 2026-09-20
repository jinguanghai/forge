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
}

const qualityAlertWindow = 24 * time.Hour

func qualityAlertPath(workDir string) string {
	return filepath.Join(workDir, ".forge", "alerts.jsonl")
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
		if a.Sev == "info" {
			continue
		}
		if ts, perr := time.Parse(time.RFC3339, a.Ts); perr == nil && ts.Before(cutoff) {
			continue
		}
		k := a.Metric + "|" + a.Key
		if prev, ok := latest[k]; !ok || a.Ts > prev.Ts {
			latest[k] = a
		}
	}
	for _, a := range latest {
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
