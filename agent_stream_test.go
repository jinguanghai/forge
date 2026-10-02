package main

// agent_stream_test.go — B3 批1 契约测试: toolLoop 工具执行循环拆分 (agent_stream.go)。
//
// 拆分的核心风险不是"编译不过"(改错名编译器立刻红), 而是**回写遗漏**:
// toolLoop 用指针持有 RunStream 的 messages/curModel/loopStrikes/lastRawOutput,
// 若日后被改成值字段且忘记回写, 编译器不会报错 —— 而 messages 漏回写会静默
// 丢弃工具结果(模型收不到 tool 响应 → 行为诡异但测试可能仍绿)。
// 因此本文件的中心断言是"指针直写契约": 循环内的修改必须原样反映到外部变量。
//
// 用例全部不触网: 只走前置守卫(工具名/参数/重复检测)与中止判定, 不进入实际执行。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestToolLoop 构造工具循环夹具。
// 返回的 msgs/strikes/cur/last 是 RunStream 侧局部变量的替身。
func newTestToolLoop(a *AgentRunner) (*toolLoop, *[]ChatMessage, *int, *string, *string) {
	msgs := []ChatMessage{}
	strikes := 0
	cur := a.cfg.Model
	last := ""
	tl := &toolLoop{
		agent:          a,
		runCtx:         context.Background(),
		callHistory:    map[string]int{},
		userContent:    "测试输入",
		turn:           0,
		taskAnchor:     "测试锚点",
		maxLoopStrikes: 5,
		escalateOnLoop: func() {},
		finishStuck:    func() error { return nil },
		messages:       &msgs,
		curModel:       &cur,
		loopStrikes:    &strikes,
		lastRawOutput:  &last,
	}
	return tl, &msgs, &strikes, &cur, &last
}

// b3IdleAgent 构造一个不触网的 AgentRunner (空 httptest 端点)。
func b3IdleAgent(t *testing.T, tweak func(*Config)) *AgentRunner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	return b3Agent(t, srv.URL, tweak)
}

func b3tc(id, name, args string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}}
}

// TestToolLoop_EmptyCalls: 本轮无工具调用 → 直接返回, 不产生消息。
func TestToolLoop_EmptyCalls(t *testing.T) {
	a := b3IdleAgent(t, nil)
	tl, msgs, _, _, _ := newTestToolLoop(a)
	if err := tl.runToolCalls(nil); err != nil {
		t.Fatalf("空输入应返回 nil, 实际 %v", err)
	}
	if len(*msgs) != 0 {
		t.Fatalf("空输入不应产生消息, 实际 %d 条", len(*msgs))
	}
}

// TestToolLoop_UnknownTool: 未知工具名 → 追加 tool 响应 + 计数递增, 单次不中止。
func TestToolLoop_UnknownTool(t *testing.T) {
	a := b3IdleAgent(t, nil)
	tl, msgs, _, _, _ := newTestToolLoop(a)
	if err := tl.runToolCalls([]ToolCall{b3tc("c1", "bash", "{}")}); err != nil {
		t.Fatalf("单次未知工具不应中止: %v", err)
	}
	if tl.unknownTools != 1 {
		t.Fatalf("unknownTools 应为 1, 实际 %d", tl.unknownTools)
	}
	if len(*msgs) != 1 || (*msgs)[0].Role != "tool" || (*msgs)[0].ToolCallID != "c1" {
		t.Fatalf("tool 响应消息不匹配: %+v", *msgs)
	}
	if !strings.Contains((*msgs)[0].Content, "未知工具: bash") {
		t.Fatalf("消息未回显工具名: %q", (*msgs)[0].Content)
	}
}

