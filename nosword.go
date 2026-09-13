package main

// ── noSword.go — 无剑求值引擎 (确定性死程序, 公理五落地) ──
import (
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type nswAnchor struct {
	expr string
	val  string
}

type nswParser struct {
	s   string
	pos int
}

func (p *nswParser) skip() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

func (p *nswParser) tok() (byte, bool) {
	p.skip()
	if p.pos >= len(p.s) {
		return 0, false
	}
	c := p.s[p.pos]
	if strings.IndexByte("+-*/^(),", c) >= 0 {
		return c, true
	}
	return 0, false
}

func (p *nswParser) number() (float64, bool) {
	p.skip()
	start := p.pos
	for p.pos < len(p.s) && ((p.s[p.pos] >= '0' && p.s[p.pos] <= '9') || p.s[p.pos] == '.') {
		p.pos++
	}
	if start == p.pos {
		return 0, false
	}
	v, err := strconv.ParseFloat(p.s[start:p.pos], 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func (p *nswParser) expr() (float64, bool) {
	v, ok := p.term()
	if !ok {
		return 0, false
	}
	for {
		c, ok := p.tok()
		if !ok {
			break
		}
		if c == '+' {
			p.pos++
			r, ok := p.term()
			if !ok {
				return 0, false
			}
			v += r
		} else if c == '-' {
			p.pos++
			r, ok := p.term()
			if !ok {
				return 0, false
			}
			v -= r
		} else {
			break
		}
	}
	return v, true
}

// term 乘除层。左右操作数都走 unary —— 一元负号优先于乘除:
// "-3*2" = (-3)*2 = -6, 且 "-2^2*3" = -(2^2)*3 = -12。
func (p *nswParser) term() (float64, bool) {
	v, ok := p.unary()
	if !ok {
		return 0, false
	}
	for {
		c, ok := p.tok()
		if !ok {
			break
		}
		if c == '*' {
			p.pos++
			r, ok := p.unary()
			if !ok {
				return 0, false
			}
			v *= r
		} else if c == '/' {
			p.pos++
			r, ok := p.unary()
			if !ok {
				return 0, false
			}
			if r == 0 {
				return 0, false
			}
			v /= r
		} else {
			break
		}
	}
	return v, true
}

// power 幂运算 (右结合)。
//
// 优先级链 (20260911 缺陷I 修复): expr → term → unary → power → factor。
// 一元负号必须**低于**幂: "-2^2" = -(2^2) = -4, 不是 (-2)^2 = 4。
// 原实现是 term → power → unary, 负号被 unary 抢在 power 之前吃掉 → 恒算成 (-2)^2 = 4 (错值)。
// 基数为 factor (括号/数字/函数), 指数走 unary —— 保证 "2^-3" 合法, "2^3^2" = 2^(3^2) 右结合。
func (p *nswParser) power() (float64, bool) {
	base, ok := p.factor()
	if !ok {
		return 0, false
	}
	if c, ok := p.tok(); ok && c == '^' {
		p.pos++
		exp, ok := p.unary()
		if !ok {
			return 0, false
		}
		return math.Pow(base, exp), true
	}
	return base, true
}

func (p *nswParser) unary() (float64, bool) {
	p.skip()
	if p.pos < len(p.s) && p.s[p.pos] == '-' {
		p.pos++
		v, ok := p.unary()
		if !ok {
			return 0, false
		}
		return -v, true
	}
	if p.pos < len(p.s) && p.s[p.pos] == '+' {
		p.pos++
		return p.unary()
	}
	return p.power()
}

func (p *nswParser) factor() (float64, bool) {
	p.skip()
	if p.pos < len(p.s) && (p.s[p.pos] >= 'a' && p.s[p.pos] <= 'z' || p.s[p.pos] >= 'A' && p.s[p.pos] <= 'Z' || p.s[p.pos] == '_') {
		start := p.pos
		for p.pos < len(p.s) && (p.s[p.pos] >= 'a' && p.s[p.pos] <= 'z' || p.s[p.pos] >= 'A' && p.s[p.pos] <= 'Z' || p.s[p.pos] == '_') {
			p.pos++
		}
		fn := p.s[start:p.pos]
		p.skip()
		if p.pos < len(p.s) && p.s[p.pos] == '(' {
			p.pos++
			arg, ok := p.expr()
			if !ok {
				return 0, false
			}
			p.skip()
			if p.pos < len(p.s) && p.s[p.pos] == ')' {
				p.pos++
			}
			switch fn {
			case "sqrt":
				if arg < 0 {
					return 0, false
				}
				return math.Sqrt(arg), true
			case "log":
				if arg <= 0 {
					return 0, false
				}
				return math.Log10(arg), true
			case "round":
				if p.pos < len(p.s) && p.s[p.pos] == ',' {
					p.pos++
					n, ok := p.expr()
					if !ok {
						return 0, false
					}
					p.skip()
					if p.pos < len(p.s) && p.s[p.pos] == ')' {
						p.pos++
					}
					scale := math.Pow(10, float64(int(n)))
					return math.Round(arg*scale) / scale, true
				}
				return math.Round(arg), true
			}
		}
		return 0, false
	}
	if p.pos < len(p.s) && p.s[p.pos] == '(' {
		p.pos++
		v, ok := p.expr()
		if !ok {
			return 0, false
		}
		p.skip()
		if p.pos < len(p.s) && p.s[p.pos] == ')' {
			p.pos++
		}
		return v, true
	}
	return p.number()
}

// nswFloatExactMax float64 整数精确上界 (2^53)。达到该值后相邻可表示整数间隔 >1,
// 任何超出此范围的整数结果都不再可信 (2^53+1 == 2^53)。
const nswFloatExactMax = 1 << 53

func nswEval(expr string) (string, bool) {
	if !nswIsCandidate(expr) {
		return "", false
	}
	p := &nswParser{s: expr}
	v, ok := p.expr()
	if !ok {
		return "", false
	}
	p.skip()
	if p.pos != len(p.s) {
		return "", false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", false
	}
	// 精度闸 (20260910 缺陷D): float64 尾数 53 bit, |v| >= 2^53 后相邻可表示整数间隔 >1,
	// "2^53+1" 会被静默算成 9007199254740992 —— 错值会污染 LLM 上下文, 危害远大于漏算。
	if math.Abs(v) >= nswFloatExactMax {
		return "", false
	}
	return nswFmtNum(v), true
}

// nswFmtNum 数值 -> 展示字符串。
//
// 定点位数取 12 位有效数字 (缺陷G, 20260911): 末位与真值的错率 ≈ 2.22e-16 × 10^(d-1),
// 1211 例精确有理数复算实测 d=12 -> 0.00%, d=13 -> 0.08%, d=14 -> 0.25%, d=15 -> 1.65%,
// d=16 -> 20.8%, d=17 -> 82.8%, 与公式吻合 —— 12 位是"每一位都反映真值"的上限,
// 勿因"精度越高越好"上调; 15 位虽多 3 位精度, 末位错率却恶化 1000 倍。
//
// 整数判定必须用精确比较 (20260910 缺陷F): 原用 math.Abs(v-math.Round(v)) < 1e-9,
// 把判据混进了"接近零"的语义 —— 任何 |v| < 1e-9 的非零值都被舍成 0,
// "1/10000000000" 输出 "0"、负值输出 "-0"。与缺陷D同源: 死程序产出错值,
// 污染 LLM 上下文, 危害远大于漏算。v 本身是整数时 v == Round(v) 精确成立;
// 浮点误差导致的近似整数 (如 sqrt(2)^2 = 2.0000000000000004) 走 'g' 12 分支照样显示 2。
//
// "接近零"另设浮点噪声闸: 运算结果的舍入残差 (如 0.1+0.2-0.3 = 5.55e-17) 不是真值,
// 展示成长串只会污染上下文。float64 机器精度 2.22e-16, 量级 ~1 的算式累积残差可达 ~1e-15,
// 故 |v| < 1e-15 视为噪声归零; 1e-15 以上 (如 1e-10) 是真实量级, 必须原样保留。
func nswFmtNum(v float64) string {
	if v == 0 { // 含 -0.0: 统一输出 "0", 不吐 "-0"
		return "0"
	}
	if v == math.Round(v) {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	if math.Abs(v) < 1e-15 { // 浮点噪声闸 (见上)
		return "0"
	}
	s := strconv.FormatFloat(v, 'g', 12, 64)
	if strings.ContainsAny(s, "eE") {
		// 'g' 对极小/极大值切科学计数法, 转定点更易读; 但 'f',-1 会吐 float64 全位尾数
		// (1/30000 -> 0.000033333333333333335, 末 5 位是浮点残差不是真值), 伪装成高精度
		// 污染 LLM 上下文 —— 与缺陷D/F 同源: 死程序产出错值危害远大于漏算。
		// 按十进制指数反推小数位, 与 'g',12 同口径收敛到 12 位有效数字。
		if dec := nswFracDigits(v); dec >= 0 {
			s = strconv.FormatFloat(v, 'f', dec, 64)
			s = strings.TrimRight(s, "0")
			s = strings.TrimSuffix(s, ".")
		} else {
			// dec < 0 即 |v| >= 1e12 (缺陷G 残留, 20260911): 整数部分已占满 12 位有效数字,
			// 定点承载不下 —— 'f',-1 会吐 14~17 位浮点残差 (1e12+1/3 -> 1000000000000.3334),
			// 实测该区间 100% 超标; 'g',12 去尾零后只剩 1 位 (1e+12), 同样失真。
			// 改用 'e',11: 显式 12 位有效数字, 与定点路径同口径。
			s = strconv.FormatFloat(v, 'e', 11, 64)
		}
	}
	return s
}

// nswFracDigits 定点小数位数: 使定点输出与 'g',12 同为 12 位有效数字。
// 指数由 'e' 格式精确解析 —— math.Log10 在 10 的整数幂附近会差 1
// (log10(1e-5) 可能算出 -4.999999999999999), 用它会让位数错一位。
// 返回 -1 表示整数部分已占满 12 位有效数字 (|v| >= 1e12), 定点承载不下,
// 调用方改用 'e',11 科学计数法。
func nswFracDigits(v float64) int {
	e := strconv.FormatFloat(v, 'e', -1, 64) // 形如 "3.3333333333333335e-05"
	i := strings.IndexByte(e, 'e')
	if i < 0 {
		return 11
	}
	exp, err := strconv.Atoi(e[i+1:])
	if err != nil {
		return 11
	}
	dec := 11 - exp
	if dec < 0 {
		return -1
	}
	if dec > 40 {
		dec = 40
	}
	return dec
}

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
)

// nswHasDanglingDot 检测悬空点号: 点号后不是数字 (如 "0-9." "1+2.")。
// 这类片段来自正则/文本, 不是合法数字字面量 → 拒绝 (宁可漏算, 不可误算)。
// 注意 ".5" 属合法小数 (点号后是数字), 不受影响。
func nswHasDanglingDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i+1 >= len(s) || s[i+1] < '0' || s[i+1] > '9') {
			return true
		}
	}
	return false
}

