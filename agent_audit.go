// ══════════════════════════════════════════════════════════════
// agent_audit.go — agent 侧审计埋点 (gate_audit.jsonl): 通用审计 + 无剑分子/分母埋点。
// ══════════════════════════════════════════════════════════════

package main

import (
	"os"
	"regexp"
	"strings"
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

// stripHeadRunes 剥离样本摘要的长度 (rune)。只存开头: 全文入库会淹没数据
// (gate_audit.jsonl 已 4 万行), 摘要足够判"剥掉的是分析还是废话"。
const stripHeadRunes = 40

// stripAudit 生成与执行分离的剥离埋点 (20261004)。
//
// 背景: 工具轮的 assistant 文本被程序硬剥离 (agent_stream_setup.go 的
// Content: ""), 但剥离此前零留痕 —— "剥了几次 / 剥掉多少 / 剥的是什么"
// 全不可测, 于是"这条纪律该不该留在 prompt 里"只能靠感觉(违反度量铁律)。
// 与 nswProbeAudit 同构: 分子 = 有剥离的工具轮, 分母 = 全部工具轮;
// 触发率 = 有 strip 的轮数 / 工具轮数。
//
// 返回被剥离的 rune 数 (0 = 无文字可剥)。调用方据此决定是否打回执 ——
// "判"与"显"共用同一个数, 避免两处口径分叉。
func stripAudit(a *AgentRunner, stripped string, tools, turn int) int {
	text := strings.TrimSpace(stripped)
	if text == "" {
		return 0
	}
	runes := []rune(text)
	head := string(runes)
	if len(runes) > stripHeadRunes {
		head = string(runes[:stripHeadRunes]) + "..."
	}
	appendAuditLine(a, map[string]interface{}{
		"event": "strip",
		"ts":    time.Now().Format(time.RFC3339Nano),
		"chars": len(runes),
		"tools": tools,
		"turn":  turn,
		"head":  strings.ReplaceAll(head, "\n", " "),
	})
	return len(runes)
}

// ─── 度量铁律埋点 (20261004) ──────────────────────────────────
//
// 纪律原文: 凡"改进/效率/成功率/失败率"结论必须由 quality_report.py 出数,
// 禁凭感觉; 单点极值不是证据, 分布才是。此前这条纪律全仓零判据 —— 纯 prompt。
//
// 为什么第一步只记审计、不注入: 判据是**概率型**(词表匹配), 而 netroute.go 的
// 决策记录已实证概率型判据精确率仅 1.6%~13.5%, 直接拿去拦人必然误伤成灾。
// 只记审计时假阳性成本 ≈ 一行日志, 故可用宽词表换召回率; 升级为强干预前
// 必须先看这份数据的分布 (has_source 列: 声称统计结论的轮次里有多少真读过源)。
var metricClaimRe = regexp.MustCompile(
	`(成功率|失败率|命中率|通过率|误报率|漏检率|提升|提高|降低|下降|优化|改进)` +
		`[^。！？\n]{0,12}[0-9]+(\.[0-9]+)?\s*%`)

// metricClaimWeakRe 弱信号词表 —— 只决定"要不要把样本写进审计", 不参与 hit 判定。
//
// 二次修正的产物 (20261004): 首版"只在命中时落行"实测上线后 25 次真实收尾零行 ——
// 命中率算不出(无分母), 漏报看不见(无样本), 决策依旧卡死。
// weak 让**漏报语料可见**: weak=true 且 hit=false 的行就是"正则该管却没管"的候选
// (真实输出大量是"PASS 2057 / FAIL 0"这类计数式结论, 而强判据只认"统计词+百分比")。
// 取宽无害 —— 它只影响是否记样本摘要, 不拦人。
var metricClaimWeakRe = regexp.MustCompile(`(率|%|占比|PASS|FAIL|通过|失败|命中)`)

// metricSourceKeys 数据源指纹: 统计结论必须能指到这些源才算"有源"。
var metricSourceKeys = []string{"quality_report", "gate_audit.jsonl"}

// hasMetricSource 本轮 (最后一条 user 之后) 是否真的读过数据源。
// 与虚报检测同口径 (verifyClaim.go hasToolEvidenceSinceUser): 历史轮次读过源,
// 不能冒充本轮读过了。
func hasMetricSource(messages []ChatMessage) bool {
	for _, m := range messages[lastUserIndex(messages)+1:] {
		if m.Role != "tool" {
			continue
		}
		for _, k := range metricSourceKeys {
			if strings.Contains(m.Content, k) {
				return true
			}
		}
	}
	return false
}

// metricClaimAudit 度量铁律埋点: 每轮收尾写一行 (分子/分母同时可算)。
//
// 与 nswProbeAudit 同构 —— 后者的注释早已写明"只有分子没有分母, 无法区分
// 『用得少』与『该触发没触发』", 本埋点首版却重犯了同一个错(只写命中行),
// 代价 = 上线半天 25 次收尾零行, 决策依旧卡死。现改为每轮必写:
//
//	hit  = 强判据命中 (统计词 + 百分比)
//	weak = 弱信号命中 (计数式结论等, 供改进强判据取语料)
//
// has_source / sample 只在 hit||weak 时写 —— 纯叙述轮无这两字段, 体积可控。
// 不注入、不阻断 —— 本函数是**观测臂**, 不是拦截臂。
func metricClaimAudit(a *AgentRunner, asst string, messages []ChatMessage) {
	loc := metricClaimRe.FindStringIndex(asst)
	hit := loc != nil
	weak := metricClaimWeakRe.MatchString(asst)
	e := map[string]interface{}{
		"event": "metric_claim",
		"ts":    time.Now().Format(time.RFC3339Nano),
		"hit":   hit,
		"weak":  weak,
	}
	if hit || weak {
		head := []rune(asst)
		if loc != nil {
			head = []rune(asst[loc[0]:])
		}
		if len(head) > 40 {
			head = head[:40]
		}
		e["sample"] = strings.ReplaceAll(string(head), "\n", " ")
		e["has_source"] = hasMetricSource(messages)
	}
	appendAuditLine(a, e)
}
