package main

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNSWEvaluate_Positive(t *testing.T) {
	cases := []struct {
		text string
		want []string // 期望的值集合
	}{
		{"帮我算一下 2*3+4 是多少", []string{"10"}},
		{"sqrt(16) 是几", []string{"4"}},
		{"(1+2)*3 是多少", []string{"9"}},
		{"10/4 呢", []string{"2.5"}},
		{"((5+3)*2)+1 等于?", []string{"17"}},
		{"round(28.274,2)", []string{"28.27"}},
	}
	for _, c := range cases {
		got := nswEvaluate(c.text)
		vals := make([]string, 0)
		for _, a := range got {
			vals = append(vals, a.val)
		}
		for _, w := range c.want {
			found := false
			for _, v := range vals {
				if v == w {
					found = true
				}
			}
			if !found {
				t.Errorf("[%s] 期望含值 %s, 实际 %v", c.text, w, vals)
			}
		}
	}
}

func TestNSWEvaluate_Negative(t *testing.T) {
	texts := []string{
		"价格12元，数量3件，合计36元",
		"版本2.0和3.5倍，第3节",
		"我的身份证号 110101199001011234 不算哦",
		"答案是 这个 不算",
		"abc def 没有数字",
	}
	for _, x := range texts {
		got := nswEvaluate(x)
		if len(got) != 0 {
			// 允许: 版本2.0和3.5倍 中3.5带运算符? 不, 3.5无运算符. 身份证是纯常数.
			t.Errorf("[%s] 应为0命中, 实际%d: %v", x, len(got), got)
		}
	}
}

func TestNSWEvaluate_BareConst(t *testing.T) {
	// 裸常量/纯数字 必须拒绝
	for _, s := range []string{"2.0", "12", "36", "(16)"} {
		if v, ok := nswEval(s); ok {
			t.Errorf("裸常量 %s 不应通过, 实际 %s", s, v)
		}
	}
}

func TestNSW_Determinism(t *testing.T) {
	// 相同输入 → 相同输出 (低熵稳定)
	s1 := nswEvaluate("帮我算 2*3+4 和 (1+2)*3 和 sqrt(16)")
	s2 := nswEvaluate("帮我算 2*3+4 和 (1+2)*3 和 sqrt(16)")
	if len(s1) != len(s2) {
		t.Fatalf("确定性失败: %d vs %d", len(s1), len(s2))
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			t.Fatalf("确定性失败 idx%d: %v vs %v", i, s1[i], s2[i])
		}
	}
}

func TestNSW_FeedbackLowEntropy(t *testing.T) {
	// 反馈必须是稳定格式: 相同锚点 → 相同token
	expr := "2*3+4"
	v1, ok1 := nswEval(expr)
	v2, ok2 := nswEval(expr)
	if !ok1 || !ok2 || v1 != v2 {
		t.Fatalf("稳定表达失败: %v %v %v", ok1, ok2, v1)
	}
	// 反馈格式: [算: expr = val]
	fb1 := "「{EXPR} = {VAL}」" + strings.ReplaceAll("「{EXPR} = {VAL}」", "{EXPR}", expr) + " " + v1
	_ = fb1
	h1 := md5.Sum([]byte(expr + "=" + v1))
	h2 := md5.Sum([]byte(expr + "=" + v2))
	if hex.EncodeToString(h1[:]) != hex.EncodeToString(h2[:]) {
		t.Fatal("md5 不稳定")
	}
}

func TestNSW_FeedbackText(t *testing.T) {
	// nswFeedbackText 输出形态稳定: 【求值】「EXPR = VAL」
	fb1 := nswFeedbackText("帮我算 2*3+4 和 (1+2)*3")
	fb2 := nswFeedbackText("帮我算 2*3+4 和 (1+2)*3")
	if fb1 != fb2 {
		t.Fatalf("feedback 不稳定: %q vs %q", fb1, fb2)
	}
	if fb1 == "" {
		t.Fatal("应命中锚点, 实际为空")
	}
	t.Logf("feedback=%q", fb1)
	// 无锚点 → 空
	if x := nswFeedbackText("价格12元，版本2.0"); x != "" {
		t.Fatalf("语境数字不应命中: %q", x)
	}
}
