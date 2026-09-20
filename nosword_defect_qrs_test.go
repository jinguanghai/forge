package main

import "testing"

// 缺陷Q (20260913): 范围符 (en dash U+2013 / em dash U+2014 / 波浪) 不在嗅探字符集内,
// 被当分隔符切开 → 区间 "1–12 / 1–31" 的中间段 "12 / 1" 成了完整算式, 算出 12 注入上下文。
func TestDefectQ_RangeDash(t *testing.T) {
	cases := []string{
		"1–12 / 1–31",
		"1–12",
		"月份 1–12, 日期 1–31",
		"2020—2021",
		"1～12 / 1～31",
		"0.5–1.5",
		"3~5 个工作日",
		"1－12",
	}
	for _, s := range cases {
		if got := nswEvaluate(s); len(got) != 0 {
			t.Errorf("范围符 %q: 期望零锚点, 实得 %v", s, got)
		}
	}
}

// 缺陷Q 保留面: 真算式不得被范围符修复误伤
func TestDefectQ_KeepRealExpr(t *testing.T) {
	cases := []struct{ in, want string }{
		{"3+4", "3+4=7"},
		{"12 / 1", "12 / 1=12"},
		{"(10-4)-3", "(10-4)-3=3"},
		{"18.9-12.5", "18.9-12.5=6.4"},
	}
	for _, c := range cases {
		got := nswEvaluate(c.in)
		if len(got) != 1 || got[0].expr+"="+got[0].val != c.want {
			t.Errorf("真算式 %q: 期望 [%s], 实得 %v", c.in, c.want, got)
		}
	}
}

// 缺陷R (20260913): "(1853-1861)" 整串被一对括号包裹, 内部是纯整数区间。
// nswRangeRe 锚定整串, 括号使其失配 → 被当减法算出 -8 注入上下文 (实测复现)。
func TestDefectR_ParenRange(t *testing.T) {
	cases := []string{
		"(1853-1861)",
		"年份 (1853-1861)",
		"(3-5) 个",
	}
	for _, s := range cases {
		if got := nswEvaluate(s); len(got) != 0 {
			t.Errorf("括号区间 %q: 期望零锚点, 实得 %v", s, got)
		}
	}
}

// 缺陷S (20260913): 权重表 "0.45/+0.12" 被当除法算出 3.75。
// J4 斜杠列举要求 >=3 段纯数字, 此形态只有 2 段且带显式正号 → 漏网。
func TestDefectS_SlashPlus(t *testing.T) {
	cases := []string{
		"0.45/+0.12",
		"权重 0.45/+0.12",
		"+0.45/0.12",
	}
	for _, s := range cases {
		if got := nswEvaluate(s); len(got) != 0 {
			t.Errorf("斜杠带号 %q: 期望零锚点, 实得 %v", s, got)
		}
	}
}

// 缺陷S 保留面: 负分数 "-3/4" 是常见算式, 不得因"带号"被误拒
func TestDefectS_KeepFraction(t *testing.T) {
	cases := []string{"10/4", "-3/4", "3/4+1/4"}
	for _, s := range cases {
		if got := nswEvaluate(s); len(got) == 0 {
			t.Errorf("真除法 %q: 期望有锚点, 实得零", s)
		}
	}
}

// 既有防线不得回退: 反引号引用 / 行内代码跨度
func TestDefectQRS_NoRegression(t *testing.T) {
	cases := []string{
		"`(1853-1861)`",
		"`0.45/+0.12`",
		"见 `1–12 / 1–31` 说明",
	}
	for _, s := range cases {
		if got := nswEvaluate(s); len(got) != 0 {
			t.Errorf("引用位置 %q: 期望零锚点, 实得 %v", s, got)
		}
	}
}
