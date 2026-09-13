package main

// ── nosword_selfloop_test.go — J6 自激回路回归 (20260912) ──
//
// 实录: 报告里引用无剑反馈原文 (形如 反引号包裹 + 前缀标记 + 分数算式) 之后,
// 无剑把"引用的错值例 + 邻近的正确值"混判为 corrected 并再次注入,
// 12 分钟后又出现一条一模一样的注入 (审计 15:34:28 / 15:46:51,
// 两条均 corrected=1 exprs=["3/4=0.75"]) —— 报告越解释, 越被触发。
// corrected 属 fresh, 不被会话级去重 nswFilterEchoed 过滤, 故每轮必注入。
//
// 修法 J6: 含铸剑炉自身反馈前缀的行整行不求值。理由是该前缀只由 nswFormatAnchors
// 生成, 断言不会自带 —— 出现即元语境 (引用/复述/讨论)。
// 本文件把闭环与对照固化为死程序判定。
//
// 注: 源码中反引号一律写 \x60 (Go raw string 可跨行, 裸反引号会吞掉后续代码)。

import "testing"

const evalMark = "\u3010\u6c42\u503c\u3011" // 反馈前缀

// TestNSWSelfLoop_EvalMarkLineRejected J6: 含反馈前缀的行不求值、不注入
func TestNSWSelfLoop_EvalMarkLineRejected(t *testing.T) {
	// 真实形态: 报告的报错原文行 (错值例与反馈原文同行)
	real := "**rc=1 FAIL** — 报错原文：" + bt + "不应注入反馈: （" + bt + "占比 3/4 = 0.8" + bt + "…） -> " + evalMark + "「3/4 = 0.75」" + bt
	got := nswEvaluate(real)
	if len(got) != 0 {
		t.Errorf("含反馈前缀的行不应求值, 实际 %+v", got)
	}
	if fb := nswFeedbackText(real); fb != "" {
		t.Errorf("含反馈前缀的行不应注入反馈, 实际 %q", fb)
	}
	// 反馈原文本身 (最直接的自激源)
	raw := evalMark + "「3/4 = 0.75」"
	if fb := nswFeedbackText(raw); fb != "" {
		t.Errorf("反馈原文不应再被求值 (自激), 实际 %q", fb)
	}
	// 多锚点形态
	multi := evalMark + "「8/13 = 0.615384615385」 「5/8 = 0.625」"
	if got := nswEvaluate(multi); len(got) != 0 {
		t.Errorf("多锚点反馈原文不应求值, 实际 %+v", got)
	}
}

// TestNSWSelfLoop_NoMarkStillEvaluated J6 对照: 去掉前缀标记后同形文本仍须求值
// (证明拦截源于标记本身, 而非算式形态被其他判据顺手拦掉)
func TestNSWSelfLoop_NoMarkStillEvaluated(t *testing.T) {
	// 去掉标记, 反引号也去掉 -> 裸断言
	bare := "占比 3/4 = 0.8"
	got := nswEvaluate(bare)
	if len(got) != 1 || got[0].expr != "3/4" {
		t.Fatalf("裸断言应求值, 实际 %+v", got)
	}
	if c := nswClassifyAnchor(bare, got[0]); c != "corrected" {
		t.Errorf("3/4=0.8 应判 corrected, 实际 %s", c)
	}
	// 带反引号但无标记 -> 由 J0/J0c 处理 (紧邻反引号照旧拒)
	quoted := "见 " + bt + "3*7 = 21" + bt + " 一例"
	if got := nswEvaluate(quoted); len(got) != 0 {
		t.Errorf("紧邻反引号应拒, 实际 %+v", got)
	}
}

// TestNSWSelfLoop_LineScope J6 边界: 标记只作用于所在行, 不跨行传染
func TestNSWSelfLoop_LineScope(t *testing.T) {
	// 标记在上一行, 本行是裸断言 -> 本行照旧求值
	cross := evalMark + "「1+1 = 2」\n占比 3/4 = 0.8"
	if got := nswEvaluate(cross); len(got) != 1 {
		t.Errorf("本行无标记, 应求值 1 个锚点, 实际 %+v", got)
	}
	// 标记在本行 -> 拒
	same := evalMark + "「1+1 = 2」 占比 3/4 = 0.8"
	if got := nswEvaluate(same); len(got) != 0 {
		t.Errorf("本行含标记, 应 0 个锚点, 实际 %+v", got)
	}
}

// TestNSWSelfLoop_NoCollateralDamage J6 反例: 正常算式零影响 (回归护栏)
func TestNSWSelfLoop_NoCollateralDamage(t *testing.T) {
	cases := []struct {
		in       string
		wantAnch int
	}{
		{"3*7 = 21", 1},
		{"总分 3+5 = 8", 1},
		{"37/36^2/45/43^1", 1},
		{"占比 3/4 = 0.8", 1},
		{"交付率 8/13", 0}, // J5 既有判据
	}
	for _, c := range cases {
		if got := len(nswEvaluate(c.in)); got != c.wantAnch {
			t.Errorf("nswEvaluate(%q) 得到 %d 个锚点, 期望 %d", c.in, got, c.wantAnch)
		}
	}
}
