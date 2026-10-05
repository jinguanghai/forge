// nosword_cand.go: 算式候选裁决 — 是否值得求值 / 上下文否决 / 候选提取

package main

import (
	"strconv"
	"strings"
	"unicode"
)

// nswIsCandidate 嗅探口径的候选闸 (explicit=false): 全部闸生效。
// 保留原签名供一期路径与既有测试使用, 行为逐字节不变。
func nswIsCandidate(s string) bool { return nswIsCandidateMode(s, false) }

// nswIsCandidateMode 候选闸。explicit=true 走显式标记口径 (三十三期, 见 nswEvalExplicit):
// 只跳过"数值范围歧义"闸, 语法闸 / 形态闸 / 精度闸两条路径共用。
func nswIsCandidateMode(s string, explicit bool) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(unicode.IsDigit(r) || r == '.' || r == '+' || r == '-' || r == '*' || r == '/' || r == '^' || r == '(' || r == ')' || r == ',' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == ' ' || r == '\t') {
			return false
		}
	}
	// ── 形态闸 (两条路径共用) ────────────────────────────────────
	// 这些不是"范围歧义", 而是高置信度的非算式形态 (日期/电话/编号/超长整数),
	// 即使模型显式标记也拒绝 —— 算出的错值污染上下文, 危害远大于漏算。
	//
	// 多段纯数字减号串拒绝 (20260913 缺陷P): 电话 "138-1234-5678" / CAS 号 "50-78-2" /
	// 编号 "1-2-3" / 美式日期 "9-13-2026" 全落此形, 此前被当连减算出 -6774 / -30 / -4 / -2030
	// 等错值注入 LLM 上下文 —— 违反本文件"死程序产出错值危害远大于漏算"的原则。
	// 两段对已由 nswRangeRe 全拒, 三段以上此前漏网。段数 >= 3 一律拒绝: 编号/电话/日期
	// 是自然语言中的高频来源, 而真连减 (如 10-4-3) 罕见且漏算代价仅为"不纠正"。
	// 前导负号 (-1-2-3) 与括号形式 ((-1)-2-3) 不匹配本正则, 照旧求值 (它们是算式形态)。
	if nswDateRe.MatchString(s) || nswYearMonRe.MatchString(s) ||
		nswMultiSegRe.MatchString(s) || nswHugeIntRe.MatchString(s) ||
		nswRangeDivRe.MatchString(s) {
		return false
	}
	if !explicit {
		// ── 数值范围歧义闸 (仅嗅探路径) ──────────────────────────
		// 存在理由: 嗅探输入无位置信息 —— "3-5" 既可能是区间也可能是减法,
		// 中文文本里区间高频, 故一律拒绝 (宁漏勿误)。
		// 显式标记 {{...}} 是模型的意图声明, 歧义已消解 → 跳过。
		if nswRangeRe.MatchString(s) || nswUnaryBare.MatchString(s) ||
			nswLeadZeroRe.MatchString(s) {
			return false
		}
	}
	// 悬空点号拒绝 (20260910 缺陷C): "[0-9.]+$" 经嗅探切分后剩 "0-9.", 会被
	// number() 读成 "9." = 9 → 算出 -9 (正则片段被当算式)。点号后非数字即非法字面量。
	if nswHasDanglingDot(s) {
		return false
	}
	if !explicit {
		// 缺陷R (20260913): 整串 = 一对半角括号 + 纯整数区间 → 区间/编号语义, 一律拒
		// (与 nswRangeRe 同口径)。嵌套算式 "(10-4)-3" 不以 ')' 结尾, 不落此判据。
		if nswParenRangeRe.MatchString(s) {
			return false
		}
		// 缺陷S (20260913): 权重表 "0.45/+0.12" 被当除法算出 3.75。J4 斜杠列举要求
		// >=3 段纯数字, 此形态只有 2 段且带显式正号 → 漏网。算式里 "/+" 几乎不写
		// (a/+b ≡ a/b), 而数值列举中 "+0.12" 是显式带号写法 (增量/正项)。
		// 只认 '+' 不认 '-' —— 负分数 "-3/4" 是常见算式, 不得误伤。
		if nswSlashPlusRe.MatchString(s) {
			return false
		}
		// 裸数对减号消歧 (20260910 缺陷A修复): 含小数时按数值序判定 ——
		//   升序 (A < B) 是区间语义 ("价格 12.5-18.9 元") → 拒绝;
		//   降序 (A > B) 保留为减法 ("18.9-12.5" → 6.4)。
		// 纯整数对由上方 nswRangeRe 全拒 (中文 "3-5个工作日" 高频, 宁漏勿误), 不会走到这里。
		if m := nswNumPairRe.FindStringSubmatch(s); m != nil &&
			(strings.Contains(m[1], ".") || strings.Contains(m[2], ".")) {
			a, e1 := strconv.ParseFloat(m[1], 64)
			b, e2 := strconv.ParseFloat(m[2], 64)
			if e1 == nil && e2 == nil && a < b {
				return false
			}
		}
	}
	stripped := strings.NewReplacer("(", "", ")", "", ",", "").Replace(s)
	if nswBareConstRe.MatchString(stripped) {
		return false
	}
	if !strings.ContainsAny(s, "0123456789") {
		return false
	}
	hasOp := strings.ContainsAny(s, "+-*/^")
	hasFn := strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	return hasOp || hasFn
}

