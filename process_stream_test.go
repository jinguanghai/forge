package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func newTestAgent() *AgentRunner {
	return &AgentRunner{stats: &SessionStats{StartTime: time.Now()}}
}

// ─── processStream: 假事件流测试 (无需真实 API) ──────────────

func TestProcessStream_ContentOnly(t *testing.T) {
	a := newTestAgent()
	ch := make(chan StreamEvent, 3)
	ch <- StreamEvent{Type: "content", Content: "你好"}
	ch <- StreamEvent{Type: "content", Content: "世界"}
	ch <- StreamEvent{Type: "done"}
	close(ch)

	var content, reasoning strings.Builder
	var calls []ToolCall
	err := a.processStream(ch, &content, &reasoning, &calls, false, nil)
	if err != nil {
		t.Fatalf("processStream: %v", err)
	}
	if content.String() != "你好世界" {
		t.Errorf("content = %q, want 你好世界", content.String())
	}
	if reasoning.Len() != 0 {
		t.Errorf("reasoning should be empty, got %q", reasoning.String())
	}
	if a.stats.TotalTokens == 0 {
		t.Error("token count not recorded")
	}
}

func TestProcessStream_ReasoningAndContent(t *testing.T) {
	a := newTestAgent()
	ch := make(chan StreamEvent, 4)
	ch <- StreamEvent{Type: "reasoning", Content: "思考中"}
	ch <- StreamEvent{Type: "content", Content: "答案"}
	ch <- StreamEvent{Type: "done"}
	close(ch)

	var content, reasoning strings.Builder
	var calls []ToolCall
	err := a.processStream(ch, &content, &reasoning, &calls, false, nil)
	if err != nil {
		t.Fatalf("processStream: %v", err)
	}
	if reasoning.String() != "思考中" {
		t.Errorf("reasoning = %q", reasoning.String())
	}
	if content.String() != "答案" {
		t.Errorf("content = %q", content.String())
	}
}

func TestProcessStream_Error(t *testing.T) {
	a := newTestAgent()
	sentinel := errors.New("stream boom")
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: "error", Error: sentinel}
	close(ch)

	var content, reasoning strings.Builder
	var calls []ToolCall
	err := a.processStream(ch, &content, &reasoning, &calls, false, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}

func TestProcessStream_ToolCallDeltaThenDone(t *testing.T) {
	a := newTestAgent()
	ch := make(chan StreamEvent, 3)
	// 流式增量片段
	ch <- StreamEvent{Type: "tool_call_delta", ToolCalls: []ToolCall{
		{ID: "call_1", Function: FunctionCall{Name: "forge", Arguments: `{"action"`}},
	}}
	ch <- StreamEvent{Type: "tool_call_delta", ToolCalls: []ToolCall{
		{ID: "call_1", Function: FunctionCall{Name: "forge", Arguments: `:"运行"}`}},
	}}
	// 合并后的最终调用 (done 事件替换全部增量)
	ch <- StreamEvent{Type: "tool_call_done", ToolCalls: []ToolCall{
		{ID: "call_1", Function: FunctionCall{Name: "forge", Arguments: `{"action":"运行"}`}},
	}}
	close(ch)

	var content, reasoning strings.Builder
	calls := []ToolCall{}
	err := a.processStream(ch, &content, &reasoning, &calls, false, nil)
	if err != nil {
		t.Fatalf("processStream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v, want 1 merged call", calls)
	}
	if calls[0].ID != "call_1" || calls[0].Function.Arguments != `{"action":"运行"}` {
		t.Errorf("merged call = %+v", calls[0])
	}
}

func TestProcessStream_ClosedChannel(t *testing.T) {
	a := newTestAgent()
	ch := make(chan StreamEvent)
	close(ch)
	var content, reasoning strings.Builder
	calls := []ToolCall{}
	err := a.processStream(ch, &content, &reasoning, &calls, false, nil)
	if err != nil {
		t.Fatalf("closed channel should return nil, got %v", err)
	}
}

func TestProcessStream_OnFirstEventCalled(t *testing.T) {
	a := newTestAgent()
	called := false
	ch := make(chan StreamEvent, 2)
	ch <- StreamEvent{Type: "content", Content: "x"}
	ch <- StreamEvent{Type: "done"}
	close(ch)

	var content, reasoning strings.Builder
	calls := []ToolCall{}
	err := a.processStream(ch, &content, &reasoning, &calls, false, func() { called = true })
	if err != nil {
		t.Fatalf("processStream: %v", err)
	}
	if !called {
		t.Error("onFirstEvent callback not invoked")
	}
}

// ─── trimTurnMessages: 工具回合裁剪 ─────────────────────────

func TestTrimTurnMessages_KeepsToolPairing(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "forge"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "out1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c2", Function: FunctionCall{Name: "forge"}}}},
		{Role: "tool", ToolCallID: "c2", Content: "out2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "final"},
	}
	got := trimTurnMessages(msgs, 5)
	// 两轮工具回合被裁剪, 剩 [sys,u1,u2,u3,final]? 不对:
	// 第一轮删 asst(c1)+tool(c1) → 7 条; 第二轮删 asst(c2)+tool(c2) → 5 条
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5: %+v", len(got), got)
	}
	if got[0].Role != "system" {
		t.Errorf("first = %+v", got[0])
	}
	last := got[len(got)-1]
	if last.Role != "assistant" || last.Content != "final" {
		t.Errorf("last = %+v", last)
	}
	// 不允许残留孤立 tool 消息
	for _, m := range got {
		if m.Role == "tool" {
			t.Errorf("orphan tool message: %+v", m)
		}
	}
}

func TestTrimTurnMessages_NoToolCalls_Untouched(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	got := trimTurnMessages(msgs, 5)
	if len(got) != 3 {
		t.Fatalf("should be untouched, got %d", len(got))
	}
}

func TestTrimTurnMessages_SystemPreserved(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "forge"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "out"},
	}
	got := trimTurnMessages(msgs, 2)
	if got[0].Role != "system" {
		t.Errorf("system must be preserved, got %+v", got[0])
	}
}

// ─── 辅助函数 ───────────────────────────────────────────────

func TestParseForgeParams(t *testing.T) {
	p, err := parseForgeParams(`{"action":"运行","code":"print(1)","lang":"python","input":"x"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Action != "运行" || p.Code != "print(1)" || p.Lang != "python" || p.Input != "x" {
		t.Errorf("params = %+v", p)
	}
	if _, err := parseForgeParams(`{bad json`); err == nil {
		t.Error("invalid JSON should error")
	}
	if _, err := parseForgeParams(``); err == nil {
		t.Error("empty JSON should error")
	}
}

func TestHashCall(t *testing.T) {
	h1 := hashCall("print(1)", "python", "")
	h2 := hashCall("print(1)", "python", "")
	h3 := hashCall("print(1)", "python", "input")
	if h1 != h2 {
		t.Error("hash not deterministic")
	}
	if h1 == h3 {
		t.Error("different input should differ")
	}
	if len(h1) != 64 {
		t.Errorf("len = %d, want 64 (sha256 hex)", len(h1))
	}
}

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0B"}, {1023, "1023B"}, {1024, "1.0KB"},
		{2048, "2.0KB"}, {5 * 1024 * 1024, "5.0MB"},
	}
	for _, c := range cases {
		if got := formatSize(c.in); got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
