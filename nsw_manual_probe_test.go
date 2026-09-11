package main

import "testing"

// TestNSWManualProbe 人工探针: 打印真实输入的实际求值结果 (证据表, 非断言)
func TestNSWManualProbe(t *testing.T) {
	cases := []string{
		"价格 12.5-18.9 元", "18.9-12.5", "1.5-2", "2-1.5", "36.2-37.3",
		"3-5个工作日", "5-3", "3 * 7", "2*3+4", "10/4", "0.1+0.2",
		"2026-09-10", "第 3-5 章", "体重 45.5-62.5kg",
		// 20260910 用户真实反馈原文 (活体证据: 旧代码在此处算出 -9 / 9007199254740992)
		"0-9.", "2^53+1", "1+2.", "2+53",
	}
	for _, c := range cases {
		as := nswEvaluate(c)
		if len(as) == 0 {
			t.Logf("%-24q -> (拒绝/无锚点)", c)
		} else {
			for _, a := range as {
				t.Logf("%-24q -> %s = %s", c, a.expr, a.val)
			}
		}
	}
}