// nswCand 候选及其在原文中的位置 (rune 偏移, [start, end))
type nswCand struct {
	expr         string
	start, end   int
	trimmedLeft  string // 被 Trim("+*/^,") 去掉的首部 (Markdown 强调符检测)
	trimmedRight string
}

// nswSniffCands 保留位置与被修剪字符的嗅探器 (语境判据在 nswCtxReject)
func nswSniffCands(text string) []nswCand {
	// 范围符 (20260913 缺陷Q): en dash / em dash / 全角减号 / 波浪号必须留在候选内,
	// 否则 "1–12 / 1–31" 被切成三段, 中间段 "12 / 1" 成了完整算式 (实测算出 12)。
	// 留在候选内后由 nswIsCandidate 的字符白名单整段拒绝 (与"宁漏勿误"口径一致)。
	opSet := func(r rune) bool {
		return unicode.IsDigit(r) || r == '.' || r == '+' || r == '-' || r == '*' || r == '/' || r == '^' || r == '(' || r == ')' || r == ',' || r == '%' ||
			r == '–' || r == '—' || r == '−' || r == '－' || r == '～' || r == '〜' || r == '~' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' ||
			r == ' ' || r == '\t'
	}
	var cands []nswCand
	var cur []rune
	curStart, pos := 0, 0
	flush := func() {
		if len(cur) > 0 {
			raw := string(cur)
			rw := []rune(raw)
			s := strings.TrimSpace(raw)
			lead := len(rw) - len([]rune(strings.TrimLeft(raw, " \t")))
			core := strings.Trim(s, "+*/^,")
			lead2 := len([]rune(s)) - len([]rune(strings.TrimLeft(s, "+*/^,")))
			trail := len([]rune(s)) - len([]rune(strings.TrimRight(s, "+*/^,")))
			if core != "" {
				cands = append(cands, nswCand{
					expr:         core,
					start:        curStart + lead + lead2,
					end:          curStart + lead + len([]rune(s)) - trail,
					trimmedLeft:  string(rw[lead : lead+lead2]),
					trimmedRight: string(rw[lead+len([]rune(s))-trail : lead+len([]rune(s))]),
				})
			}
			cur = nil
		}
	}
	prevSpace := false
	for _, r := range text {
		if r == ' ' || r == '\t' {
			if prevSpace {
				flush()
				pos++
				continue
			}
			prevSpace = true
			if len(cur) == 0 {
				curStart = pos
			}
			cur = append(cur, r)
			pos++
			continue
		}
		prevSpace = false
		if opSet(r) {
			if len(cur) == 0 {
				curStart = pos
			}
			cur = append(cur, r)
		} else {
			flush()
		}
		pos++
	}
	flush()
	return cands
}

