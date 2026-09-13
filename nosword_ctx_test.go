package main

// ── nosword_ctx_test.go — 缺陠N 回归: 语境闸 (引用位置不求值) ──
// 证据来源: gate_audit.jsonl 7 条 nosword 真实记录, 累计 27 锚点 / 18 fresh,
// 其中 11 个 fresh 是语义错位的假阳性 —— 全部发生在"讨论无剑模式自身"的元语境。
// 本文件把这些真实原文形态固化为回归用例。

import "testing"

// TestNSWDefectN_QuotedExprRejected 引用位置的算式不求值 (J0~J4)
func TestNSWDefectN_QuotedExprRejected(t *testing.T) {
	reject := []string{
		// 记录[7] 原文形态: 计划书逐条举例假阳性, 结果自己成了假阳性源
		"| `0/4`、`1/5` | `从 **0/4** 变成 1/5` | Markdown 强调符 `**` 被当成乘号 |",
		"| `30+60` | `9:30+60` | 冒号非算式字符 |",
		"| `0.02/1/4` | 三档价格列表 | 斜杠列举被当除法链 |",
		"| `7/8` | 表格 `| 7/8 | 7/8 | 0.875 |` | 表格单元格 |",
		"- **A 假阳性注入**: 当前 `fresh` 12 条中 5 条语义错位(**42%**) → 目标 **0/12**",
		// 无反引号但带其他结构标记
		"9:30+60 属同一类",
		"命中0.02/1/4 三档价格",
		"比例 1:5",
		"```\n2+3\n```",
		"代码块内 `2+3` 的算式",
	}
	for _, in := range reject {
		if as := nswEvaluate(in); len(as) != 0 {
			t.Errorf("引用位置的算式应不求值: %q -> %+v", in, as)
		}
	}
}

// TestNSWDefectN_AssertionStillEvaluated 真断言照常求值 —— 语境闸不得伤及主干
func TestNSWDefectN_AssertionStillEvaluated(t *testing.T) {
	accept := map[string]string{
		"帮我算 3 * 7":     "21",
		"2+2":           "4",
		"总价是 12.5*3":    "37.5",
		"2 + 3":         "5",
		"如下: 3 * 7":     "21", // 冒号后有空格 -> 不是时间/比例前缀
		"3 * 7 / 1 + 1": "22",
		"1+1=2":         "2",
		"10/4":          "2.5", // 两段斜杠不是列举
	}
	for in, want := range accept {
		as := nswEvaluate(in)
		if len(as) == 0 {
			t.Errorf("真断言被误杀: %q (期望 %s)", in, want)
			continue
		}
		if as[0].val != want {
			t.Errorf("值错: %q -> %s (期望 %s)", in, as[0].val, want)
		}
	}
}
