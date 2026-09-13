package main

// ── nosword_defect4_test.go — 缺陷④ 回归 (20260912) ──
//
// 实录: 一轮报告里写了 "交付率 8/13, 交付后正确率 5/8, 数学子项 2/3, 总分 5/13"
// 与四个精确负值。无剑逐条求值并注入【求值】反馈, 其中 4 条把本来正确的精确值判为
// "写错了" (审计: corrected=4) —— 反馈把 LLM 从用户任务上拽到求值噪音上。
// 两个根因:
//   ④-a 比例陈述不带结构性标记, 语境闸 J0~J4 拦不住 (元语境外的最高发假阳性形态)
//   ④-b nswNumEqual 用精确相等, 12 位截断值 != 16 位精确值 -> 假纠错
// 本文件把两条固化为死程序判定。

import (
	"strings"
	"testing"
)

// TestNSWNumEqual_RelTol ④-b 容差口径: 舍入/截断差异判等, 真实错值仍判不等
func TestNSWNumEqual_RelTol(t *testing.T) {
	eq := [][2]string{
		{"0.30", "0.3"}, {"2", "2.0"}, {"-1.5", "-1.5"}, {"0", "0.0"},
		// 实测对: LLM 写 16 位精确值, 死程序输出 12 位 -> 相对差 6.3e-13
		{"0.615384615385", "0.6153846153846154"},
		{"-0.615384615385", "-0.6153846153846154"},
	}
	for _, c := range eq {
		if !nswNumEqual(c[0], c[1]) {
			t.Errorf("nswNumEqual(%q,%q) 应为真 (容差 %.0e 内)", c[0], c[1], nswNumRelTol)
		}
	}
	ne := [][2]string{
		{"0.1", "0.2"}, {"", "0"}, {"abc", "0"},
		// 短形式仍是真错值 (相对差 >> 容差) -> 照旧走 corrected
		{"0.1268", "0.12682357518"},
		{"0.615", "0.6153846153846154"},
		{"0", "1e-15"},
	}
	for _, c := range ne {
		if nswNumEqual(c[0], c[1]) {
			t.Errorf("nswNumEqual(%q,%q) 应为假", c[0], c[1])
		}
	}
}

// TestNSWDefect4a_RatioStatementNotEvaluated ④-a: 比例陈述不产生锚点, 不注入反馈
func TestNSWDefect4a_RatioStatementNotEvaluated(t *testing.T) {
	asst := "交付率 8/13，交付后正确率 5/8，数学子项 2/3，总分 5/13。"
	got := nswEvaluate(asst)
	if len(got) != 0 {
		t.Errorf("比例陈述不应求值, 实际得到 %d 个锚点: %+v", len(got), got)
	}
	if fb := nswFeedbackText(asst); fb != "" {
		t.Errorf("比例陈述不应注入反馈, 实际: %q", fb)
	}
}

// TestNSWDefect4a_RealAssertionStillCaught ④-a 反例: 带结论的断言(含错值)仍须抓
func TestNSWDefect4a_RealAssertionStillCaught(t *testing.T) {
	asst := "占比 3/4 = 0.8"
	anchors := nswEvaluate(asst)
	if len(anchors) != 1 || anchors[0].expr != "3/4" {
		t.Fatalf("带结论的比例断言应求值, 实际 %+v", anchors)
	}
	fb, fresh, total := nswFeedbackTextFresh(asst)
	if len(fresh) != 1 || total != 1 {
		t.Fatalf("错值应保留为 fresh, 实际 fresh=%d total=%d", len(fresh), total)
	}
	if c := nswClassifyAnchor(asst, anchors[0]); c != "corrected" {
		t.Errorf("3/4=0.8 应判 corrected, 实际 %s", c)
	}
	if !strings.Contains(fb, "3/4") {
		t.Errorf("反馈应含 3/4, 实际 %q", fb)
	}
	// 结论正确 -> 纯复述, 不反馈
	if fb2, fresh2, _ := nswFeedbackTextFresh("占比 7/8 = 0.875"); fb2 != "" || len(fresh2) != 0 {
		t.Errorf("正确结论应被过滤, 实际 %q", fb2)
	}
}

// TestNSWDefect4b_NoFalseCorrection ④-b: 精确值与截断值互不判错 (假纠错清零)
func TestNSWDefect4b_NoFalseCorrection(t *testing.T) {
	for _, asst := range []string{
		"8/13 = 0.6153846153846154",
		"- 8/13 = -0.6153846153846154",
		"5/8 = 0.625",
	} {
		anchors := nswEvaluate(asst)
		if len(anchors) != 1 {
			t.Fatalf("%q 应得到 1 个锚点, 实际 %+v", asst, anchors)
		}
		if c := nswClassifyAnchor(asst, anchors[0]); c != "redundant" {
			t.Errorf("%q 应判 redundant (原文已给正确值), 实际 %s", asst, c)
		}
		if fb, fresh, _ := nswFeedbackTextFresh(asst); fb != "" || len(fresh) != 0 {
			t.Errorf("%q 不应注入反馈 (假纠错), 实际 %q", asst, fb)
		}
	}
}

// TestNSWDefect4a_J5Scope ④-a 边界: J5 只作用于"比例语境且无结论", 普通算式零影响
func TestNSWDefect4a_J5Scope(t *testing.T) {
	cases := []struct {
		in       string
		wantAnch int
		why      string
	}{
		{"3*7 = 21", 1, "普通算式照旧"},
		{"总分 3+5 = 8", 1, "前置量词但带结论 -> 放行"},
		{"交付率 8/13", 0, "前置量词无结论 -> 拒"},
		{"37/36^2/45/43^1", 1, "含运算符的连除不是斜杠列举, 照旧"},
	}
	for _, c := range cases {
		if got := len(nswEvaluate(c.in)); got != c.wantAnch {
			t.Errorf("nswEvaluate(%q) 得到 %d 个锚点, 期望 %d (%s)", c.in, got, c.wantAnch, c.why)
		}
	}
}