// nswCtxReject 语境否决 —— 引用位置的算式不求值 (J0~J4)
func nswCtxReject(text string, c nswCand) bool {
	rs := []rune(text)
	n := len(rs)
	// J6 元语境行: 含铸剑炉自身反馈前缀的行整行不求值 (斩断自激回路, 先判最便宜)
	if nswEvalMarkLine(rs, c.start) {
		return true
	}
	// J0 反引号: 引用语义最强标记 —— LLM 写断言时不会给算式加反引号
	if i := c.start - 1; i >= 0 {
		for i >= 0 && (rs[i] == ' ' || rs[i] == '\t') {
			i--
		}
		if i >= 0 && rs[i] == '`' {
			return true
		}
	}
	if j := c.end; j < n {
		for j < n && (rs[j] == ' ' || rs[j] == '\t') {
			j++
		}
		if j < n && rs[j] == '`' {
			return true
		}
	}
	// J0b ``` 代码块内部
	if nswInCodeFence(rs, c.start) {
		return true
	}
	// J0c 行内代码跨度: 反引号包短语时 J0 的“紧邻”判据失效 (见 nswInInlineCode)
	if nswInInlineCode(rs, c.start) {
		return true
	}
	// J1 Markdown 强调符包裹: "**0/4**" 被 Trim("+*/^,") 剥成裸算式 0/4
	if strings.Contains(c.trimmedLeft, "*") || strings.Contains(c.trimmedRight, "*") {
		return true
	}
	// J2 冒号前缀 (紧邻, 不跳空白): 时间 "9:30+60" / 比例 "1:5"
	if c.start > 0 && (rs[c.start-1] == ':' || rs[c.start-1] == '：') {
		return true
	}
	// J3 表格行
	if nswTableRow(rs, c.start) {
		return true
	}
	// J4 斜杠列举: "0.02/1/4" 是价格档位/命中-未命中-输出三档, 不是连除。
	// 判据精确到"整串仅由 数字(/数字){2,} 构成" —— 含任何其他运算符 (如 "37/36^2/45/43^1")
	// 或不足 3 段 (如 "10/4") 一律不拒, 故对 1000 例对拍零影响 (实测命中 0 例)。
	if nswSlashListRe.MatchString(c.expr) {
		return true
	}
	// J5 比例陈述 (20260912 缺陷④-a): 候选后无紧跟数值 且 (前置比例量词 或
	// 纯分数形式) → 引用比例, 不是断言算式。
	// 例: "交付率 8/13, 交付后正确率 5/8, 数学子项 2/3, 总分 5/13" (候选后是逗号/
	// 句号) → 4 个锚点全拒。此前它们全部注入反馈, 把 LLM 从用户任务上拽到求值
	// 噪音上 (实际危害最大的一条: 反馈污染任务)。
	// 为什么必须带"后无数值"闸门: 只看前置形态会误伤真纠错 —— "占比 3/4 = 0.8"
	// 的错值恰恰该抓, 而它带结论(后有数值) → 不拒。
	// 为什么加"纯分数 + 前置汉字"分支: 紧邻量词枚举必然漏网 (实测 "数学子项 2/3" 的
	// '项' 曾不在集合里); 而审计 34 个锚点中 19 个是纯分数形式, 且全部是引用比例 ——
	// 形态比量词更本质。含其他运算符 (3/4+1/4) 或带结论的一律不拒。
	// 前置"中文语境"(汉字或中文标点, 见 nswCJKBefore) 而非"量词"是为了不误伤裸算式
	// 夹具 (如整串 "10/4" / "1/3" —— 无中文语境, 不是比例陈述, 照旧求值);
	// 真实文本里的分数必带中文语境。
	// 缺陷T (20261004): 原判据只认汉字, 全角括号 "（1080/1081）" 漏网 (审计实证误算)。
	pureFrac := nswPureFractionRe.MatchString(c.expr)
	if !nswNumFollows(rs, c.end) &&
		((pureFrac && nswCJKBefore(rs, c.start)) || (!pureFrac && nswRatioWordBefore(rs, c.start))) {
		return true
	}
	// J8 斜杠带显式正号 (20260913 缺陷S): "+0.45/0.12" 的前导 '+' 被
	// flush 的 Trim("+*/^,") 剥成 trimmedLeft, expr 内看不到 → 在此补判。
	// 仅当 expr 是纯分数形式时才拒, 避免误伤 "+1/2+1/3" 这类真算式。
	if strings.Contains(c.trimmedLeft, "+") && nswPureFractionRe.MatchString(c.expr) {
		return true
	}
	return false
}