func nswIsCandidate(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(unicode.IsDigit(r) || r == '.' || r == '+' || r == '-' || r == '*' || r == '/' || r == '^' || r == '(' || r == ')' || r == ',' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == ' ' || r == '\t') {
			return false
		}
	}
	if nswDateRe.MatchString(s) || nswYearMonRe.MatchString(s) ||
		nswRangeRe.MatchString(s) || nswUnaryBare.MatchString(s) ||
		nswHugeIntRe.MatchString(s) || nswRangeDivRe.MatchString(s) ||
		nswLeadZeroRe.MatchString(s) || nswMultiSegRe.MatchString(s) {
		return false
	}
	// 多段纯数字减号串拒绝 (20260913 缺陷P): 电话 "138-1234-5678" / CAS 号 "50-78-2" /
	// 编号 "1-2-3" / 美式日期 "9-13-2026" 全落此形, 此前被当连减算出 -6774 / -30 / -4 / -2030
	// 等错值注入 LLM 上下文 —— 违反本文件"死程序产出错值危害远大于漏算"的原则。
	// 两段对已由 nswRangeRe 全拒, 三段以上此前漏网。段数 >= 3 一律拒绝: 编号/电话/日期
	// 是自然语言中的高频来源, 而真连减 (如 10-4-3) 罕见且漏算代价仅为"不纠正"。
	// 前导负号 (-1-2-3) 与括号形式 ((-1)-2-3) 不匹配本正则, 照旧求值 (它们是算式形态)。
	// 悬空点号拒绝 (20260910 缺陷C): "[0-9.]+$" 经嗅探切分后剩 "0-9.", 会被
	// number() 读成 "9." = 9 → 算出 -9 (正则片段被当算式)。点号后非数字即非法字面量。
	if nswHasDanglingDot(s) {
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

// ── 语境层 (20260912 缺陷N): 算式处于"引用位置"而非"断言位置"时不求值 ──
// 根因: 原嗅探器是无位置信息的字符流切分, 分不清"断言算式"与"提及算式"。
// 7 条真实审计记录累计 18 个 fresh 锚点, 其中 11 个是语义错位的假阳性 —— 全部来自
// "提及"位置 (引用/举例/表格/讨论), 且全部带结构性标记。标记是词法可判的:
// 不需要语义理解, 符合公理五"死程序只判可判定项"。
// 代价: 正文中嵌在句子里的算式漏算 (宁漏勿误)。

// nswCand 候选及其在原文中的位置 (rune 偏移, [start, end))
type nswCand struct {
	expr         string
	start, end   int
	trimmedLeft  string // 被 Trim("+*/^,") 去掉的首部 (Markdown 强调符检测)
	trimmedRight string
}

// nswSniffCands 保留位置与被修剪字符的嗅探器 (语境判据在 nswCtxReject)
func nswSniffCands(text string) []nswCand {
	opSet := func(r rune) bool {
		return unicode.IsDigit(r) || r == '.' || r == '+' || r == '-' || r == '*' || r == '/' || r == '^' || r == '(' || r == ')' || r == ',' || r == '%' ||
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
	// 前置"汉字"而非"量词"是为了不误伤裸算式夹具 (如整串 "10/4" / "1/3" —— 无中文
	// 语境, 不是比例陈述, 照旧求值); 真实文本里的分数必带中文语境。
	pureFrac := nswPureFractionRe.MatchString(c.expr)
	if !nswNumFollows(rs, c.end) &&
		((pureFrac && nswHanBefore(rs, c.start)) || (!pureFrac && nswRatioWordBefore(rs, c.start))) {
		return true
	}
	return false
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

// nswFeedbackText 从 asst 文本嗅探锚点, 若命中返回稳定低熵反馈文本; 无锚点返回 "".
// 反馈格式固定: 每锚点一行 "「EXPR = VAL」". 输入 asst 相同 → 输出逐字节相同 (缓存友好).
func nswFeedbackText(asst string) string {
	return nswFormatAnchors(nswEvaluate(asst))
}

// ── 会话级去重 (20260911 缺陷M: 自激循环) ──
//
// 现象: 只扫本轮 asst 是对的(见 agent.go 接线注释), 但 LLM 在后续轮次里"引用"前面
// 反馈过的算式时(举例/列表格/复述分析), 引用与真实计算在形式上完全同形 —— 嗅探器
// 只有形式没有意图, 无法区分 → 每轮都反馈 → LLM 看到反馈又继续引用 → 自我维持。
// 实测一次会话: 累计反馈 30 次, 不同锚点仅 11 个 (重复 19 次, 63% 冗余), 且不收敛。
//
// 修法: 调用侧维护会话级 (expr,val) 表, 已反馈过的锚点跳过。纯文本比对, 不需要理解
// 语义, 因此不破坏"死程序判定"的确定性。
// 边界: 只跳过"同 expr 且同 val"的锚点。同 expr 不同 val 视为新锚点照常反馈
// (理论上不该出现, 防御性保留: 宁可多反馈一次, 不可漏掉一次真实纠错)。
// 代价: 同一会话内重复提问同一算式不再反馈 —— 取舍是有意的, 切断自激的收益
// (每轮省一次完整 LLM 调用) 远大于极端情况下的漏反馈。
// 本组函数纯: 相同 (anchors, seen) → 相同输出, 且不修改 seen。

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
