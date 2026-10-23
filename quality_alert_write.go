package main

// quality_alert_write.go — 质量告警的写入臂 (Go 侧, 20261012)
//
// 背景 (T29-A 实测): 告警出口 .forge/alerts.jsonl 此前是 Python 侧单边写入
// (forge_watchdog.py / fractal_check.py / cache_guard.py), Go 侧只有读函数
// (quality_alerts.go)。后果: anchorExemptWarnThreshold=3 早已在 /anchor 摘要里
// 打印「今日豁免 N 次」, 却从未落进告警流 —— 启动横幅与 /health 看不见。
// 「检测到」与「送到人眼前」之间断了一节: 结论只活在一条需要人主动敲命令
// 才能看到的文本里, 与 A11 的展示层说谎同族。
//
// 契约单一数据源 = defense_system/alert_sink.py, 本文件是它的 Go 侧对等实现:
//  ① 一行一条 JSON: ts/sev/metric/key/value/threshold/msg
//  ② 同 metric|key 在去重窗口内不重复写 (喊一次就够)
//  ③ 指标回落 → 追加 {"sev":"info","resolved":true,...} 压掉旧告警
//  ④ 只碰自己的 metric|key —— 归属由调用方的 metric 命名保证 (带脚本前缀),
//     不清除其它脚本的告警 (多个脚本共用同一个 alerts.jsonl)
//
// 写失败一律静默降级: 告警是旁路, 绝不能因为写不出告警而影响主流程。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// qualityAlertDedupWindow 同键去重窗口 (与 alert_sink.py 的 dedup_hours 同口径)。
const qualityAlertDedupWindow = 24 * time.Hour

// lastQualityAlert 返回 alerts.jsonl 中同 metric|key 的最新一条 (无则 ok=false)。
// 读失败/坏行一律跳过 —— 与 quality_alerts.go 的读路径同判据: 宁可多报, 不误吞。
func lastQualityAlert(workDir, metric, key string) (QualityAlert, bool) {
	f, err := os.Open(qualityAlertPath(workDir))
	if err != nil {
		return QualityAlert{}, false
	}
	defer f.Close()
	var last QualityAlert
	found := false
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
		if a.Metric != metric || a.Key != key {
			continue
		}
		if !found || a.Ts >= last.Ts {
			last, found = a, true
		}
	}
	return last, found
}

// appendQualityAlertLine 追加一行 (O_APPEND, 行级原子; 与 alert_sink.py 的追加语义一致)。
func appendQualityAlertLine(workDir string, a QualityAlert) error {
	if a.Ts == "" {
		a.Ts = time.Now().Format(time.RFC3339)
	}
	line, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(qualityAlertPath(workDir)); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(qualityAlertPath(workDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(string(line) + "\n")
	return err
}

// EmitQualityAlert 写一条告警。同键在去重窗口内已有未解决告警 → 不重复写 (返回 false)。
//
// 去重只看流 (alerts.jsonl 的最新一条), 不另建 state 文件 —— 与 alert_sink.py 的
// 「流驱动」通道同构, 也避免 Go/Python 两份水位各自漂移。
func EmitQualityAlert(workDir, metric, key, sev string, value, threshold float64, msg string) bool {
	if workDir == "" || metric == "" {
		return false
	}
	if last, ok := lastQualityAlert(workDir, metric, key); ok && !last.Resolved {
		if ts, ok2 := parseAlertTs(last.Ts); ok2 && time.Since(ts) < qualityAlertDedupWindow {
			return false
		}
	}
	a := QualityAlert{
		Ts: time.Now().Format(time.RFC3339), Sev: sev, Metric: metric, Key: key,
		Value: value, Threshold: threshold, Msg: msg,
	}
	return appendQualityAlertLine(workDir, a) == nil
}

// ResolveQualityAlert 写恢复水位 (仅当该键最新一条仍是未解决告警时写, 幂等)。
// 没在报就不写 —— 恢复记录本身也是噪音 (与 alert_sink.py 同判据)。
func ResolveQualityAlert(workDir, metric, key string) bool {
	if workDir == "" || metric == "" {
		return false
	}
	last, ok := lastQualityAlert(workDir, metric, key)
	if !ok || last.Resolved {
		return false
	}
	a := QualityAlert{
		Ts: time.Now().Format(time.RFC3339), Sev: "info", Metric: metric, Key: key,
		Resolved: true,
		Msg:      fmt.Sprintf("%s/%s 已恢复 (本次检查未复现)", metric, key),
	}
	return appendQualityAlertLine(workDir, a) == nil
}

// SyncQualityAlert 统一入口: active=true 报 (带去重), false 恢复。
// 调用方只表达「当前是否超线」, 去重与恢复的簿记收在这里。
func SyncQualityAlert(workDir, metric, key, sev string, value, threshold float64, msg string, active bool) {
	if active {
		EmitQualityAlert(workDir, metric, key, sev, value, threshold, msg)
		return
	}
	ResolveQualityAlert(workDir, metric, key)
}
