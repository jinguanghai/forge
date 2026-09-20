package main

import (
	"os"
	"strings"
	"testing"
)

// ── 三十三期: 显式标记口径跳过"数值范围歧义"闸 ──────────────────────
//
// 背景: 一期 nswEval 的歧义闸是为"嗅探"设计的 —— 输入是自由文本切出的片段,
// 死程序不知道模型是否想算, 故 "3-5" 一律按区间拒绝 (宁漏勿误)。
// 但 {{}} 标记是模型的意图声明, 歧义已被消解; 继续套用嗅探闸会让升级的信息
// 价值归零 —— 实测 {{100-37}} 被拒 → 拒绝权回告 → 模型多烧一轮改写。

func TestNSWExprExplicit_RescuesRangeForms(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"100-37", "63"},
		{"1234-5678", "-4444"},
		{"3-5", "-2"},
		{"336-50", "286"},
		{"100 - 37", "63"},
		{"(100-37)", "63"},
		{"12.5-18.9", "-6.4"},
		{"007+1", "8"},
		{"-5", "-5"},
		{"0.45/+0.12", "3.75"},
	}
	for _, c := range cases {
		got, ok := nswEvalExplicit(c.expr)
		if !ok || got != c.want {
			t.Errorf("nswEvalExplicit(%q) = %q,%v; want %q,true", c.expr, got, ok, c.want)
		}
	}
}

// 嗅探口径必须逐字节不变: 本次改动只新增入口, 不改一期行为。
func TestNSWExprSniffPath_Unchanged(t *testing.T) {
	rejected := []string{
		"100-37", "1234-5678", "3-5", "336-50", "100 - 37", "(100-37)",
		"12.5-18.9", "10-4-3", "9-13-2026", "50-78-2", "138-1234-5678",
		"2026-09-20", "2026-09", "007+1", "-5", "0.45/+0.12",
	}
	for _, e := range rejected {
		if v, ok := nswEval(e); ok {
			t.Errorf("嗅探口径回归: nswEval(%q) = %q, 应为拒绝", e, v)
		}
	}
	accepted := []struct{ expr, want string }{
		{"1/5", "0.2"}, {"2+2", "4"}, {"0.7*480", "336"},
		{"480*0.7-50", "286"}, {"18.9-12.5", "6.4"}, {"(10-4)-3", "3"},
	}
	for _, c := range accepted {
		got, ok := nswEval(c.expr)
		if !ok || got != c.want {
			t.Errorf("嗅探口径回归: nswEval(%q) = %q,%v; want %q,true", c.expr, got, ok, c.want)
		}
	}
}

// 形态闸两条路径共用: 日期/电话/编号即使被显式标记也拒绝 ——
// 算出的错值污染上下文, 危害远大于漏算。
func TestNSWExprExplicit_KeepsFormGuards(t *testing.T) {
	rejected := []string{
		"9-13-2026",           // 美式日期 (3 段)
		"10-4-3",              // 3 段以上纯数字串
		"50-78-2",             // CAS 号
		"138-1234-5678",       // 电话
		"2026-09-20",          // ISO 日期
		"2026-09",             // 年月
		"12345678901234567-1", // 超长整数 (16 位以上字面量)
	}
	for _, e := range rejected {
		if v, ok := nswEvalExplicit(e); ok {
			t.Errorf("显式口径不应放行 %q, 却算出 %q (错值会污染上下文)", e, v)
		}
	}
}

// 语法闸两条路径共用: 非算式字符 / 悬空点 / 无算符纯常量 / 空串。
func TestNSWExprExplicit_KeepsSyntaxGuards(t *testing.T) {
	rejected := []string{"100元-37", "=100-37", "9.", "100", "abc", "", "  ", "100;37", "{{100-37}}"}
	for _, e := range rejected {
		if v, ok := nswEvalExplicit(e); ok {
			t.Errorf("显式口径不应放行语法非法输入 %q, 却算出 %q", e, v)
		}
	}
}

