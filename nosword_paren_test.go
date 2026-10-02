package main

// 括号配平回归 (20260921 修复缺陷 S)。
//
// 背景: factor() 遇未闭合 '(' 曾静默跳过 (不消费也不报错), 使 nswEvalMode 末尾的
// "p.pos != len(p.s)" 兜底形同虚设 —— "(1853-1861" 被算成 -8, "(138-1234-5678"
// 被算成 -6774, "(1-2-3" 被算成 -4, 全部注入 LLM 上下文。
//
// 尤其危险的是它绕过了已专门修过的闸: "(1853-1861)" (已闭合) 被拒, 而 "(1853-1861"
// (少一个右括号) 却算出值 —— 防护被一个缺失的 ')' 整体绕过。
//
// 判据: 括号不平衡的算式一律拒绝 (两个口径); 括号平衡的合法算式一律可算。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestParenUnbalancedRejected(t *testing.T) {
	cases := []string{
		"(1853-1861", "(138-1234-5678", "(1-2-3", "((1+2)", "1)",
		"sqrt(4", "round(1.55,1", "2*(3+4", "(2+3))*(4-1)", ")", "(",
		"1+(2*3", "sqrt((4)", "(1+2))",
	}
	for _, e := range cases {
		if v, ok := nswEvalExplicit(e); ok {
			t.Errorf("显式口径 %q 括号不平衡却算出 %q", e, v)
		}
		if v, ok := nswEval(e); ok {
			t.Errorf("嗅探口径 %q 括号不平衡却算出 %q", e, v)
		}
	}
}

func TestParenBalancedAccepted(t *testing.T) {
	cases := []struct{ e, want string }{
		{"(1+2)", "3"}, {"((1+2))", "3"}, {"((((1+2))))", "3"},
		{"sqrt(4)", "2"},
		{"round(1.5)", "2"}, {"round(1.55,1)", "1.6"},
		{"2*(3+4)", "14"}, {"(2+3)*(4-1)", "15"}, {"((1+2)*3)", "9"},
		{"(1+2)*(3+4)", "21"}, {"sqrt(16)+round(2.5)", "7"},
	}
	for _, c := range cases {
		v, ok := nswEvalExplicit(c.e)
		if !ok || v != c.want {
			t.Errorf("显式口径 %q: got=(%q,%v) want=%q", c.e, v, ok, c.want)
		}
	}
}

// exprRandomBalanced 生成括号平衡的算式 (顶层必含算符或函数, 不会退化成裸常量)。
func exprRandomBalanced(rnd *rand.Rand, depth int) string {
	if depth >= 3 {
		return fmt.Sprintf("%d", rnd.Intn(99)+1)
	}
	switch rnd.Intn(5) {
	case 0:
		return fmt.Sprintf("(%s+%s)", exprRandomBalanced(rnd, depth+1), exprRandomBalanced(rnd, depth+1))
	case 1:
		return fmt.Sprintf("(%s-%s)", exprRandomBalanced(rnd, depth+1), exprRandomBalanced(rnd, depth+1))
	case 2:
		return fmt.Sprintf("(%s*%s)", exprRandomBalanced(rnd, depth+1), exprRandomBalanced(rnd, depth+1))
	case 3:
		return fmt.Sprintf("(%s/%d)", exprRandomBalanced(rnd, depth+1), rnd.Intn(9)+1)
	default:
		return fmt.Sprintf("sqrt(%s)", exprRandomBalanced(rnd, depth+1))
	}
}

// TestRandomBalancedExprSnapshot 行为指纹: 3000 例括号平衡算式的 (输入, 结果, 是否可算)
// 序列化后取 SHA256。修复前后实测该指纹逐字节相同 —— 证明配平闸对合法算式零误伤、
// 零行为变化 (差分验证, 非"看起来没问题")。任何未来的行为漂移都会立刻暴露。
func TestRandomBalancedExprSnapshot(t *testing.T) {
	rnd := rand.New(rand.NewSource(20260921))
	var sb strings.Builder
	rej := 0
	for i := 0; i < 3000; i++ {
		e := exprRandomBalanced(rnd, 0)
		if !nswParenBalanced(e) {
			t.Fatalf("生成器产出不平衡算式 %q —— 测试数据自身有缺陷", e)
		}
		v, ok := nswEvalExplicit(e)
		if !ok {
			rej++
		}
		fmt.Fprintf(&sb, "%s\x00%s\x00%v\n", e, v, ok)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	got := hex.EncodeToString(sum[:])
	const want = "66b93a19034d2ac866a9fa59d77f7e9b16e339fd920c76e165abb6ad13639fe6"
	if got != want {
		t.Errorf("3000 例随机平衡算式行为指纹漂移:\n  got =%s\n  want=%s\n  (被拒 %d 例)", got, want, rej)
	}
	t.Logf("随机平衡算式 3000 例: 可算 %d 被拒 %d, 指纹 %s", 3000-rej, rej, got)
}