// TestToolLoop_ParseError: 参数 JSON 非法 → 追加提示消息, 不计失败(格式问题非逻辑失败)。
func TestToolLoop_ParseError(t *testing.T) {
	a := b3IdleAgent(t, nil)
	tl, msgs, _, _, _ := newTestToolLoop(a)
	if err := tl.runToolCalls([]ToolCall{b3tc("c2", ForgeToolName, "not-json")}); err != nil {
		t.Fatalf("参数解析失败不应中止: %v", err)
	}
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0].Content, "参数解析失败") {
		t.Fatalf("期望参数解析失败提示, 实际: %+v", *msgs)
	}
	if tl.consecutiveFails != 0 || tl.unknownTools != 0 {
		t.Fatalf("JSON 格式问题不应计分: fails=%d unknown=%d", tl.consecutiveFails, tl.unknownTools)
	}
}

// TestToolLoop_MultipleCallsAccumulate: 多调用累积 —— 指针直写契约的核心断言。
// 若 messages 被改回值字段且漏回写, 此处立刻红。
func TestToolLoop_MultipleCallsAccumulate(t *testing.T) {
	a := b3IdleAgent(t, nil)
	tl, msgs, _, _, _ := newTestToolLoop(a)
	err := tl.runToolCalls([]ToolCall{
		b3tc("a1", "bad1", "{}"),
		b3tc("a2", "bad2", "{}"),
		b3tc("a3", ForgeToolName, "{{{"),
	})
	if err != nil {
		t.Fatalf("守卫命中不应中止: %v", err)
	}
	if len(*msgs) != 3 {
		t.Fatalf("应累积 3 条 tool 响应, 实际 %d: %+v", len(*msgs), *msgs)
	}
}

// TestToolLoop_RepeatBlocked: 同一语义哈希超阈值 → 拦截消息 + loopStrikes 指针回写。
func TestToolLoop_RepeatBlocked(t *testing.T) {
	a := b3IdleAgent(t, nil)
	tl, msgs, strikes, _, _ := newTestToolLoop(a)
	tl.callHistory[hashCall("print(1)", "python", "")] = maxRepeatedCalls
	args := `{"action":"run","code":"print(1)","lang":"python"}`
	if err := tl.runToolCalls([]ToolCall{b3tc("c3", ForgeToolName, args)}); err != nil {
		t.Fatalf("未达 maxLoopStrikes 不应中止: %v", err)
	}
	if *strikes != 1 {
		t.Fatalf("loopStrikes 应经指针回写为 1, 实际 %d", *strikes)
	}
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0].Content, "重复调用检测") {
		t.Fatalf("期望重复调用拦截消息, 实际: %+v", *msgs)
	}
}

// TestToolLoop_FinishStuckOnMaxStrikes: 拦截次数达 maxLoopStrikes → 走 finishStuck 收尾。
func TestToolLoop_FinishStuckOnMaxStrikes(t *testing.T) {
	a := b3IdleAgent(t, nil)
	tl, _, strikes, _, _ := newTestToolLoop(a)
	tl.maxLoopStrikes = 1
	called := false
	tl.finishStuck = func() error { called = true; return nil }
	tl.callHistory[hashCall("print(1)", "python", "")] = maxRepeatedCalls
	args := `{"action":"run","code":"print(1)","lang":"python"}`
	if err := tl.runToolCalls([]ToolCall{b3tc("c4", ForgeToolName, args)}); err != nil {
		t.Fatalf("finishStuck 返回 nil 时不应报错: %v", err)
	}
	if !called {
		t.Fatal("达 maxLoopStrikes 应调用 finishStuck 收尾")
	}
	if *strikes != 1 {
		t.Fatalf("loopStrikes 应为 1, 实际 %d", *strikes)
	}
}

// TestToolLoop_AbortOnUnknownTools: 连续未知工具达阈值 → 返回错误中止整个 RunStream。
func TestToolLoop_AbortOnUnknownTools(t *testing.T) {
	a := b3IdleAgent(t, func(c *Config) { c.MaxConsecutiveFails = 1 })
	tl, _, _, _, _ := newTestToolLoop(a)
	err := tl.runToolCalls([]ToolCall{b3tc("c5", "nope", "{}")})
	if err == nil || !strings.Contains(err.Error(), "未知工具") {
		t.Fatalf("达阈值应中止并报未知工具, 实际: %v", err)
	}
}
