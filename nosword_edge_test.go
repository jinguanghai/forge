package main

import "testing"

// C1 边界修复验证: 带空格算式应命中 (漏算修复)
func TestNSWEdge_SpacedExpr(t *testing.T) {
	cases := map[string]string{
		"2 * 3 + 4":       "10",
		"10 / 4 = 2.5":    "2.5",
		"3 * 4 = 12":      "12",
		"帮我算 2 * 3 + 4":   "10",
		"面积 = 3.14 * 2^2": "12.56",
		"2*3 + 4":         "10",
		"(1 + 2) * 3":     "9",
		"sqrt( 16 )":      "4",
	}
	for in, want := range cases {
		as := nswEvaluate(in)
		if len(as) != 1 || as[0].val != want {
			t.Errorf("输入 %q 期望 val=%s, 实际 %+v", in, want, as)
		}
	}
}

// C1 边界修复验证: 日期/区间/大数/裸带号 必须全部拒绝 (误算修复)
func TestNSWEdge_Negatives(t *testing.T) {
	bad := []string{
		"今天是 2026-09-10",
		"2026/09/10",
		"2026-09-10 10:44:40",
		"2026-09",
		"3-5个工作日",
		"页码 12-15",
		"共 5-8 项",
		"第1-2季度",
		"2-3天",
		"温度 -3 度",
		"99999999999999999999+1",
		"价格12元，版本2.0和3.5倍",
		"身份证 11010119900307123X",
		"2.0",
		"12",
		"(16)",
	}
	for _, in := range bad {
		if as := nswEvaluate(in); len(as) != 0 {
			t.Errorf("输入 %q 不应命中, 实际 %+v", in, as)
		}
	}
}

// C1 浮点长尾修复
func TestNSWEdge_FloatTail(t *testing.T) {
	cases := map[string]string{
		"0.1+0.2": "0.3",
		"1/3":     "0.333333333333",
		"10/4":    "2.5",
	}
	for in, want := range cases {
		as := nswEvaluate(in)
		if len(as) != 1 || as[0].val != want {
			t.Errorf("输入 %q 期望 val=%s, 实际 %+v", in, want, as)
		}
	}
}

// C1 不得破坏原正样本
func TestNSWEdge_OriginalPositive(t *testing.T) {
	cases := map[string]string{
		"2*3+4":           "10",
		"sqrt(16)":        "4",
		"(1+2)*3":         "9",
		"10/4":            "2.5",
		"((5+3)*2)+1":     "17",
		"round(28.274,2)": "28.27",
	}
	for in, want := range cases {
		v, ok := nswEval(in)
		if !ok || v != want {
			t.Errorf("输入 %q 期望 %s, 实际 %s ok=%v", in, want, v, ok)
		}
	}
}

// 确定性 (缓存前缀必要条件) 仍然成立
func TestNSWEdge_Determinism(t *testing.T) {
	txt := "帮我算 2 * 3 + 4 和 0.1+0.2 和 sqrt(16)"
	first := nswFeedbackText(txt)
	if first == "" {
		t.Fatal("应命中")
	}
	for i := 0; i < 500; i++ {
		if got := nswFeedbackText(txt); got != first {
			t.Fatalf("第%d次不确定: %q vs %q", i, got, first)
		}
	}
	t.Logf("feedback=%q", first)
}
