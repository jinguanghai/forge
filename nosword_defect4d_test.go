package main

// ── nosword_defect4d_test.go — 缺陷④-d 回归 (20260912) ──
//
// 实录: 一轮报告里用行内代码引用了一个错值例子 —— 形如 "占比 3/4 = 0.8" 加反引号包裹。
// 无剑把它当断言求值并注入【求值】反馈 (审计 15:34:28: anchors=1 fresh=1 corrected=1)。
// 根因: J0 判据是"候选紧邻反引号", 而 LLM 写引用时反引号包的是**短语** ——
// 候选前紧邻是 '比'、后紧邻是 '=', J0 完全失效。
// 危害最大的一条不是假阳性本身, 而是自激回路: 讨论无剑的内容被求值 ->
// 注入反馈 -> LLM 注意力从用户任务被拽到求值噪音上 (实测发生过)。
// 修法: J0c = 行内代码跨度判据 (按行统计候选前的反引号奇偶, 奇数即在跨度内)。
// 本文件把四形态固化为死程序判定。
//
// 注: 源码中反引号一律写 \x60 —— Go 的 raw string 可跨行, 裸反引号会把
// 后续代码吞进字面量 (本文件首版即因此编译失败)。

import "testing"

const bt = "\x60" // 反引号

// TestNSWDefect4d_InlineCodePhraseRejected ④-d: 行内代码跨度内的算式不求值、不反馈
func TestNSWDefect4d_InlineCodePhraseRejected(t *testing.T) {
	cases := []string{
		"（" + bt + "占比 3/4 = 0.8" + bt + " 的错值恰恰该抓）",
		bt + "占比 3/4 = 0.8" + bt,
		"见 " + bt + "交付率 8/13 = 0.6" + bt + " 一例",
		"正文 " + bt + "3*7 = 21" + bt + " 也是引用",
	}
	for _, s := range cases {
		if got := nswEvaluate(s); len(got) != 0 {
			t.Errorf("行内代码跨度内不应求值: %q -> %+v", s, got)
		}
		if fb := nswFeedbackText(s); fb != "" {
			t.Errorf("行内代码跨度内不应注入反馈: %q -> %q", s, fb)
		}
	}
}

// TestNSWDefect4d_BareAssertionStillEvaluated ④-d 反例: 裸句断言照旧求值 (不误伤)
func TestNSWDefect4d_BareAssertionStillEvaluated(t *testing.T) {
	asst := "占比 3/4 = 0.8"
	got := nswEvaluate(asst)
	if len(got) != 1 || got[0].expr != "3/4" {
		t.Fatalf("裸句断言应求值, 实际 %+v", got)
	}
	if c := nswClassifyAnchor(asst, got[0]); c != "corrected" {
		t.Errorf("3/4=0.8 应判 corrected, 实际 %s", c)
	}
}

// TestNSWDefect4d_LineIsolation ④-d 边界: 行内代码不跨行, 上一行的反引号不污染本行
func TestNSWDefect4d_LineIsolation(t *testing.T) {
	asst := "上一行提到 " + bt + " 符号\n占比 3/4 = 0.8"
	if got := nswEvaluate(asst); len(got) != 1 {
		t.Errorf("本行无反引号, 应求值 1 个锚点, 实际 %+v", got)
	}
	quoted := "上一行提到 " + bt + " 符号\n" + bt + "占比 3/4 = 0.8" + bt
	if got := nswEvaluate(quoted); len(got) != 0 {
		t.Errorf("本行在代码跨度内, 应 0 个锚点, 实际 %+v", got)
	}
}

// TestNSWDefect4d_KnownTradeoff ④-d 已知取舍: 行内孤立反引号 -> 该行后续算式漏算
// 这是"宁漏勿误"的显式代价 (与缺陷N 同一取舍), 固化当前行为防无声变更。
func TestNSWDefect4d_KnownTradeoff(t *testing.T) {
	asst := "这里用了 " + bt + " 符号, 占比 3/4 = 0.8"
	if got := nswEvaluate(asst); len(got) != 0 {
		t.Errorf("孤立反引号后漏算 (已知取舍), 实际 %+v —— 若行为变更请更新本用例与注释", got)
	}
}

// TestNSWDefect4d_CodeFenceAndTable 复核: J0b/J3 既有判据未被 J0c 干扰
func TestNSWDefect4d_CodeFenceAndTable(t *testing.T) {
	fence := "\x60\x60\x60\n占比 3/4 = 0.8\n\x60\x60\x60"
	if got := nswEvaluate(fence); len(got) != 0 {
		t.Errorf("代码块内不应求值, 实际 %+v", got)
	}
	table := "| " + bt + "占比 3/4 = 0.8" + bt + " | — |"
	if got := nswEvaluate(table); len(got) != 0 {
		t.Errorf("表格行不应求值, 实际 %+v", got)
	}
}
