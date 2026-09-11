package main

// ── nosword_dedup_test.go — 无剑复述过滤 (缺陷M: 自激循环) ──
//
// 现场证据 (20260911 一次真实会话):
//   累计反馈 30 次, 不同锚点仅 11 个 -> 重复 19 次 (63% 冗余), 且不收敛。
// 根因: nswFeedbackText 只扫本轮 asst (这本身是对的), 但 LLM 在后续轮次里
// "引用"前面反馈过的算式时 (举例/列表格/复述分析), 引用与真实计算形式完全同形 ——
// 嗅探器只有形式没有意图, 无法区分 -> 每轮都反馈 -> LLM 看到反馈又继续引用。
//
// 修法演进 (死程序抓出的缺陷, 非推测):
//   第一版"已反馈过的锚点去重表"被 TestNSWEndToEnd_RoundCap 打回 —— LLM 连续两轮
//   输出同一个错误 "2+2 = 5", 第二轮因"见过"而不再纠正。方向错了。
//   定稿: 跳过条件 = 原文已自行给出同形正确结论 (纯复述, 零信息增量)。
//   无状态、纯函数, 且写错/未给结论的锚点每轮都照常反馈。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNSWFresh_EchoedConclusionSkipped 现场形态: 引用前面反馈过的算式, 原文自带正确值
func TestNSWFresh_EchoedConclusionSkipped(t *testing.T) {
	echo := "占比 2608/20564 = 0.12682357518, 另一个 7/8 = 0.875"
	fb, fresh, total := nswFeedbackTextFresh(echo)
	if total != 2 {
		t.Fatalf("应切出 2 个锚点, 实际 %d: %+v", total, nswEvaluate(echo))
	}
	if fb != "" || len(fresh) != 0 {
		t.Errorf("原文已含正确结论 -> 纯复述应全部跳过, 实际 fb=%q fresh=%d", fb, len(fresh))
	}
}

// TestNSWFresh_RepeatedWrongAnswerStillCorrected 死程序抓出的缺陷回归:
// LLM 连续多轮输出同一个错误算式, 必须每轮都纠正 (去重表方案在此漏纠)。
func TestNSWFresh_RepeatedWrongAnswerStillCorrected(t *testing.T) {
	asst := "2+2 = 5。"
	for round := 1; round <= 3; round++ {
		fb, fresh, total := nswFeedbackTextFresh(asst)
		if total != 1 || len(fresh) != 1 {
			t.Fatalf("第%d轮应命中 1 锚点, 实际 total=%d fresh=%d", round, total, len(fresh))
		}
		if !strings.Contains(fb, "2+2 = 4") {
			t.Errorf("第%d轮必须纠正为 2+2 = 4, 实际 fb=%q", round, fb)
		}
		if nswClassifyAnchor(asst, fresh[0]) != "corrected" {
			t.Errorf("第%d轮应判为 corrected", round)
		}
	}
}

// TestNSWFresh_MixedEchoAndError 混合: 复述的跳过, 写错的照常纠正
func TestNSWFresh_MixedEchoAndError(t *testing.T) {
	asst := "占比 2608/20564 = 0.12682357518, 另一个 7/8 = 0.874"
	fb, fresh, total := nswFeedbackTextFresh(asst)
	if total != 2 {
		t.Fatalf("应切出 2 个锚点, 实际 %d: %+v", total, nswEvaluate(asst))
	}
	if len(fresh) != 1 {
		t.Fatalf("应只保留写错的 7/8, 实际 fresh=%d fb=%q", len(fresh), fb)
	}
	if fresh[0].expr != "7/8" || fresh[0].val != "0.875" {
		t.Errorf("保留的锚点错误: %+v", fresh[0])
	}
	if strings.Contains(fb, "2608/20564") {
		t.Errorf("复述锚点不应出现在反馈里: %q", fb)
	}
}

// TestNSWFresh_NoConclusionStillReported 原文只写算式未给结论 -> 补全, 照常反馈
func TestNSWFresh_NoConclusionStillReported(t *testing.T) {
	fb, fresh, total := nswFeedbackTextFresh("7/8 是多少")
	if total != 1 || len(fresh) != 1 || fb == "" {
		t.Fatalf("未给结论应反馈, 实际 total=%d fresh=%d fb=%q", total, len(fresh), fb)
	}
	if !strings.Contains(fb, "7/8 = 0.875") {
		t.Errorf("反馈内容错误: %q", fb)
	}
}

