// nosword.go: 无剑求值引擎 — 对外接口与反馈格式化 (确定性死程序, 公理二落地)

package main

import (
	"math"
	"os"
	"strconv"
	"strings"
)

func nswEvaluate(text string) []nswAnchor {
	seen := map[string]bool{}
	var out []nswAnchor
	for _, c := range nswSniffCands(text) {
		// 语境闸 (20260912 缺陷N): 引用位置的算式不是断言, 不求值
		if nswCtxReject(text, c) {
			continue
		}
		cand := c.expr
		v, ok := nswEval(cand)
		if !ok {
			continue
		}
		// 去重键用表达式而非结果值 (20260910 缺陷E): 按值去重会让 "2+3" 和 "4+1"
		// 只留其一, 锚点列表不完整; 更糟的是它会掩盖同值不同式中的错值。
		if seen[cand] {
			continue
		}
		seen[cand] = true
		out = append(out, nswAnchor{expr: cand, val: v})
	}
	return out
}

// nswEnabled 无剑求值旁路开关: FORGE_NOSWORD=1 启用
func nswEnabled() bool {
	return os.Getenv("FORGE_NOSWORD") == "1"
}

// nswFilterEchoed 过滤掉"原文已自行给出同形正确结论"的锚点 (20260911 缺陷M)。
//
// 判据: nswEchoed —— 原文中 expr 之后紧跟的数值 == 死程序算出的 val。
// 与埋点口径同源 (二者共用 nswTrailingNum+nswNumEqual), 不会各自漂移。
// 此时反馈是纯复述, 零信息增量 -> 跳过。
// 保留 corrected(原文写错) 与 completed(原文未给结论) -> 反馈的价值恰在这两类。
//
// 为什么不用"已反馈过的锚点"去重表: 那会把 LLM **重复同一个错误**也一并跳过
// (死程序测试抓出的缺陷: E2E 用例里 LLM 连续两轮输出 "2+2 = 5", 第二轮不再纠正)。
// 本方案无状态、纯函数, 且语义精确 —— 跳过的充要条件是"反馈没有新信息"。
func nswFilterEchoed(asst string, anchors []nswAnchor) []nswAnchor {
	var out []nswAnchor
	for _, a := range anchors {
		if nswEchoed(asst, a) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// nswFormatAnchors 锚点列表 → 稳定低熵反馈文本 (空列表返回 "")
func nswFormatAnchors(anchors []nswAnchor) string {
	if len(anchors) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【求值】")
	for i, a := range anchors {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString("「" + a.expr + " = " + a.val + "」")
	}
	return b.String()
}

// nswFeedbackTextFresh 只对"原文未给出正确结论"的锚点生成反馈。
// 返回 (反馈文本, 有效锚点, 原文锚点总数)。反馈文本为空 = 原文锚点全是纯复述, 不注入。
func nswFeedbackTextFresh(asst string) (string, []nswAnchor, int) {
	all := nswEvaluate(asst)
	fresh := nswFilterEchoed(asst, all)
	return nswFormatAnchors(fresh), fresh, len(all)
}

// nswTrailingNum 原文中 expr 之后是否紧跟一个数值 (返回该数值串, 无则 "")。
// 与 nswEchoed 共用同一套位置扫描 —— 度量口径与行为判据必须同源, 否则两处各自
// 演化必然漂移 (缺陷④-b 的教训: 注释说"不参与行为判定", 实现却在参与)。
func nswTrailingNum(asst string, a nswAnchor) string {
	idx := 0
	for {
		j := strings.Index(asst[idx:], a.expr)
		if j < 0 {
			return ""
		}
		idx = idx + j + len(a.expr)
		// TrimLeft 按 rune 集合处理: 多字节连接符('：' '→')不能用 byte 索引比较
		rest := strings.TrimLeft(asst[idx:], " \t=:：→")
		if num := nswLeadingNum(rest); num != "" {
			return num
		}
	}
}

// nswEchoed 行为判据: 原文是否已给出 "expr ... 与 val 相等的数值" 的结论。
// 这是 nswFilterEchoed 的唯一依据 —— 真判据, 会改变行为。
func nswEchoed(asst string, a nswAnchor) bool {
	num := nswTrailingNum(asst, a)
	return num != "" && nswNumEqual(num, a.val)
}

// nswClassifyAnchor 埋点用分类 —— 判断锚点在原文里是否已给出结论。
//
//	redundant = 原文已含 "expr ... val" 同形结论 (零信息增量 = 空转)
//	corrected = 原文含 expr 但紧跟的数值与 val 不同 (真纠错, 无剑的价值所在)
//	completed = 原文只写算式未写结果 (补全)
//
// 与行为的关系 (20260912 修正): redundant 分支**同时是行为判据** —— nswFilterEchoed
// 直接调 nswEchoed, 二者共用 nswTrailingNum+nswNumEqual, 结构性保证不会漂移。
// 原注释"仅用于度量, 不参与任何行为判定"与实现矛盾(会误导后来者), 已订正;
// corrected / completed 两个分支才只用于度量。
//
// 口径局限: 原文写四舍五入短形式 (0.1268 vs 精确值 0.12682357518, 相对差 1.9e-4)
// 仍计入 corrected。这是有意选择 —— 宁可高估纠错、不可低估(低估会掩盖真实纠错)。
func nswClassifyAnchor(asst string, a nswAnchor) string {
	num := nswTrailingNum(asst, a)
	if num == "" {
		return "completed"
	}
	if nswNumEqual(num, a.val) {
		return "redundant"
	}
	return "corrected"
}

// nswLeadingNum 取字符串前导数字串 (可含正负号与小数点), 无则返回 ""
func nswLeadingNum(s string) string {
	i := 0
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	start := i
	dot := false
	for i < len(s) {
		c := s[i]
		if c >= '0' && c <= '9' {
			i++
			continue
		}
		if c == '.' && !dot {
			dot = true
			i++
			continue
		}
		break
	}
	if i == start {
		return ""
	}
	return s[:i]
}

// nswNumRelTol 数值等值比较的相对容差 (取值依据见 nswNumEqual)
const nswNumRelTol = 1e-9

// nswNumEqual 数值字符串等值比较 (容忍书写差异: "0.3" / ".3" / "0.30", 以及
// 四舍五入/截断造成的末位差)。判据 = 精确相等 或 相对差 < nswNumRelTol。
//
// 为什么不是精确相等 (20260912 缺陷④-b): LLM 写精确值 0.6153846153846154, 死程序
// 算 12 位 0.615384615385, 精确比较判"不等" -> 锚点被标 corrected -> 注入反馈要求
// "修正"一个本来正确的值。实测该场景下假纠错占 corrected 的 80%, 该口径不可信。
//
// 容差依据: 1e-9 远宽于真实书写/舍入误差 (本例实测相对差 6.3e-13), 又远窄于任何
// 真实错值 (真错值不会只差 1e-9 的量级)。0 vs 0 走精确相等分支。
func nswNumEqual(a, b string) bool {
	x, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil {
		return false
	}
	if x == y {
		return true
	}
	m := math.Max(math.Abs(x), math.Abs(y))
	if m == 0 {
		return false
	}
	return math.Abs(x-y)/m < nswNumRelTol
}
