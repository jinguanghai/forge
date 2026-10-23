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
//
// 结构 (T61 拆分, 20261018): 白名单 → 列表前缀复判 → 共用形态闸 → 嗅探专属歧义闸 → 算式体判定。
// 各闸门互不依赖且均为纯判定 (无副作用), 故相邻闸门的先后顺序不影响判决 —— 这一点是
// 拆分的等价性依据, 不是"看起来一样"。
func nswIsCandidateMode(s string, explicit bool) bool {
	s = strings.TrimSpace(s)
	if s == "" || !nswAllExprRunes(s) {
		return false
	}
	// 列表前导符剥离复判 (20261015 缺陷U): Markdown 列表项 "- 10-14 全天" 的候选是
	// "- 10-14" —— 前导 "- " 使全部区间/日期闸 (都锚定 ^\d) 失配, 于是被当连减算出 -24
	// 注入 LLM 上下文 (实测)。判据: 前导 '-'/'+' 且其后紧跟空白 (列表项语法) 时剥离复判,
	// 内层被拒则整体拒。
	//
	// 为什么必须要求"后跟空白": 无空白的 "-1-2-3" 是前导负号算式 (缺陷P 红线), 照旧求值。
	// 剥离只增拒不求值 —— 内层通过时仍按原串求值 (如 "- 3+5" 照走一元负号路径)。
	// 只作用于嗅探口径: 显式 {{...}} 是意图声明, 不因前导符号改判。
	// 递归必然终止: 每层至少剥掉一个符号加一个空白, 串长严格递减。
	if !explicit && nswListPrefixRejects(s) {
		return false
	}
	if nswRejectShape(s) {
		return false
	}
	// 悬空点号拒绝 (20260910 缺陷C): "[0-9.]+$" 经嗅探切分后剩 "0-9.", 会被
	// number() 读成 "9." = 9 → 算出 -9 (正则片段被当算式)。点号后非数字即非法字面量。
	// 本条与 explicit 无关, 故与上一闸并列于共用段 (拆分前位于嗅探专属闸之后, 顺序无关)。
	if nswHasDanglingDot(s) {
		return false
	}
	if !explicit && nswRejectSniffOnly(s) {
		return false
	}
	return nswHasExprBody(s)
}

// nswExprRuneTable ASCII 算式字符表 (构造期填充后只读)。
// 用查表而非 switch: gocyclo 对 switch 每个 case 计 1, 15 个 case 等于没拆。
var nswExprRuneTable = func() (t [128]bool) {
	for _, r := range "0123456789.+-*/^(),_ \t" {
		t[r] = true
	}
	for r := 'a'; r <= 'z'; r++ {
		t[r] = true
	}
	for r := 'A'; r <= 'Z'; r++ {
		t[r] = true
	}
	return
}()

// nswIsExprRune 单字符是否属算式白名单。
// 非 ASCII 走 unicode.IsDigit —— 与拆分前的 `unicode.IsDigit(r) || ...` 逐字等价:
// 全角/其他文种数字仍被放行 (后续闸门照旧裁决), 不得收窄。
func nswIsExprRune(r rune) bool {
	if r < 128 {
		return nswExprRuneTable[r]
	}
	return unicode.IsDigit(r)
}

// nswAllExprRunes 字符白名单: 全部字符落在算式字符集内才可能是候选。
func nswAllExprRunes(s string) bool {
	for _, r := range s {
		if !nswIsExprRune(r) {
			return false
		}
	}
	return true
}

// nswListPrefixRejects 列表前导符剥离复判 (缺陷U)。见 nswIsCandidateMode 内注释。
func nswListPrefixRejects(s string) bool {
	if len(s) < 2 || (s[0] != '-' && s[0] != '+') || (s[1] != ' ' && s[1] != '\t') {
		return false
	}
	inner := strings.TrimLeft(s[1:], " \t")
	return inner != "" && !nswIsCandidateMode(inner, false)
}

// nswRejectShape 形态闸 (两条路径共用)。
//
// 这些不是"范围歧义", 而是高置信度的非算式形态 (日期/电话/编号/超长整数),
// 即使模型显式标记也拒绝 —— 算出的错值污染上下文, 危害远大于漏算。
//
// 多段纯数字减号串拒绝 (20260913 缺陷P): 电话 "138-1234-5678" / CAS 号 "50-78-2" /
// 编号 "1-2-3" / 美式日期 "9-13-2026" 全落此形, 此前被当连减算出 -6774 / -30 / -4 / -2030
// 等错值注入 LLM 上下文 —— 违反本文件"死程序产出错值危害远大于漏算"的原则。
// 两段对已由 nswRangeRe 全拒, 三段以上此前漏网。段数 >= 3 一律拒绝: 编号/电话/日期
// 是自然语言中的高频来源, 而真连减 (如 10-4-3) 罕见且漏算代价仅为"不纠正"。
// 前导负号 (-1-2-3) 与括号形式 ((-1)-2-3) 不匹配本正则, 照旧求值 (它们是算式形态)。
func nswRejectShape(s string) bool {
	return nswDateRe.MatchString(s) || nswYearMonRe.MatchString(s) ||
		nswMultiSegRe.MatchString(s) || nswHugeIntRe.MatchString(s) ||
		nswRangeDivRe.MatchString(s)
}

