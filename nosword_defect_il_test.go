package main

import "testing"

// TestNSWDefectI_UnaryVsPower 缺陷I回归 (20260911): 一元负号优先级必须低于幂。
//
// 优先级链: expr -> term -> unary -> power -> factor
// 原实现为 expr -> term -> power -> unary -> factor, 负号被 unary 抢在 power 之前吃掉,
// "-2^2" 恒算成 (-2)^2 = 4 (真值 -4) —— 符号级错值, 危害远大于漏算。
// 3000 例对拍: 修复前错值 202 例 -> 修复后 0 例。
func TestNSWDefectI_UnaryVsPower(t *testing.T) {
	cases := []struct{ in, want string }{
		{"-2^2", "-4"},  // -(2^2), 非 (-2)^2
		{"(-2)^2", "4"}, // 括号强制
		{"-(2^2)", "-4"},
		{"-2^2+10", "6"},
		{"1-2^2", "-3"},
		{"1-2^3", "-7"},
		{"2^-3", "0.125"}, // 指数允许一元负号
		{"4^-1", "0.25"},
		{"-2^-2", "-0.25"}, // -(2^-2)
		{"2^3^2", "512"},   // 右结合 2^(3^2)
		{"2*3^2", "18"},
		{"-3*2", "-6"},
		{"-2^2*3", "-12"}, // -(2^2)*3
		{"2 - -3", "5"},
		{"2^-2*4", "1"},
		{"sqrt(9)^2", "9"},
		{"-sqrt(9)", "-3"},
	}
	for _, c := range cases {
		got, ok := nswEval(c.in)
		if !ok || got != c.want {
			t.Errorf("nswEval(%q) = %q,%v; want %q", c.in, got, ok, c.want)
		}
	}
}

// TestNSWDefectL_ColumnGap 缺陷L回归 (20260911): 连续 >=2 空白 = 列分隔, 跨列不得粘连。
//
// 表格/清单里 "-(2^2)     -4" 两列曾被粘成一个算式算出 -8, 死程序产出错值灌回 LLM 上下文。
// 单个空格仍是算式内空白 ("2 + 3" 必须可算), 双空格断开。
// 代价: "2  +  3" 漏算 —— 宁漏勿误。
func TestNSWDefectL_ColumnGap(t *testing.T) {
	cases := []struct{ in, want string }{
		{"-(2^2)     -4", "【求值】「-(2^2) = -4」"},
		{"| -(2^2)     -4     →    -4 |", "【求值】「-(2^2) = -4」"},
		{"2 + 3", "【求值】「2 + 3 = 5」"},
		{"3 * 7 / 1 + 1", "【求值】「3 * 7 / 1 + 1 = 22」"},
		{"3.14 * 2^2", "【求值】「3.14 * 2^2 = 12.56」"},
	}
	for _, c := range cases {
		got := nswFeedbackText(c.in)
		if got != c.want {
			t.Errorf("nswFeedbackText(%q)\n  got  %q\n  want %q", c.in, got, c.want)
		}
	}
	if got := nswFeedbackText("2  +  3"); got != "" {
		t.Errorf("双空格算式应漏算, got %q", got)
	}
	// 既有取舍不得被本次改动破坏: 整数区间 / 裸带号常量仍全拒
	for _, s := range []string{"2-3", "3-5", "9-12", "-4"} {
		if v, ok := nswEval(s); ok {
			t.Errorf("既有拒绝规则被破坏: nswEval(%q) = %q", s, v)
		}
	}
}
