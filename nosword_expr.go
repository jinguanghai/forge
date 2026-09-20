package main

import (
	"os"
	"regexp"
	"strings"
	"time"
)

// ── 表达式化强制 + 拒绝权 (20260920, 无剑二期) ──────────────────────────
//
// 背景: 一期无剑是"事后嗅探" —— LLM 先自由书写算式与结果, 死程序再去猜哪一段
// 是算式 (nswSniffCands 词法切分 + nswCtxReject 语境闸)。猜的能力有天花板:
// 中文算式、句中算式系统性漏检 (见 nosword.go 已知盲区注释)。
//
// 二期改为"事前声明": LLM 把需要计算的数值写成 {{算式}}, 死程序只求值不猜 ——
// 从"猜哪段是算式"变成"被告知哪段是算式"。判据由词法切分换成解释器执行。
//
// 三层互补 (旧路径不删):
//  ① 标记求值 — 模型主动声明, 覆盖率高 (本文件)
//  ② 嗅探兜底 — 一期路径保留, 未标记的裸算式仍走 nswIntervene 反馈
//  ③ 拒绝权   — 标记内非纯算式时死程序拒绝执行并回告模型 (本文件)
//
// 缓存纪律: 约束文本是固定常量, 只在开关开启时追加到 system 稳定段尾部,
// 逐字节恒定 → 不打断前缀缓存。历史存原文 (模型下轮看到自己的标记格式,
// 格式自维持), 只有展示层做替换。

// nswExprMarkRe 匹配 {{...}}, 内容不含花括号 (嵌套不是合法算式)。
var nswExprMarkRe = regexp.MustCompile(`\{\{([^{}]*)\}\}`)

const (
	nswExprOpen  = "{{"
	nswExprClose = "}}"
)

// nswExprConstraint 追加到 system 稳定段尾部的固定约束文本 (逐字节恒定)。
//
// 措辞要点 (20260920 实测): 必须写成"规则5 的补充"而非独立要求。实测对比 ——
// 措辞写成"凡需要算出结果的数值一律标记"时, 模型在有工具的语境下 0/2 遵守
// (它坚持调 math gate, 甚至在没有 tools 参数时输出伪 tool_call);
// 改成"当你不调用工具、要在正文里直接给数值时"后, 无工具场景 3/3 遵守。
// 根因: 独立措辞与 system 里的"算数必须用 forge 计算"规则竞争且被压制,
// 协同措辞则让两条路径分工 —— 有工具走工具, 没工具走标记。
const nswExprConstraint = "\n<numeric_output>\n" +
	"补充规则 5：当你不调用工具、要在正文里直接给出需要计算得到的数值时，" +
	"不要自己算，写成 {{算式}} 交给系统求值替换。\n" +
	"例：一共 {{35*30}} 元。标记内只放纯算式（数字与 + - * / ( ) 小数点）。\n" +
	"引用已有数据、日期、序号、剂量等不需要计算的数字照常直接写。\n" +
	"</numeric_output>\n"

// nswExprEnabled 表达式化开关 (独立于 FORGE_NOSWORD, 便于灰度验证)。
func nswExprEnabled() bool { return os.Getenv("FORGE_NSW_EXPR") == "1" }

// nswExprEvalMark 求值单个标记内容, 返回 (结果文本, 是否成功)。
//
// 走 nswEvalExplicit (三十三期): 标记是模型的意图声明, 故跳过"数值范围歧义"闸
// (nswRangeRe 等) —— 否则 {{100-37}} 这类最基础的减法会被拒, 拒绝权回告后
// 模型还得改写一轮, 显式声明的信息价值等于零。
// 语法闸 (字符集/悬空点/纯常量)、形态闸 (日期/电话/编号) 与精度闸 (2^53/NaN/Inf)
// 照旧生效: 死程序仍不产出错值。
func nswExprEvalMark(inner string) (string, bool) {
	expr := strings.TrimSpace(inner)
	if expr == "" {
		return "", false
	}
	return nswEvalExplicit(expr)
}

// nswFenceToggle 统计 s 中 ``` 的出现次数, 奇数次翻转代码围栏状态。
func nswFenceToggle(in bool, s string) bool {
	if strings.Count(s, "```")%2 == 1 {
		return !in
	}
	return in
}

// nswExprRenderFrom 从给定围栏状态开始替换 text 中的标记。
// 返回 (渲染文本, 新围栏状态, 求值失败的标记列表)。
// 围栏内的标记原样保留 (代码块里的 {{}} 是模板语法, 不是算式);
// 求值失败的标记也原样保留 —— 不静默吞掉, 让模型与用户都看得见。
func nswExprRenderFrom(text string, inFence bool) (string, bool, []string) {
	var sb strings.Builder
	var bad []string
	last := 0
	for _, m := range nswExprMarkRe.FindAllStringSubmatchIndex(text, -1) {
		seg := text[last:m[0]]
		sb.WriteString(seg)
		inFence = nswFenceToggle(inFence, seg)
		last = m[1]
		raw := text[m[0]:m[1]]
		if inFence {
			sb.WriteString(raw)
			continue
		}
		if v, ok := nswExprEvalMark(text[m[2]:m[3]]); ok {
			sb.WriteString(v)
			continue
		}
		bad = append(bad, strings.TrimSpace(text[m[2]:m[3]]))
		sb.WriteString(raw)
	}
	sb.WriteString(text[last:])
	return sb.String(), inFence, bad
}

