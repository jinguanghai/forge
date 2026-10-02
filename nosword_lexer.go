// nosword_lexer.go: 无剑表达式递归下降解析器 (词法+语法, 纯计算无副作用)

package main

import (
	"math"
	"strconv"
	"strings"
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
			// round(x, n) 的第二参数写在 ')' 之前, 故此处放行逗号 (由下方 round 分支消费);
			// 其余情况必须见到 ')' —— 缺右括号一律拒绝, 不得静默吞掉。
			if p.pos >= len(p.s) || p.s[p.pos] != ')' {
				if !(fn == "round" && p.pos < len(p.s) && p.s[p.pos] == ',') {
					return 0, false
				}
			} else {
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
					if p.pos >= len(p.s) || p.s[p.pos] != ')' {
						return 0, false
					}
					p.pos++
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
		if p.pos >= len(p.s) || p.s[p.pos] != ')' {
			return 0, false
		}
		p.pos++
		return v, true
	}
	return p.number()
}