// nswRejectSniffOnly 嗅探路径专属的数值范围歧义闸 (显式标记跳过)。
//
// 存在理由: 嗅探输入无位置信息 —— "3-5" 既可能是区间也可能是减法,
// 中文文本里区间高频, 故一律拒绝 (宁漏勿误)。
// 显式标记 {{...}} 是模型的意图声明, 歧义已消解 → 跳过。
func nswRejectSniffOnly(s string) bool {
	return nswRejectRangeAmbiguous(s) || nswRejectSlashAmbiguous(s)
}

// nswRejectRangeAmbiguous 区间/裸带号/前导零。
func nswRejectRangeAmbiguous(s string) bool {
	return nswRangeRe.MatchString(s) || nswUnaryBare.MatchString(s) || nswLeadZeroRe.MatchString(s)
}

// nswRejectSlashAmbiguous 斜杠语义歧义 (缺陷R/S/V) 与裸数对升序。
func nswRejectSlashAmbiguous(s string) bool {
	// 缺陷R (20260913): 整串 = 一对半角括号 + 纯整数区间 → 区间/编号语义, 一律拒
	// (与 nswRangeRe 同口径)。嵌套算式 "(10-4)-3" 不以 ')' 结尾, 不落此判据。
	if nswParenRangeRe.MatchString(s) {
		return true
	}
	// 缺陷S (20260913) / 缺陷V (20261015): 斜杠任一侧带显式符号的数值对。
	//   S: 权重表 "0.45/+0.12" 被当除法算出 3.75 —— J4 斜杠列举要求 >=3 段纯数字,
	//      此形态只有 2 段且带显式正号 → 漏网。
	//   V: 对偶缺口 "70/-4" (git 统计 "改动 3 文件, +70/-4") 被算成 -17.5 注入上下文,
	//      同族 "+3/-2" -> -1.5。S 当年只认斜杠右侧的 '+', 故 "/-数字" 整族无闸。
	// 泛化后按"符号在斜杠右侧"鉴别 (算式 a/-b ≡ -a/b 几乎不写, 自然语言里恒为增减记法);
	// "-3/4" 的负号在分子, 仍不匹配, 照旧求值 (缺陷P keep 集合同口径)。
	if nswSlashSignedRe.MatchString(s) {
		return true
	}
	return nswRejectAscendingDecimalPair(s)
}

// nswRejectAscendingDecimalPair 裸数对减号消歧 (20260910 缺陷A修复): 含小数时按数值序判定 ——
//
//	升序 (A < B) 是区间语义 ("价格 12.5-18.9 元") → 拒绝;
//	降序 (A > B) 保留为减法 ("18.9-12.5" → 6.4)。
//
// 纯整数对由 nswRangeRe 全拒 (中文 "3-5个工作日" 高频, 宁漏勿误), 不会走到这里。
func nswRejectAscendingDecimalPair(s string) bool {
	m := nswNumPairRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	if !strings.Contains(m[1], ".") && !strings.Contains(m[2], ".") {
		return false
	}
	a, e1 := strconv.ParseFloat(m[1], 64)
	b, e2 := strconv.ParseFloat(m[2], 64)
	return e1 == nil && e2 == nil && a < b
}

// nswHasExprBody 尾判: 必须有算子或函数名, 且不是纯常量/无数字。
func nswHasExprBody(s string) bool {
	stripped := strings.NewReplacer("(", "", ")", "", ",", "").Replace(s)
	if nswBareConstRe.MatchString(stripped) {
		return false
	}
	if !strings.ContainsAny(s, "0123456789") {
		return false
	}
	return strings.ContainsAny(s, "+-*/^") ||
		strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// nswCand 候选及其在原文中的位置 (rune 偏移, [start, end))
type nswCand struct {
	expr         string
	start, end   int
	trimmedLeft  string // 被 Trim("+*/^,") 去掉的首部 (Markdown 强调符检测)
	trimmedRight string
}

// nswIsCandRune 候选字符集: 与白名单 (nswIsExprRune) 不同, 此处还要吞下范围符与百分号 ——
// 范围符 (20260913 缺陷Q): en dash / em dash / 全角减号 / 波浪号必须留在候选内,
// 否则 "1–12 / 1–31" 被切成三段, 中间段 "12 / 1" 成了完整算式 (实测算出 12)。
// 留在候选内后由 nswIsCandidate 的字符白名单整段拒绝 (与"宁漏勿误"口径一致)。
func nswIsCandRune(r rune) bool {
	if unicode.IsDigit(r) {
		return true
	}
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
		return true
	}
	switch r {
	case '.', '+', '-', '*', '/', '^', '(', ')', ',', '%', '_', ' ', '\t',
		'–', '—', '−', '－', '～', '〜', '~':
		return true
	}
	return false
}

