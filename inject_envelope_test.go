package main

import (
	"os"
	"strings"
	"testing"
)

// ── 缺陷P 回归 (20260912): 程序注入消息必须自带来源信封 ──────────────────────
//
// 病象: 无剑反馈 / 虚报干预以裸 Role:"user" 进当轮 messages, 与主人真实输入形式
// 完全同形 -> 模型把死程序注入当成"用户发来的内容", 连续三轮报告建立在错误前提上,
// 用户当轮真正的指令被劫持。修法: autoInjectEnvelope 统一信封。

// 1) 无剑反馈注入: 必须带信封, 且信封在反馈本体之前 (信封在后=模型先读到裸反馈, 无效)
func TestInjectEnvelope_NoswordFeedback(t *testing.T) {
	fb, fresh, _ := nswFeedbackTextFresh("2 + 3")
	if fb == "" || len(fresh) == 0 {
		t.Fatalf("用例前提失效: %q 应产生 fresh 反馈", "2 + 3")
	}
	got := autoInjectEnvelope("算式求值校验", fb)
	if !strings.HasPrefix(got, "〔铸剑炉自动注入") {
		t.Errorf("信封必须在最前: %q", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("信封与本体应在不同行: %q", got)
	}
	if !strings.Contains(lines[0], "非用户消息") {
		t.Errorf("信封行须含来源声明: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "【求值】") {
		t.Errorf("本体行须保持原格式(信封不得改写反馈本体): %q", lines[1])
	}
	if strings.Contains(lines[0], "【求值】") {
		t.Errorf("信封行不得混入反馈标记(否则 J6 判据口径漂移): %q", lines[0])
	}
}

// 2) 虚报干预注入: 同样必须带信封, 且不吞掉原有的【证据先于声称】语义
func TestInjectEnvelope_VerifyClaim(t *testing.T) {
	got := verifyInterventionMsg("状态: clean")
	if !strings.Contains(got, "非用户消息") {
		t.Errorf("虚报干预缺来源信封: %q", got)
	}
	if !strings.Contains(got, "【证据先于声称】") {
		t.Errorf("信封不得吃掉干预本体: %q", got)
	}
	if strings.Index(got, "非用户消息") > strings.Index(got, "【证据先于声称】") {
		t.Errorf("信封必须在本体之前: %q", got)
	}
}

//  3. 关键: 注入文本自身不得再被无剑嗅探出锚点 (否则信封自己点燃新回路)
//     真实回环形态 = 模型下一轮复述/引用该注入文本 -> 若能被嗅探, 回路自我维持。
func TestInjectEnvelope_NotSelfSniffed(t *testing.T) {
	cases := []string{
		autoInjectEnvelope("算式求值校验", "【求值】「2 + 3 = 5」"),
		autoInjectEnvelope("算式求值校验", "【求值】「17*23 = 391」 「3/4 = 0.75」"),
		verifyInterventionMsg("git log --oneline -5; [go vet .] (clean);"),
		autoInjectEnvelope("完成态核验", "【证据先于声称】声称已提交 3 次"),
	}
	for i, c := range cases {
		if n := len(nswEvaluate(c)); n != 0 {
			t.Errorf("用例%d: 注入文本被嗅探出 %d 个锚点 -> 信封自激回路: %q", i, n, c)
		}
		if fb, _, _ := nswFeedbackTextFresh(c); fb != "" {
			t.Errorf("用例%d: 注入文本产生新反馈 %q (回路未断)", i, fb)
		}
	}
}

// 4) 空反馈不注入: 信封只在 fb 非空时附加 (否则每轮都塞一条空信封污染上下文)
func TestInjectEnvelope_EmptyFeedbackNoEnvelope(t *testing.T) {
	if fb, _, _ := nswFeedbackTextFresh("今天天气不错"); fb != "" {
		t.Fatalf("用例前提失效: 无算式文本不应产生反馈, 实得 %q", fb)
	}
	// 源码哨兵: 闭包内必须先判空再套信封, 顺序颠倒会导致空信封注入
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	iEmpty := strings.Index(s, "if fb == \"\" {")
	iEnv := strings.Index(s, "autoInjectEnvelope(")
	if iEmpty < 0 || iEnv < 0 {
		t.Fatalf("agent.go 缺少判空(%d)或信封(%d)调用", iEmpty, iEnv)
	}
	if iEmpty > iEnv {
		t.Errorf("闭包内判空必须在套信封之前 (否则空反馈也会被注入信封)")
	}
}

// 5) 接线哨兵: 两个注入构造点都必须套信封 (将来被删掉 = 缺陷P 复发)
func TestInjectEnvelope_WiredAtBothSites(t *testing.T) {
	checks := []struct {
		file string
		want int
		why  string
	}{
		{"agent.go", 1, "无剑注入闭包必须套信封 (缺失=反馈又被当成用户发言)"},
		{"verifyClaim.go", 1, "虚报干预必须套信封 (缺失=干预又被当成用户发言)"},
	}
	for _, c := range checks {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Count(string(b), "autoInjectEnvelope("); got != c.want {
			t.Errorf("%s 中 autoInjectEnvelope 出现 %d 次, 期望 %d (%s)", c.file, got, c.want, c.why)
		}
	}
}

// 6) 信封是常量前缀: 同一 kind 的输出前缀逐字节稳定 (缓存友好, 且可被模型学会忽略)
func TestInjectEnvelope_StablePrefix(t *testing.T) {
	a := autoInjectEnvelope("算式求值校验", "A")
	b := autoInjectEnvelope("算式求值校验", "B")
	pa, _ := strings.CutSuffix(a, "A")
	pb, _ := strings.CutSuffix(b, "B")
	if pa != pb {
		t.Errorf("信封前缀不稳定:\n%q\n%q", pa, pb)
	}
}
