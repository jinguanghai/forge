package main

// math_inline.go — math gate 纯整数域内联求值 (P0 性能优化, 20261001)
//
// 问题 (实测, 见 bench/perf_20261001.md):
//   math gate 走 spawn math_gate.exe → 写临时 .py → python + `from sympy import *`。
//   纯整数四则 "2+2" 也要付 464ms, 其中 import sympy 441ms、python 冷启 22ms。
//   生产 2026-09-02~10-01 共 903 次 math 调用, P50 486ms, 累计净执行 209s。
//
// 策略: 对「纯整数四则 / 整数幂 / 整数取模」表达式, 在主程序内用 math/big 在
// 有理数域精确求值 (无浮点误差), 产出与 math_gate 逐字节同形的 JSON, 跳过 spawn。
// 内联不改变任何「判定」语义, 只改变「谁来算」; 凡有一丝不确定一律回退 spawn,
// sympy 始终是唯一语义权威。
//
// 语义锚点 (全部由实测 sympy 输出固化, 见 math_inline_test.go 的 A/B 表):
//   10/4 -> 5/2        (精确有理数, 不是 2.5)     (2/3)^3 -> 8/27
//   2^3^2 -> 512       (幂右结合)                 -2^2 -> -4 (一元负号弱于 ^)
//   10%3 -> 1;  -7%3 -> 2;  7%-3 -> -2            (Python/sympy 取模语义)
//   0^0 -> 1;   2^-1 -> 1/2;  3/1 -> 3;  10/5 -> 2
//   -1/3 -> ("-1/3", "- \\frac{1}{3}")            分数 latex 负号在 \frac 之外
//
// 回退 (ok=false) 的全部情形 —— 宁可慢, 不可错:
//   · 含小数点 / 字母 / 其他字符 (浮点域与符号域精度语义复杂, 交 sympy)
//   · 隐式乘法 "2(3)" / 语法不完整 / 尾随垃圾 (解析器只认显式算符)
//   · 除零 / 模零 (sympy 返回 zoo 或 error, 内联无权替它下结论)
//   · 指数非整数 "4^(1/2)" 或 |指数| 超界 (防 DoS)
//   · 表达式长度 / 数字字面量位数超界

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// mathInlineMaxLen 内联只处理短算式; 超长交 sympy (解析器无递归深度保护之外的收益)
	mathInlineMaxLen = 400
	// mathInlineMaxDigits 单个数字字面量位数上限
	mathInlineMaxDigits = 1000
	// mathInlineMaxExp 幂指数绝对值上限 (2^10000 ≈ 3011 位, 毫秒级; 再大交 sympy)
	mathInlineMaxExp = 10000
)

// mathInlineEnabled 内联开关 (FORGE_MATH_INLINE=0 关闭)。默认开启。
// 关闭后 math 全量回退 spawn —— 语义完全不变, 只失去加速。
func mathInlineEnabled() bool {
	v := strings.TrimSpace(os.Getenv("FORGE_MATH_INLINE"))
	return v != "0" && !strings.EqualFold(v, "false") && !strings.EqualFold(v, "off")
}

// mathInlineEligible 字符集闸: 数字 / 四则 / 幂 / 取模 / 括号 / 空格。
// 不含 '.'(浮点域) 与字母(符号域与函数) —— 那两类语义由 sympy 独占。
func mathInlineEligible(expr string) bool {
	if expr == "" || len(expr) > mathInlineMaxLen {
		return false
	}
	for i := 0; i < len(expr); i++ {
		switch c := expr[i]; {
		case c >= '0' && c <= '9':
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '%':
		case c == '^' || c == '(' || c == ')' || c == ' ':
		default:
			return false
		}
	}
	return true
}

type mathInlineParser struct {
	src []byte
	pos int
}

func (p *mathInlineParser) skipSpaces() {
	for p.pos < len(p.src) && p.src[p.pos] == ' ' {
		p.pos++
	}
}

// peek 返回下一个非空格字符 (0 表示结束), 并把 pos 停在它上面。
func (p *mathInlineParser) peek() byte {
	p.skipSpaces()
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *mathInlineParser) eat(c byte) bool {
	if p.peek() == c {
		p.pos++
		return true
	}
	return false
}

// parseExpr := parseTerm (('+'|'-') parseTerm)*
func (p *mathInlineParser) parseExpr() (*big.Rat, bool) {
	left, ok := p.parseTerm()
	if !ok {
		return nil, false
	}
	for {
		switch p.peek() {
		case '+':
			p.pos++
			r, ok := p.parseTerm()
			if !ok {
				return nil, false
			}
			left = new(big.Rat).Add(left, r)
		case '-':
			p.pos++
			r, ok := p.parseTerm()
			if !ok {
				return nil, false
			}
			left = new(big.Rat).Sub(left, r)
		default:
			return left, true
		}
	}
}

