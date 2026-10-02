// ══════════════════════════════════════════════════════════════
// agent_audit.go — agent 侧审计埋点 (gate_audit.jsonl): 通用审计 + 无剑分子/分母埋点。
// ══════════════════════════════════════════════════════════════

package main

import (
	"os"
	"time"
)

// appendAuditLine 追加一行 JSON 到 gate_audit.jsonl (agent 侧审计)。
// 与 forge 侧 auditGate 共用开关 FORGE_GATE_AUDIT=0; 写失败静默 (不阻断主流程)。
func appendAuditLine(a *AgentRunner, entry map[string]interface{}) {
	if os.Getenv("FORGE_GATE_AUDIT") == "0" {
		return
	}
	dir := ""
	if a != nil && a.cfg != nil {
		dir = a.cfg.WorkDir
	}
	appendAuditJSONL(auditFilePath(dir), entry)
}

// nswAudit 无剑埋点 (20260911): 让"触发几次 / 其中多少是空转"可度量。
// 此前 gate_audit.jsonl 20637 行里零条 nsw 记录 -> 触发率、空转率完全不可测,
// 谈扩语法只能盲扩(无法回答"木剑是在拦幻觉还是在自己制造开销")。
// corrected = 原文写错被纠正 (无剑的核心价值); completed = 原文只写算式未给结论。
// 被复述过滤掉的纯冗余只进 skipped 计数。
func nswAudit(a *AgentRunner, asst string, fresh []nswAnchor, total int) {
	if len(fresh) == 0 {
		return
	}
	corrected, completed := 0, 0
	exprs := make([]string, 0, 8)
	for _, an := range fresh {
		if nswClassifyAnchor(asst, an) == "corrected" {
			corrected++
		} else {
			completed++
		}
		if len(exprs) < 8 {
			exprs = append(exprs, an.expr+"="+an.val)
		}
	}
	appendAuditLine(a, map[string]interface{}{
		"event":     "nosword",
		"ts":        time.Now().Format(time.RFC3339Nano),
		"anchors":   total,
		"fresh":     len(fresh),
		"skipped":   total - len(fresh),
		"corrected": corrected,
		"completed": completed,
		"exprs":     exprs,
	})
}

// nswProbeAudit 无剑分母埋点 (20260912): 每轮"最终收尾"写一行, 让触发率可算
// = 有 fresh 的轮数 / 总收尾轮数。此前只有分子(nosword 事件)没有分母, 无法区分
// "用得少"与"该触发没触发"。enabled=false 的轮次同样记录(关闸期的分母)。
// source: plain=纯文本收尾, frag=工具碎片全无效降级收尾。
func nswProbeAudit(a *AgentRunner, asst string, rounds int, source string) {
	enabled := nswEnabled()
	anchors, fresh := 0, 0
	if enabled {
		_, f, t := nswFeedbackTextFresh(asst)
		anchors, fresh = t, len(f)
	}
	appendAuditLine(a, map[string]interface{}{
		"event":   "nosword_probe",
		"ts":      time.Now().Format(time.RFC3339Nano),
		"enabled": enabled,
		"rounds":  rounds,
		"anchors": anchors,
		"fresh":   fresh,
		"source":  source,
	})
}
