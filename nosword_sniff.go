// nosword_sniff.go: 算式候选嗅探 — 正则与位置上下文判定 (不含最终裁决)

package main

import (
	"regexp"
	"strings"
	"unicode"
)

var nswBareConstRe = regexp.MustCompile(`^\s*[0-9.(),]+\s*$`)

// 歧义拒绝规则: 宁可漏算, 不可误算 (误算会污染 LLM 上下文, 危害远大于漏算)
var (
	nswDateRe     = regexp.MustCompile(`^\d{4}\s*[-/]\s*\d{1,2}\s*[-/]\s*\d{1,2}$`)             // 2026-09-10 / 2026/09/10
	nswYearMonRe  = regexp.MustCompile(`^\d{4}\s*[-/]\s*\d{1,2}$`)                              // 2026-09 / 2026/09
	nswRangeRe    = regexp.MustCompile(`^\d{1,4}\s*-\s*\d{1,4}$`)                               // 3-5 / 12-15 整数区间: 全拒 (中文文本高频, 不可误算)
	nswNumPairRe  = regexp.MustCompile(`^\s*(\d{1,6}(?:\.\d+)?)\s*-\s*(\d{1,6}(?:\.\d+)?)\s*$`) // 裸数对减号, 见 nswIsCandidate
	nswMultiSegRe = regexp.MustCompile(`^\d+(?:\.\d+)?(?:\s*-\s*\d+(?:\.\d+)?){2,}$`)           // 多段纯数字减号串, 见 nswIsCandidate (缺陷P)
	nswUnaryBare  = regexp.MustCompile(`^[+-]\s*[0-9.]+\s*$`)                                   // -3 裸带号常量
	nswHugeIntRe  = regexp.MustCompile(`\d{16,}`)                                               // >2^53 float64 精度失效

	// 时段组拒绝 (20260911 缺陷H): "9-12 / 14-18" 是营业/门诊时段, 不是算式。
	// 嗅探按非算式字符切分, 全串都是算式字符 → 合成一个候选 → 被算成 9-(12/14)-18 = -69/7。
	// 单区间 "9-12" 已由 nswRangeRe 拒绝, 但 "区间/区间" 绕过了它。
	nswRangeDivRe = regexp.MustCompile(`^\d{1,4}\s*-\s*\d{1,4}\s*/\s*\d{1,4}\s*-\s*\d{1,4}$`)

	// 前导零拒绝 (20260911 缺陷H): "00/14" 是 "12:00 / 14:00" 的残片, 算出 0 属误算。
	// 判据: '0' 紧跟数字, 且该 '0' 前不是 '.' —— 排除 1.05 / 100.05 这类内嵌零;
	// "100+2" 的 "00" 前是数字, [^\d.] 不匹配 → 不误伤 (实测 100+2 / 1000/2 均放行)。
	nswLeadZeroRe = regexp.MustCompile(`(^|[^\d.])0\d`)

	// 括号包裹的整数区间 (20260913 缺陷R): "(1853-1861)" 整串被一对半角括号包裹,
	// 内部是纯整数区间。nswRangeRe 锚定整串, 括号使其失配 → 被当减法算出 -8。
	nswParenRangeRe = regexp.MustCompile(`^\([ \t]*\d{1,6}[ \t]*-[ \t]*\d{1,6}[ \t]*\)$`)

	// 斜杠任一侧带显式正号的数值对 (20260913 缺陷S): "0.45/+0.12" 权重表。
	nswSlashPlusRe = regexp.MustCompile(`^[0-9.]+[ \t]*/[ \t]*\+[0-9.]+$|^\+[0-9.]+[ \t]*/[ \t]*[0-9.]+$`)
)

// nswInCodeFence 候选之前是否存在未闭合的 ``` 代码块
func nswInCodeFence(rs []rune, idx int) bool {
	n := 0
	for i := 0; i+2 < idx; i++ {
		if rs[i] == '`' && rs[i+1] == '`' && rs[i+2] == '`' {
			n++
			i += 2
		}
	}
	return n%2 == 1
}

