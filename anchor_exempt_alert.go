package main

// anchor_exempt_alert.go — 锚点豁免与拒绝的可见化 (20261012)
//
// 两件事, 同一病根「检测到 != 送到人眼前」:
//  ① 配额拒绝留痕 (T29-B): anchorGuardCheck 拒绝时原本只 return error, 零记录 ——
//     被拒频率与撞墙时刻无从统计 (拒了但没账 = 不可测量即不可管理);
//  ② 豁免超阈值接告警出口 (T29-A): 豁免数早已在 /anchor 摘要里打印, 却从未写
//     alerts.jsonl → 启动横幅与 /health 看不见, 只有主动敲 /anchor 才知道。
//
// 判据方向: 拒绝条目只留痕, 不占配额 —— 它不是一次写入; 拦截语义 (只看 counted)
// 逐字节不变。恢复水位也由本文件写 (指标回落 → resolved), 防「修好了还喊」。
//
// 与 Python 侧同源: memory_write.py 的 today_anchor_count 同样跳过 rejected 条目
// (跨语言清单由 anchor_fields_sentinel_test.go 钉住), 否则 Python 侧会把拒绝
// 当成一次写入 → 假拦截。

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// 告警键: metric 带归属前缀 anchor_, 与其它脚本的告警互不干扰 (共用 alerts.jsonl)。
const (
	anchorExemptMetric = "anchor_exempt"
	anchorExemptKey    = "daily"
)

// appendAnchorAudit 写一条锚点审计 (单一写盘实现: 正常写入与拒绝留痕共用)。
// 写失败静默 —— 审计是旁路, 不能反过来阻断记忆写入。
func appendAnchorAudit(workDir string, e AnchorAuditEntry) {
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(anchorAuditPath(workDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(string(line) + "\n")
}

// anchorGuardRejectAudit 配额拒绝留痕 (rejected=true, 不占配额)。
// 字段为空数组: 拒绝发生在写入之前, 没有字段差异可言 —— 留的是"撞墙时刻与次数"。
func anchorGuardRejectAudit(workDir string, total, counted int) {
	appendAnchorAudit(workDir, AnchorAuditEntry{
		Time:     time.Now().Format(time.RFC3339),
		File:     "memory.json",
		Fields:   []string{},
		Reason:   fmt.Sprintf("quota_rejected: 今日锚点改动 %d 次 (计配额 %d 次)", total, counted),
		Rejected: true,
	})
}

// syncAnchorExemptAlert 豁免超阈值 → 告警出口; 回落到阈值以下 → 写恢复水位。
// 阈值来源与 /anchor 摘要同一个常量 —— 摘要与告警不会各说各话。
func syncAnchorExemptAlert(workDir string) {
	total, counted, err := anchorAuditTodayStats(workDir)
	if err != nil {
		return
	}
	exempt := total - counted
	msg := fmt.Sprintf("今日锚点豁免 %d 次 (阈值 %d): 配额被绕行, 缓存重置成本按 %d 次计",
		exempt, anchorExemptWarnThreshold, total)
	SyncQualityAlert(workDir, anchorExemptMetric, anchorExemptKey, "warn",
		float64(exempt), float64(anchorExemptWarnThreshold), msg, exempt >= anchorExemptWarnThreshold)
}