// parseTerm := parseUnary (('*'|'/'|'%') parseUnary)*   ('**' 由 parsePower 消费)
func (p *mathInlineParser) parseTerm() (*big.Rat, bool) {
	left, ok := p.parseUnary()
	if !ok {
		return nil, false
	}
	for {
		switch p.peek() {
		case '*':
			if p.pos+1 < len(p.src) && p.src[p.pos+1] == '*' {
				return nil, false // '**' 未被 parsePower 消费 = 畸形输入, 回退
			}
			p.pos++
			r, ok := p.parseUnary()
			if !ok {
				return nil, false
			}
			left = new(big.Rat).Mul(left, r)
		case '/':
			p.pos++
			r, ok := p.parseUnary()
			if !ok {
				return nil, false
			}
			if r.Sign() == 0 {
				return nil, false // 除零: sympy 给 zoo, 内联不下结论 → 回退
			}
			left = new(big.Rat).Quo(left, r)
		case '%':
			p.pos++
			r, ok := p.parseUnary()
			if !ok {
				return nil, false
			}
			v, ok := mathInlineMod(left, r)
			if !ok {
				return nil, false
			}
			left = v
		default:
			return left, true
		}
	}
}

// parseUnary := ('+'|'-')* parsePower
// 一元负号弱于幂: -2^2 = -(2^2) = -4 (实测 sympy 锚点)。
func (p *mathInlineParser) parseUnary() (*big.Rat, bool) {
	switch p.peek() {
	case '+':
		p.pos++
		return p.parseUnary()
	case '-':
		p.pos++
		v, ok := p.parseUnary()
		if !ok {
			return nil, false
		}
		return new(big.Rat).Neg(v), true
	}
	return p.parsePower()
}

// parsePower := parseAtom ('^'|'**') parseUnary   (右结合: 2^3^2 = 512)
func (p *mathInlineParser) parsePower() (*big.Rat, bool) {
	base, ok := p.parseAtom()
	if !ok {
		return nil, false
	}
	switch p.peek() {
	case '^':
		p.pos++
		exp, ok := p.parseUnary()
		if !ok {
			return nil, false
		}
		return mathInlinePow(base, exp)
	case '*':
		if p.pos+1 < len(p.src) && p.src[p.pos+1] == '*' {
			p.pos += 2
			exp, ok := p.parseUnary()
			if !ok {
				return nil, false
			}
			return mathInlinePow(base, exp)
		}
	}
	return base, true
}

// parseAtom := '(' parseExpr ')' | 数字串
func (p *mathInlineParser) parseAtom() (*big.Rat, bool) {
	if p.eat('(') {
		v, ok := p.parseExpr()
		if !ok {
			return nil, false
		}
		if !p.eat(')') {
			return nil, false
		}
		return v, true
	}
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}
	if p.pos == start {
		return nil, false
	}
	digits := string(p.src[start:p.pos])
	if len(digits) > mathInlineMaxDigits {
		return nil, false
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, false
	}
	return new(big.Rat).SetInt(n), true
}

// mathInlinePow 精确幂。指数必须是整数且在界内; 0^0 = 1 (实测 sympy 锚点);
// 0 的负指数 = zoo → 回退。
func mathInlinePow(base, exp *big.Rat) (*big.Rat, bool) {
	if !exp.IsInt() {
		return nil, false
	}
	e := exp.Num()
	if !e.IsInt64() {
		return nil, false
	}
	n := e.Int64()
	if n > mathInlineMaxExp || n < -mathInlineMaxExp {
		return nil, false
	}
	if n == 0 {
		return new(big.Rat).SetInt64(1), true
	}
	neg := n < 0
	if neg {
		n = -n
	}
	num := new(big.Int).Exp(base.Num(), big.NewInt(n), nil)
	den := new(big.Int).Exp(base.Denom(), big.NewInt(n), nil)
	if neg {
		if num.Sign() == 0 {
			return nil, false
		}
		num, den = den, num
	}
	return new(big.Rat).SetFrac(num, den), true
}