// nswEvalMarkLine 候选所在行是否含铸剑炉自身的反馈前缀 【求值】 (J6, 20260912 自激回路)
// 理由: 【求值】 只由 nswFormatAnchors 生成, 断言不会自带此前缀 -> 该行必是引用/复述。
// 为什么整行判而非`标记紧邻候选`: 反馈形如 【求值】「EXPR = VAL」, 标记与候选
// 之间隔着 「 等符号; 更关键的是 LLM 复述时会把标记和别的算式写在同一行
// (如 "... -> 【求值】「3/4 = 0.75」" 与前面的错值例同行), 只判`候选之前`会漏。
//
// 斩断的闭环 (实测审计): 报告引用反馈 -> 嗅探器把`引用的错值 + 邻近的正确值`混判为
// corrected (corrected 属 fresh, 不被会话级去重 nswFilterEchoed 过滤) -> 再注入 ->
// 报告再引用 → 自我维持。15:34:28 与 15:46:51 两条 corrected=1 exprs=["3/4=0.75"]
// 即由此产生, 间隔 12 分钟。
// 误伤边界: 正常断言文本不含 【求值】, 故对真实算式零影响 (见回归用例)。
func nswEvalMarkLine(rs []rune, idx int) bool {
	s, e := idx, idx
	for s > 0 && rs[s-1] != '\n' {
		s--
	}
	for e < len(rs) && rs[e] != '\n' {
		e++
	}
	return strings.Contains(string(rs[s:e]), "【求值】")
}

// nswInInlineCode 候选是否处于 Markdown 行内代码跨度内 (J0c, 20260912 缺陷④-d)
// 行内反引号成对出现, 按行统计候选之前的反引号个数: 奇数 => 位于跨度内。
// 为什么必须补这条: J0 判据是“紧邻反引号”, 但 LLM 写引用时反引号包的是**短语**
// (如 `占比 3/4 = 0.8`), 候选前紧邻是 '比'、后紧邻是 '=', J0 完全失效 →
// 讨论无剑自身的内容被当作断言求值, 且把 LLM 从用户任务上拽走 (自激回路)。
// 按行隔离的原因: 行内代码不跨行, 否则上方 ``` 块的反引号会污染本行计数。
// 代价: 行内出现孤立反引号时该行后续算式漏算 —— 与既有“宁漏勿误”取舍一致 (缺陷N 已确立)。
func nswInInlineCode(rs []rune, idx int) bool {
	s := idx
	for s > 0 && rs[s-1] != '\n' {
		s--
	}
	n := 0
	for i := s; i < idx; i++ {
		if rs[i] == '`' {
			n++
		}
	}
	return n%2 == 1
}

// nswTableRow 候选所在行是否含 >=2 个 '|' (表格单元格 = 列举, 非待算)
func nswTableRow(rs []rune, idx int) bool {
	s := idx
	for s > 0 && rs[s-1] != '\n' {
		s--
	}
	e := idx
	for e < len(rs) && rs[e] != '\n' {
		e++
	}
	cnt := 0
	for i := s; i < e; i++ {
		if rs[i] == '|' {
			cnt++
		}
	}
	return cnt >= 2
}

// nswRatioWords 比例量词 —— 前置紧邻即视为"引用比例"语境 (J5)
var nswRatioWords = map[rune]bool{
	'率': true, '比': true, '成': true, '分': true, '数': true,
	'份': true, '占': true, '权': true, '额': true, '量': true, '项': true,
}

// nswRatioWordBefore 候选之前 (跳空格) 是否紧邻比例量词
func nswRatioWordBefore(rs []rune, start int) bool {
	i := start - 1
	for i >= 0 && (rs[i] == ' ' || rs[i] == '\t') {
		i--
	}
	return i >= 0 && nswRatioWords[rs[i]]
}

// nswHanBefore 候选之前 (跳空格) 是否紧邻汉字 —— 中文语境下的纯分数即比例陈述 (J5)
func nswHanBefore(rs []rune, start int) bool {
	i := start - 1
	for i >= 0 && (rs[i] == ' ' || rs[i] == '\t') {
		i--
	}
	return i >= 0 && unicode.Is(unicode.Han, rs[i])
}

// nswNumFollows 候选之后 (可跳空格与一个连接符 = : ： →) 是否紧跟数值
func nswNumFollows(rs []rune, end int) bool {
	j := end
	for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
		j++
	}
	if j < len(rs) && (rs[j] == '=' || rs[j] == ':' || rs[j] == '：' || rs[j] == '→') {
		j++
		for j < len(rs) && (rs[j] == ' ' || rs[j] == '\t') {
			j++
		}
	}
	if j >= len(rs) {
		return false
	}
	c := rs[j]
	return (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '+'
}

// nswPureFractionRe 纯分数形式 (数字/数字, 允许两侧空格) —— J5 形态判据
var nswPureFractionRe = regexp.MustCompile(`^[0-9.]+[ \t]*/[ \t]*[0-9.]+$`)

// nswSlashListRe 纯斜杠列举 (>=3 段纯数字)
var nswSlashListRe = regexp.MustCompile(`^[0-9.]+([ \t]*/[ \t]*[0-9.]+){2,}$`)