// TestNSWFresh_PlainExprMatchesOldFeedback 无冗余时与旧入口逐字节一致 (零回归)
func TestNSWFresh_PlainExprMatchesOldFeedback(t *testing.T) {
	inputs := []string{
		"3*7 = 20",      // 写错 -> corrected
		"2*3+4",         // 只写算式 -> completed
		"帮我算 10/4",      // completed
		"今天 2026-09-11", // 负样本: 日期
		"",
	}
	for _, in := range inputs {
		want := nswFeedbackText(in)
		got, _, _ := nswFeedbackTextFresh(in)
		if got != want {
			t.Errorf("无冗余时应与 nswFeedbackText 逐字节相同: %q -> %q (期望 %q)", in, got, want)
		}
	}
}

// TestNSWFresh_Pure 纯函数性: 同输入同输出, 无隐藏状态
func TestNSWFresh_Pure(t *testing.T) {
	for _, in := range []string{"2+2 = 5", "7/8 是多少", "2608/20564 = 0.12682357518"} {
		fb1, f1, t1 := nswFeedbackTextFresh(in)
		fb2, f2, t2 := nswFeedbackTextFresh(in)
		if fb1 != fb2 || len(f1) != len(f2) || t1 != t2 {
			t.Errorf("纯函数性破坏: %q -> (%q,%d,%d) vs (%q,%d,%d)", in, fb1, len(f1), t1, fb2, len(f2), t2)
		}
	}
}

// TestNSWFresh_RealSessionLoopCut 现场形态复现: 同一批引用在连续 3 轮原样重复。
// 文本由死程序动态构造 (表达式 + 真实求值结果), 避免手写近似值把 redundant 变成
// corrected —— 第一版手写 "0.0268238" 就踩了这个坑, 被测试当场抓出。
func TestNSWFresh_RealSessionLoopCut(t *testing.T) {
	exprs := []string{"2608/20564", "495/18452", "7/8"}
	parts := make([]string, 0, len(exprs))
	for _, e := range exprs {
		v, ok := nswEval(e)
		if !ok {
			t.Fatalf("基线表达式无法求值: %q", e)
		}
		parts = append(parts, e+" = "+v)
	}
	base := strings.Join(parts, " 与 ")
	perRound := len(nswEvaluate(base))
	if perRound != len(exprs) {
		t.Fatalf("基线文本应切出 %d 个锚点, 实际 %d: %+v", len(exprs), perRound, nswEvaluate(base))
	}
	plain, freshTotal, rounds := 0, 0, 0
	for i := 0; i < 3; i++ {
		plain += perRound // 旧行为: 每轮全量反馈
		fb, fresh, _ := nswFeedbackTextFresh(base)
		if fb != "" {
			rounds++
		}
		freshTotal += len(fresh)
	}
	t.Logf("3 轮原样重复: 旧行为反馈 %d 锚点 / 新行为 %d 锚点 (%d 轮)", plain, freshTotal, rounds)
	if freshTotal != 0 || rounds != 0 {
		t.Errorf("原文自带正确值, 3 轮都应零反馈, 实际 %d 锚点 / %d 轮", freshTotal, rounds)
	}
	if plain != perRound*3 {
		t.Errorf("基线口径错误: %d", plain)
	}
}

// TestNSWClassifyAnchor_ThreeWay 埋点分类三态
func TestNSWClassifyAnchor_ThreeWay(t *testing.T) {
	cases := []struct {
		name string
		asst string
		a    nswAnchor
		want string
	}{
		{"同形结论=空转", "3/4=0.75 是对的", nswAnchor{"3/4", "0.75"}, "redundant"},
		{"带空格同形=空转", "7/8 = 0.875", nswAnchor{"7/8", "0.875"}, "redundant"},
		{"值不同=纠错", "算得 7/8 = 0.874", nswAnchor{"7/8", "0.875"}, "corrected"},
		{"只写算式=补全", "7/8 是多少", nswAnchor{"7/8", "0.875"}, "completed"},
		{"无任何数值=补全", "帮我看看 2*3+4", nswAnchor{"2*3+4", "10"}, "completed"},
	}
	for _, c := range cases {
		if got := nswClassifyAnchor(c.asst, c.a); got != c.want {
			t.Errorf("%s: %q + %+v -> %s (期望 %s)", c.name, c.asst, c.a, got, c.want)
		}
	}
}