// nswSniffCands 保留位置与被修剪字符的嗅探器 (语境判据在 nswCtxReject)
func nswSniffCands(text string) []nswCand {
	var cands []nswCand
	var cur []rune
	curStart, pos := 0, 0
	prevSpace := false
	for _, r := range text {
		if r == ' ' || r == '\t' {
			if prevSpace {
				cands = nswAppendCand(cands, cur, curStart)
				cur = nil
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
		if nswIsCandRune(r) {
			if len(cur) == 0 {
				curStart = pos
			}
			cur = append(cur, r)
		} else {
			cands = nswAppendCand(cands, cur, curStart)
			cur = nil
		}
		pos++
	}
	return nswAppendCand(cands, cur, curStart)
}

// nswAppendCand 把累积片段转成候选后追加 (空片段不产生候选)。
func nswAppendCand(cands []nswCand, cur []rune, curStart int) []nswCand {
	if len(cur) == 0 {
		return cands
	}
	if c, ok := nswBuildCand(string(cur), curStart); ok {
		return append(cands, c)
	}
	return cands
}

// nswBuildCand 片段 → 候选 (纯函数, 可单测): 计算 expr 与四个 rune 偏移。
// 抽出的目的不是降圈复杂度, 而是让 rune/byte 混用风险 (本文件最易错处) 首次可被死判据钉住。
func nswBuildCand(raw string, curStart int) (nswCand, bool) {
	rw := []rune(raw)
	s := strings.TrimSpace(raw)
	sr := []rune(s)
	lead := len(rw) - len([]rune(strings.TrimLeft(raw, " \t")))
	core := strings.Trim(s, "+*/^,")
	lead2 := len(sr) - len([]rune(strings.TrimLeft(s, "+*/^,")))
	trail := len(sr) - len([]rune(strings.TrimRight(s, "+*/^,")))
	if core == "" {
		return nswCand{}, false
	}
	return nswCand{
		expr:         core,
		start:        curStart + lead + lead2,
		end:          curStart + lead + len(sr) - trail,
		trimmedLeft:  string(rw[lead : lead+lead2]),
		trimmedRight: string(rw[lead+len(sr)-trail : lead+len(sr)]),
	}, true
}

// nswCtxReject 语境否决 —— 引用位置的算式不求值 (J0~J4)
// nswCtxReject 上下文裁决总入口: 三族闸门按「便宜→贵」顺序过。
//
// 拆分 (T61, 20261018): 原单函数 79 行 / 圈复杂度 30(零裕度) → 按闸门族拆三组。
// 各闸门均为纯判定(只读 rs/c, 无副作用, 互不读写), 故分组只保序不改语义:
// 原顺序 J6→J0→J0b→J0c→J1→J2→J3→J4→J5→J8 逐条保留在各自组内。
func nswCtxReject(text string, c nswCand) bool {
	rs := []rune(text)
	if nswRejectMeta(rs, c) {
		return true
	}
	if nswRejectMarkup(rs, c) {
		return true
	}
	return nswRejectRatio(rs, c)
}

// nswRejectMeta 引用/元语境族: J6 自身反馈行 / J0 反引号 / J0b 代码块 / J0c 行内代码。
// LLM 写断言时不会给算式加反引号, 故引用标记一律不求值。
func nswRejectMeta(rs []rune, c nswCand) bool {
	// J6 元语境行: 含铸剑炉自身反馈前缀的行整行不求值 (斩断自激回路, 先判最便宜)
	if nswEvalMarkLine(rs, c.start) {
		return true
	}
	// J0 反引号: 引用语义最强标记
	n := len(rs)
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
	return false
}

// nswRejectMarkup 排版/标记族: J1 Markdown 强调符 / J2 冒号前缀 / J3 表格行。
func nswRejectMarkup(rs []rune, c nswCand) bool {
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
	return false
}

// nswRejectRatio 比例/引用数值族: J4 斜杠列举 / J5 比例陈述 / J8 斜杠带显式正号。
func nswRejectRatio(rs []rune, c nswCand) bool {
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
	// (闸层的 nswSlashSignedRe 判的是候选串本身; 本判据补的是"前导符号已被切走"的
	//  场景 —— 两者同口径不同层, 不可互替, 故缺陷V 泛化闸层后本条保留不动。)
	// 仅当 expr 是纯分数形式时才拒, 避免误伤 "+1/2+1/3" 这类真算式。
	if strings.Contains(c.trimmedLeft, "+") && nswPureFractionRe.MatchString(c.expr) {
		return true
	}
	return false
}
