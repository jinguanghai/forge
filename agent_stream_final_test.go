package main

// agent_stream_final_test.go — B3 批3 单测镜像 (fractal F4)。
//
// 直接单测 turnFinalizer.finalize 的三条出口, 不经 RunStream 全链路:
//   ① nsw 反馈命中 → true, messages 追加 assistant+user 对, history 不动
//   ② 虚报检测命中 → true, verifyStrikes++, messages 注入核验证据
//   ③ 正常收尾     → false, history 追加 user+assistant, messages 不动
// 外加两个结构哨兵:
//   ④ source 标签("plain"/"frag")确实落到审计文件
//   ⑤ RunStream 内两处收尾调用点必须共用 finalize (防改回重复代码)
// 手法: 直接构造 turnFinalizer 并注入假 nswIntervene, 确定性, 不触外网。
// 与 cov_b3_branches_test.go 的 TestB3_VerifyClaimPlainPath / FragPath 互为交叉
// 验证: 那两条走 RunStream 全链路, 这两条直接打 finalize, 任一侧语义漂移即红。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newTurnFinalizer 构造直接单测用的 turnFinalizer (userContent 固定为哨兵值)。
func newTurnFinalizer(a *AgentRunner, messages *[]ChatMessage, vs, nr *int, nsw func(string) string) *turnFinalizer {
	return &turnFinalizer{
		agent:         a,
		messages:      messages,
		userContent:   "原始任务",
		turnStart:     time.Now(),
		verifyStrikes: vs,
		nswRounds:     nr,
		nswIntervene:  nsw,
	}
}

// ③ 正常收尾出口: 无可拦截项 → history 落两条, messages 一字不动。
func TestTurnFinalizer_NormalFinish(t *testing.T) {
	a := b3Agent(t, "http://127.0.0.1:1", nil)
	messages := []ChatMessage{{Role: "user", Content: "hi"}}
	vs, nr := 0, 0
	beforeHist := len(a.history)

	fin := newTurnFinalizer(a, &messages, &vs, &nr, func(string) string { return "" })
	if fin.finalize("这是结论。", "思考过程", "plain") {
		t.Fatal("无可拦截项时应走正常收尾 (返回 false)")
	}
	if len(messages) != 1 {
		t.Fatalf("正常收尾不应改动 messages: %d", len(messages))
	}
	if len(a.history) != beforeHist+2 {
		t.Fatalf("history 应追加 user+assistant 两条: %d -> %d", beforeHist, len(a.history))
	}
	u := a.history[len(a.history)-2]
	as := a.history[len(a.history)-1]
	if u.Role != "user" || u.Content != "原始任务" {
		t.Fatalf("user 消息不符: %+v", u)
	}
	if as.Role != "assistant" || as.Content != "这是结论。" || as.ReasoningContent != "思考过程" {
		t.Fatalf("assistant 消息不符: %+v", as)
	}
	if vs != 0 {
		t.Fatalf("正常收尾不应递增 verifyStrikes: %d", vs)
	}
}

// ① nsw 干预出口: 反馈非空 → 追加 assistant+user 对并让模型重试, history 不动。
func TestTurnFinalizer_NSWIntervene(t *testing.T) {
	a := b3Agent(t, "http://127.0.0.1:1", nil)
	messages := []ChatMessage{{Role: "user", Content: "hi"}}
	vs, nr := 0, 0
	beforeHist := len(a.history)

	fin := newTurnFinalizer(a, &messages, &vs, &nr, func(asst string) string {
		if !strings.Contains(asst, "{{1+1}}") {
			t.Errorf("nswIntervene 未收到原始 asst: %q", asst)
		}
		return "【算式求值校验】1+1 = 2"
	})
	if !fin.finalize("结果是 {{1+1}}。", "推理", "plain") {
		t.Fatal("nsw 反馈非空时应返回 true 让模型重试")
	}
	if len(messages) != 3 {
		t.Fatalf("messages 应追加 2 条 (assistant+user): %d", len(messages))
	}
	if messages[1].Role != "assistant" || messages[1].Content != "结果是 {{1+1}}。" ||
		messages[1].ReasoningContent != "推理" {
		t.Fatalf("注入的 assistant 消息不符: %+v", messages[1])
	}
	if messages[2].Role != "user" || !strings.Contains(messages[2].Content, "1+1 = 2") {
		t.Fatalf("注入的 user 反馈不符: %+v", messages[2])
	}
	if len(a.history) != beforeHist {
		t.Fatalf("拦截重试路径不应写 history: %d -> %d", beforeHist, len(a.history))
	}
	if vs != 0 {
		t.Fatalf("nsw 路径不应递增 verifyStrikes: %d", vs)
	}
}