// TestNSWLeadingNum 前导数字串提取
func TestNSWLeadingNum(t *testing.T) {
	cases := map[string]string{
		"0.75 元": "0.75",
		"-3.5度":  "-3.5",
		".5":     ".5",
		"12":     "12",
		"abc":    "",
		"":       "",
		"+7":     "+7",
		"1.2.3":  "1.2",
	}
	for in, want := range cases {
		if got := nswLeadingNum(in); got != want {
			t.Errorf("nswLeadingNum(%q) = %q (期望 %q)", in, got, want)
		}
	}
}

// TestNSWNumEqual 数值字符串等值比较 (严格相等, 不含近似口径)
func TestNSWNumEqual(t *testing.T) {
	for _, c := range [][2]string{{"0.30", "0.3"}, {"2", "2.0"}, {"-1.5", "-1.5"}} {
		if !nswNumEqual(c[0], c[1]) {
			t.Errorf("%q 应等于 %q", c[0], c[1])
		}
	}
	// 注: 四舍五入短形式 (0.1268 vs 0.12682357518) 判为不等 -> 会走 corrected。
	// 这是有意的口径选择: 宁可高估纠错, 不可低估(低估会掩盖真实纠错)。
	for _, c := range [][2]string{{"0.1", "0.2"}, {"", "0"}, {"abc", "0"}, {"0.1268", "0.12682357518"}} {
		if nswNumEqual(c[0], c[1]) {
			t.Errorf("%q 不应等于 %q", c[0], c[1])
		}
	}
}

// TestNSWAudit_WritesLine 埋点真的落盘 (此前 gate_audit.jsonl 零条 nsw 记录 -> 触发率不可测)
func TestNSWAudit_WritesLine(t *testing.T) {
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}
	asst := "先算 3*7 = 20, 所以是 20"
	all := nswEvaluate(asst)
	fresh := nswFilterEchoed(asst, all)
	if len(fresh) != 1 {
		t.Fatalf("应保留 1 个写错的锚点, 实际 %d", len(fresh))
	}
	nswAudit(a, asst, fresh, len(all))

	b, err := os.ReadFile(filepath.Join(dir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("埋点未落盘: %v", err)
	}
	line := strings.TrimSpace(string(b))
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(line), &d); err != nil {
		t.Fatalf("埋点不是合法 JSON: %v (%q)", err, line)
	}
	if d["event"] != "nosword" {
		t.Errorf("event 应为 nosword, 实际 %v", d["event"])
	}
	if d["corrected"] != float64(1) {
		t.Errorf("corrected 应为 1 (原文写错被纠正), 实际 %v", d["corrected"])
	}
	if d["anchors"] != float64(1) || d["fresh"] != float64(1) || d["skipped"] != float64(0) {
		t.Errorf("计数错误: anchors=%v fresh=%v skipped=%v", d["anchors"], d["fresh"], d["skipped"])
	}
	if _, ok := d["ts"].(string); !ok {
		t.Errorf("缺 ts 字段: %v", d["ts"])
	}
	t.Logf("埋点: %s", line)
}

// TestNSWAudit_SkippedNotWritten 纯复述被过滤后不写审计行 (无新信息 = 无记录)
func TestNSWAudit_SkippedNotWritten(t *testing.T) {
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}
	asst := "占比 7/8 = 0.875"
	all := nswEvaluate(asst)
	fresh := nswFilterEchoed(asst, all)
	if len(fresh) != 0 {
		t.Fatalf("纯复述应全部过滤, 实际 %d", len(fresh))
	}
	nswAudit(a, asst, fresh, len(all))
	if _, err := os.Stat(filepath.Join(dir, "gate_audit.jsonl")); !os.IsNotExist(err) {
		t.Errorf("零有效锚点不应写审计行, err=%v", err)
	}
}
