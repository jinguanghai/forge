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
	nswDateRe    = regexp.MustCompile(`^\d{4}\s*[-/]\s*\d{1,2}\s*[-/]\s*\d{1,2}$`)             // 2026-09-10 / 2026/09/10
	nswYearMonRe = regexp.MustCompile(`^\d{4}\s*[-/]\s*\d{1,2}$`)                              // 2026-09 / 2026/09
	nswRangeRe   = regexp.MustCompile(`^\d{1,4}\s*-\s*\d{1,4}$`)                               // 3-5 / 12-15 整数区间: 全拒 (中文文本高频, 不可误算)
	nswNumPairRe = regexp.MustCompile(`^\s*(\d{1,6}(?:\.\d+)?)\s*-\s*(\d{1,6}(?:\.\d+)?)\s*$`) // 裸数对减号, 见 nswIsCandidate
	nswUnaryBare = regexp.MustCompile(`^[+-]\s*[0-9.]+\s*$`)                                   // -3 裸带号常量
	nswHugeIntRe = regexp.MustCompile(`\d{16,}`)                                               // >2^53 float64 精度失效

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
		nswLeadZeroRe.MatchString(s) {
		return false
	}
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

func nswSniff(text string) []string {
	opSet := func(r rune) bool {
		return unicode.IsDigit(r) || r == '.' || r == '+' || r == '-' || r == '*' || r == '/' || r == '^' || r == '(' || r == ')' || r == ',' || r == '%' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' ||
			r == ' ' || r == '\t'
	}
	var cands []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			s := strings.TrimSpace(string(cur))
			s = strings.Trim(s, "+*/^,")
			if s != "" {
				cands = append(cands, s)
			}
			cur = nil
		}
	}
	// 连续 >=2 个空白视为列分隔 (20260911 缺陷L): 表格/清单里 "-(2^2)     -4" 两列
	// 会被粘连成一个算式算出 -8 —— 死程序产出错值污染 LLM 上下文, 危害远大于漏算。
	// 单个空格仍是算式内空白 ("2 + 3" 必须可算), 双空格断开。
	// 代价: "2  +  3" 漏算 (宁漏勿误)。
	prevSpace := false
	for _, r := range text {
		if r == ' ' || r == '\t' {
			if prevSpace {
				flush()
				continue
			}
			prevSpace = true
			cur = append(cur, r)
			continue
		}
		prevSpace = false
		if opSet(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return cands
}

func nswEvaluate(text string) []nswAnchor {
	seen := map[string]bool{}
	var out []nswAnchor
	for _, cand := range nswSniff(text) {
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
// 判据: 原文中 expr 之后紧跟的数值 == 死程序算出的 val (即 redundant)。
// 此时反馈是纯复述, 零信息增量 -> 跳过。
// 保留 corrected(原文写错) 与 completed(原文未给结论) -> 反馈的价值恰在这两类。
//
// 为什么不用"已反馈过的锚点"去重表: 那会把 LLM **重复同一个错误**也一并跳过
// (死程序测试抓出的缺陷: E2E 用例里 LLM 连续两轮输出 "2+2 = 5", 第二轮不再纠正)。
// 本方案无状态、纯函数, 且语义精确 —— 跳过的充要条件是"反馈没有新信息"。
func nswFilterEchoed(asst string, anchors []nswAnchor) []nswAnchor {
	var out []nswAnchor
	for _, a := range anchors {
		if nswClassifyAnchor(asst, a) == "redundant" {
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

// nswClassifyAnchor 埋点用分类 —— 判断锚点在原文里是否已给出结论。
//
//	redundant = 原文已含 "expr ... val" 同形结论 (零信息增量 = 空转)
//	corrected = 原文含 expr 但紧跟的数值与 val 不同 (真纠错, 无剑的价值所在)
//	completed = 原文只写算式未写结果 (补全)
//
// 仅用于度量, 不参与任何行为判定: 判错只影响统计口径, 不影响正确性。
// 口径局限: 原文写四舍五入短形式 (0.1268 vs 精确值 0.12682357518) 会被计入
// corrected。这是有意选择 —— 宁可高估纠错、不可低估(低估会掩盖真实纠错)。
func nswClassifyAnchor(asst string, a nswAnchor) string {
	idx := 0
	sawNumber := false
	for {
		j := strings.Index(asst[idx:], a.expr)
		if j < 0 {
			break
		}
		idx = idx + j + len(a.expr)
		// TrimLeft 按 rune 集合处理: 多字节连接符('：' '→')不能用 byte 索引比较
		rest := strings.TrimLeft(asst[idx:], " \t=:：→")
		num := nswLeadingNum(rest)
		if num == "" {
			continue
		}
		sawNumber = true
		if nswNumEqual(num, a.val) {
			return "redundant"
		}
		return "corrected"
	}
	if sawNumber {
		return "corrected"
	}
	return "completed"
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

// nswNumEqual 数值字符串等值比较 (容忍 "0.3" / ".3" / "0.30" 的书写差异)
func nswNumEqual(a, b string) bool {
	x, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
	y, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
	if err1 != nil || err2 != nil {
		return false
	}
	return x == y
}