// mathInlineMod 复刻 Python/sympy 的 % 语义 (实测锚点: -7%3=2, 7%-3=-2):
// 结果符号跟随除数, 即 r = a - b*floor(a/b)。
// Go 的 big.Int.Mod 是 Euclidean (0 <= m < |b|), 除数 <0 时需再平移一次。
func mathInlineMod(a, b *big.Rat) (*big.Rat, bool) {
	if !a.IsInt() || !b.IsInt() {
		return nil, false
	}
	bi := b.Num()
	if bi.Sign() == 0 {
		return nil, false // 模零: sympy 报 "integer modulo by zero" → 回退
	}
	m := new(big.Int).Mod(a.Num(), bi)
	if m.Sign() != 0 && bi.Sign() < 0 {
		m.Add(m, bi)
	}
	return new(big.Rat).SetInt(m), true
}

// mathInlineFormat 产出与 sympy str()/latex() 逐字节一致的 (result, latex)。
// 实测锚点: 4 -> ("4","4");  5/2 -> ("5/2", `\frac{5}{2}`);
//
//	-5/2 -> ("-5/2", `- \frac{5}{2}`) —— 负号在 \frac 之外, 连字符后有空格。
func mathInlineFormat(r *big.Rat) (string, string) {
	if r.IsInt() {
		s := r.Num().String()
		return s, s
	}
	num := r.Num() // big.Rat 恒已规范化: 分母为正, 符号在分子
	den := r.Denom()
	abs := new(big.Int).Abs(num)
	res := abs.String() + "/" + den.String()
	latex := `\frac{` + abs.String() + `}{` + den.String() + `}`
	if num.Sign() < 0 {
		res = "-" + res
		latex = "- " + latex
	}
	return res, latex
}

// mathInlineEval 尝试内联求值。ok=false 表示「不在内联子集」, 调用方必须回退 spawn。
func mathInlineEval(expr string) (result, latex string, ok bool) {
	expr = strings.TrimSpace(expr) // math_gate 侧同样 TrimSpace(req.Expr)
	if !mathInlineEligible(expr) {
		return "", "", false
	}
	p := &mathInlineParser{src: []byte(expr)}
	v, ok := p.parseExpr()
	if !ok {
		return "", "", false
	}
	p.skipSpaces()
	if p.pos != len(p.src) {
		return "", "", false // 尾随垃圾 (如隐式乘法 "2 3") → 回退
	}
	res, lx := mathInlineFormat(v)
	return res, lx, true
}

// mathInlineBinaryPresent 死边界二进制是否就位 (与 selfHostedGate 的定位逻辑同源)。
//
// 内联只在死边界可用时启用: 内联是「spawn 的等价替代」, 不是「绕过死边界」。
// 若二进制缺失仍走内联, 「gate 二进制缺失」这一环境故障会被静默掩盖 ——
// cov_gates_deadboundary_test.go 的哨兵 (死边界缺失时不得判成功) 正是防这个。
// 代价是一次 os.Stat (~µs 级, 相对被省掉的 464ms 可忽略)。
func (f *Forge) mathInlineBinaryPresent() bool {
	name := "math_gate.exe"
	if runtime.GOOS != "windows" {
		name = "math_gate"
	}
	_, err := os.Stat(filepath.Join(f.toolsDir, name))
	return err == nil
}

// mathInlineGate math 分支的内联快速路径。
// 返回 ok=false 表示「交给 math_gate」—— 调用方原路走 spawn, 语义零变化。
func (f *Forge) mathInlineGate(code string, start time.Time) (ForgeGateResult, bool) {
	if !mathInlineEnabled() {
		return ForgeGateResult{}, false
	}
	if !f.mathInlineBinaryPresent() {
		return ForgeGateResult{}, false
	}
	expr, action := code, "simplify"
	if validGateJSON(code) {
		var req struct {
			Expr   string `json:"expr"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(code), &req); err != nil {
			return ForgeGateResult{}, false
		}
		expr = req.Expr
		if req.Action != "" {
			action = req.Action
		}
	}
	if action != "simplify" {
		return ForgeGateResult{}, false // solve/integrate/diff/factor/equals/evaluate 交 sympy
	}
	result, latex, ok := mathInlineEval(expr)
	if !ok {
		return ForgeGateResult{}, false
	}
	// 与 math_gate 的 emitJSON 同形 (字段序 + SetEscapeHTML(false) + 尾随换行),
	// 下游 (缓存/展示/审计) 无需区分两条路径。
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(struct {
		OK        bool   `json:"ok"`
		Result    string `json:"result"`
		Latex     string `json:"latex"`
		Validated bool   `json:"validated"`
	}{true, result, latex, true}); err != nil {
		return ForgeGateResult{}, false
	}
	return ForgeGateResult{
		OK: true, Lang: "math", Stage: "done",
		Stdout:   buf.String(),
		Duration: time.Since(start).Milliseconds(),
	}, true
}