// 精度闸两条路径共用: NaN / Inf / 2^53 边界 —— 算不准就不算。
func TestNSWExprExplicit_KeepsPrecisionGuards(t *testing.T) {
	if v, ok := nswEvalExplicit("1/0"); ok {
		t.Errorf("Inf 应被拒, 得 %q", v)
	}
	if v, ok := nswEvalExplicit("8388608*1073741824"); ok { // = 2^53
		t.Errorf("2^53 应被拒, 得 %q", v)
	}
	if v, ok := nswEvalExplicit("4194304*1073741824"); !ok || v != "4503599627370496" { // = 2^52
		t.Errorf("2^52 应放行, 得 %q,%v", v, ok)
	}
}

func TestNSWExprRender_RescuesSubtraction(t *testing.T) {
	out, bad := nswExprRender("共 {{100-37}} 元。")
	if len(bad) != 0 {
		t.Fatalf("不应有被拒标记, 得 %v", bad)
	}
	if out != "共 63 元。" {
		t.Errorf("渲染 = %q; want %q", out, "共 63 元。")
	}
	// 形态闸仍生效: 标记原样保留并进入 bad (拒绝权回告)
	out2, bad2 := nswExprRender("共 {{100元-37}} 元。")
	if len(bad2) != 1 || out2 != "共 {{100元-37}} 元。" {
		t.Errorf("非法标记应原样保留并计入 bad, 得 out=%q bad=%v", out2, bad2)
	}
}

func TestNSWExprCountSaved(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"共 {{100-37}} 元。", 1},
		{"共 {{480*0.7-50}} 元。", 0},
		{"{{100-37}} 与 {{1234-5678}}", 2},
		{"{{100元-37}}", 0},
		{"无标记", 0},
	}
	for _, c := range cases {
		if got := nswExprCountSaved(c.in); got != c.want {
			t.Errorf("nswExprCountSaved(%q) = %d; want %d", c.in, got, c.want)
		}
	}
}

func TestNSWExprFilter_SavedCrossChunk(t *testing.T) {
	f := &nswExprFilter{}
	var sb strings.Builder
	for _, c := range []string{"共 {{100", "-37}}", " 元。"} {
		sb.WriteString(f.feed(c))
	}
	sb.WriteString(f.flush())
	if got := sb.String(); got != "共 63 元。" {
		t.Errorf("跨块渲染 = %q; want %q", got, "共 63 元。")
	}
	if f.saved != 1 {
		t.Errorf("saved = %d; want 1", f.saved)
	}
	if f.marks != 1 {
		t.Errorf("marks = %d; want 1", f.marks)
	}
	if len(f.bad) != 0 {
		t.Errorf("bad = %v; want 空", f.bad)
	}
}

func TestNSWExprExplicit_Deterministic(t *testing.T) {
	const expr = "1234-5678"
	first, ok := nswEvalExplicit(expr)
	if !ok {
		t.Fatalf("nswEvalExplicit(%q) 未识别", expr)
	}
	for i := 0; i < 500; i++ {
		if v, ok := nswEvalExplicit(expr); !ok || v != first {
			t.Fatalf("第 %d 次不一致: %q,%v; want %q", i, v, ok, first)
		}
	}
}

// 接线哨兵: 显式入口必须存在且被标记路径调用; 一期入口不得被删。
func TestNSWExprExplicit_Wired(t *testing.T) {
	exprSrc, err := os.ReadFile("nosword_expr.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exprSrc), "nswEvalExplicit(") {
		t.Error("nosword_expr.go 未调用 nswEvalExplicit —— 标记路径没接上显式口径")
	}
	if !strings.Contains(string(exprSrc), `"saved":`) {
		t.Error("nswExprAudit 缺 saved 字段 —— 三十三期收益不可度量")
	}
	nswSrc, err := os.ReadFile("nosword.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, need := range []string{
		"func nswEval(expr string) (string, bool)",
		"func nswEvalExplicit(expr string) (string, bool)",
		"func nswEvalMode(expr string, explicit bool)",
		"func nswIsCandidate(s string) bool",
		"func nswIsCandidateMode(s string, explicit bool) bool",
	} {
		if !strings.Contains(string(nswSrc), need) {
			t.Errorf("nosword.go 缺 %q", need)
		}
	}
	// 标记路径必须走显式口径 (行为断言, 不只查文本)
	if v, ok := nswExprEvalMark("100-37"); !ok || v != "63" {
		t.Errorf("nswExprEvalMark(\"100-37\") = %q,%v; want 63,true (标记路径未接显式口径)", v, ok)
	}
}