// ② 虚报检测出口: 完成态声称+无工具证据 → 注入核验证据; 超上限后回落正常收尾。
func TestTurnFinalizer_VerifyClaimInjects(t *testing.T) {
	b3Env(t, "FORGE_VERIFY_CLAIM", "1")
	a := b3Agent(t, "http://127.0.0.1:1", nil)
	messages := []ChatMessage{{Role: "user", Content: "你提交了吗"}}
	vs, nr := 0, 0

	fin := newTurnFinalizer(a, &messages, &vs, &nr, func(string) string { return "" })
	if !fin.finalize("已提交，编译通过。", "推理", "plain") {
		t.Fatal("完成态声称+无工具证据 应触发虚报干预 (返回 true)")
	}
	if vs != 1 {
		t.Fatalf("verifyStrikes 应递增到 1: %d", vs)
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "证据先于声称") {
		t.Fatalf("注入的核验证据不符: %+v", last)
	}

	// 第二次: verifyStrikes 已达 maxVerifyStrikes → 不再拦截, 走正常收尾。
	hist := len(a.history)
	if fin.finalize("已提交，编译通过。", "推理", "plain") {
		t.Fatalf("超过 maxVerifyStrikes(%d) 后不应再拦截", maxVerifyStrikes)
	}
	if len(a.history) != hist+2 {
		t.Fatalf("回落路径应正常收尾 (history +2): %d -> %d", hist, len(a.history))
	}
	if vs != 1 {
		t.Fatalf("回落路径不应再递增 verifyStrikes: %d", vs)
	}
}

// ④ source 标签必须落到审计: 两个出口的区分只能靠它, 丢了就分不清触发来源。
func TestTurnFinalizer_SourceTagged(t *testing.T) {
	b3Env(t, "FORGE_GATE_AUDIT", "1")
	a := b3Agent(t, "http://127.0.0.1:1", nil)
	messages := []ChatMessage{{Role: "user", Content: "hi"}}
	vs, nr := 0, 0

	fin := newTurnFinalizer(a, &messages, &vs, &nr, func(string) string { return "" })
	fin.finalize("普通结论", "", "plain")
	fin.finalize("普通结论", "", "frag")

	data, err := os.ReadFile(filepath.Join(a.cfg.WorkDir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("审计文件未生成: %v", err)
	}
	body := string(data)
	for _, want := range []string{`"source":"plain"`, `"source":"frag"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("审计缺 %s; 实际内容:\n%s", want, body)
		}
	}
}

// ⑤ 结构哨兵: 两处收尾出口必须共用 finalize。
//
// 这是批3 的全部价值所在 —— 20260912 缺陷O 就是两处拷贝不同步造成的。
// 谁把内联块改回来, 此哨兵立刻红。
//
// 扫描范围是**整个包**而非 agent.go (B3 批5): 批5 把主循环迁到
// agent_stream_setup.go 的 runState.runTurns, 两处出口随之搬家。接线与
// 出口唯一性是包级属性, 硬编码文件名会在代码重组时误报 —— 功能真被删
// (全包都找不到 fin.finalize) 仍会被抓到。
func TestTurnFinalizer_TwoExitsShareOneEntry(t *testing.T) {
	src := prodGoSources(t)

	re := regexp.MustCompile(`fin\.finalize\(.*?"(\w+)"\)`)
	hits := re.FindAllStringSubmatch(src, -1)
	if len(hits) != 2 {
		t.Fatalf("应有且仅有 2 处 fin.finalize 调用点, 实际 %d —— 收尾出口可能被改回重复代码", len(hits))
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h[1]] = true
	}
	for _, want := range []string{"plain", "frag"} {
		if !got[want] {
			t.Fatalf("收尾出口缺 source=%q, 实际 %v", want, got)
		}
	}
	// nswProbeAudit 全包只应剩 1 处调用 (收归 turnFinalizer.finalize),
	// 多出来的调用点意味着收尾逻辑被改回了内联拷贝。
	if n := strings.Count(src, "nswProbeAudit(") - strings.Count(src, "func nswProbeAudit("); n != 1 {
		t.Fatalf("nswProbeAudit 应恰好 1 处调用, 实际 %d —— 收尾审计被改回重复拷贝", n)
	}
}