// nswExprRender 一次性替换 text 中的全部标记 (非流式入口 / 拒绝权扫描)。
func nswExprRender(text string) (string, []string) {
	out, _, bad := nswExprRenderFrom(text, false)
	return out, bad
}

// nswExprPendingTail 返回必须留在缓冲区中的尾部字节数。
// 未闭合的 {{... 或孤立的 { 都可能被下一个分块补全, 提前渲染会把半个算式吐给用户。
func nswExprPendingTail(s string) int {
	if i := strings.LastIndex(s, nswExprOpen); i >= 0 {
		if !strings.Contains(s[i+len(nswExprOpen):], nswExprClose) {
			return len(s) - i
		}
	}
	if strings.HasSuffix(s, "{") {
		return 1
	}
	return 0
}

// ── 流式过滤器 ────────────────────────────────────────────────

// nswExprFilter 流式标记过滤器: 标记可能跨分块到达 (如 "{{480*0" + ".7-50}}"),
// 未闭合的尾部必须暂存到闭合后再替换。
type nswExprFilter struct {
	buf   strings.Builder
	fence bool
	marks int      // 已渲染的标记数 (审计)
	saved int      // 显式口径救回的标记数 (审计, 三十三期)
	bad   []string // 被拒绝的标记 (审计)
}

// feed 返回可以立即渲染的文本 (不含未决标记)。
func (f *nswExprFilter) feed(chunk string) string {
	f.buf.WriteString(chunk)
	s := f.buf.String()
	hold := nswExprPendingTail(s)
	ready := s[:len(s)-hold]
	f.buf.Reset()
	f.buf.WriteString(s[len(s)-hold:])
	if ready == "" {
		return ""
	}
	out, nf, bad := nswExprRenderFrom(ready, f.fence)
	f.fence = nf
	f.bad = append(f.bad, bad...)
	f.marks += len(nswExprMarkRe.FindAllString(ready, -1))
	f.saved += nswExprCountSaved(ready)
	return out
}

// flush 吐出缓冲区残余 (流结束时未闭合的标记按原文输出)。
func (f *nswExprFilter) flush() string {
	s := f.buf.String()
	f.buf.Reset()
	if s == "" {
		return ""
	}
	out, nf, bad := nswExprRenderFrom(s, f.fence)
	f.fence = nf
	f.bad = append(f.bad, bad...)
	f.marks += len(nswExprMarkRe.FindAllString(s, -1))
	f.saved += nswExprCountSaved(s)
	return out
}

// ── 拒绝权 ────────────────────────────────────────────────────

// nswExprRejectText 拒绝权: 扫描 asst 中被死程序拒绝求值的标记, 生成回告模型的
// 反馈文本 (固定格式, 同输入逐字节相同 → 缓存友好)。非空 = 应注入一轮让模型改写。
// 这是"解释器拒绝权"的落地: 死程序不接受非法表达式, 并把原因回告模型。
func nswExprRejectText(asst string) string {
	_, bad := nswExprRender(asst)
	if len(bad) == 0 {
		return ""
	}
	seen := map[string]bool{}
	var sb strings.Builder
	sb.WriteString("以下数值标记无法求值（标记内必须是纯算式：数字与 + - * / ( ) 小数点，不含单位、汉字、等号）：\n")
	for _, b := range bad {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		sb.WriteString("「" + b + "」\n")
	}
	if len(seen) == 0 {
		return ""
	}
	sb.WriteString("请把这些数值改成可直接计算的纯算式标记，或直接写出准确数值。\n")
	return sb.String()
}

// ── 埋点 ──────────────────────────────────────────────────────

// nswExprCountSaved 统计"显式口径救回"的标记数: 这些标记在显式口径下求值成功,
// 但在嗅探口径下会被拒 (典型 {{100-37}})。三十三期的收益由此可度量 ——
// 没有这个数就只能靠"感觉更好了"论证改动价值。
// 围栏内的标记也会被计入 (近似计数): 埋点用于观测量级, 不值得为此复制一套围栏状态机。
func nswExprCountSaved(text string) int {
	n := 0
	for _, m := range nswExprMarkRe.FindAllStringSubmatch(text, -1) {
		inner := strings.TrimSpace(m[1])
		if inner == "" {
			continue
		}
		if _, ok := nswEvalExplicit(inner); !ok {
			continue
		}
		if _, ok := nswEval(inner); !ok {
			n++
		}
	}
	return n
}

// nswExprAudit 表达式化埋点 (20260920): 记录每轮最终回复的标记数、被拒数与
// 显式口径救回数, 让"表达式化到底被用过几次、三十三期改动救回多少"可度量。
// 教训: 一期无剑上线时零埋点, 触发率与空转率完全不可测, 谈扩语法只能盲扩 ——
// 二期先埋点, 再谈是否启用与如何扩。
func nswExprAudit(a *AgentRunner, marks, rejected, saved int) {
	appendAuditLine(a, map[string]interface{}{
		"event":    "nosword_expr",
		"ts":       time.Now().Format(time.RFC3339Nano),
		"enabled":  true,
		"marks":    marks,
		"rejected": rejected,
		"saved":    saved,
	})
}
