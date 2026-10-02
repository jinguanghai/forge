package main

// llm_merge_test.go — tool_call 合并 / 可重试判定 / 消息清洗 / SSE 取消路径。
// 20260927 自 llm_http_test.go 拆出 (该文件 988 行超 F2 上限 500)。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type stagedReader struct {
	stage1 []byte
	stage2 []byte
	gate   chan struct{}
	mu     sync.Mutex
	sent2  bool
}

func (r *stagedReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.stage1) > 0 {
		n := copy(p, r.stage1)
		r.stage1 = r.stage1[n:]
		return n, nil
	}
	if !r.sent2 {
		<-r.gate
		r.sent2 = true
	}
	if len(r.stage2) > 0 {
		n := copy(p, r.stage2)
		r.stage2 = r.stage2[n:]
		return n, nil
	}
	return 0, io.EOF
}

// ---- parseSSE: 已发事件后被取消 -> errPartialStream ----
func TestParseSSE_CancelAfterEvents(t *testing.T) {
	gate := make(chan struct{})
	reader := &stagedReader{
		stage1: []byte(sseLine(`{"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`)),
		stage2: []byte(sseLine(`{"choices":[{"delta":{},"finish_reason":null}]}`) + "extra\n"),
		gate:   gate,
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan StreamEvent, 16)
	errCh := make(chan error, 1)
	go func() {
		errCh <- testLLMClient().parseSSE(ctx, reader, ch, "m")
	}()
	select {
	case ev := <-ch:
		if ev.Type != "content" {
			t.Fatalf("first event = %s", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting first event")
	}
	cancel()
	close(gate)
	select {
	case err := <-errCh:
		if !errors.Is(err, errPartialStream) {
			t.Fatalf("err = %v, want errPartialStream", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting parseSSE return")
	}
}

// ---- parseSSE: 未发事件即取消 -> ctx.Err ----
func TestParseSSE_CancelNoEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan StreamEvent, 16)
	err := testLLMClient().parseSSE(ctx, strings.NewReader("data: x\n\n"), ch, "m")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// ==== mergeToolCalls ====

func tc(index int, id, name, args string) ToolCall {
	return ToolCall{Index: index, ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}}
}

func TestMergeToolCalls_Empty(t *testing.T) {
	if got := mergeToolCalls(nil); got != nil {
		t.Fatalf("nil -> %v, want nil", got)
	}
	if got := mergeToolCalls([]ToolCall{}); got != nil {
		t.Fatalf("empty -> %v, want nil", got)
	}
}

func TestMergeToolCalls_SingleCallByIndex(t *testing.T) {
	merged := mergeToolCalls([]ToolCall{
		tc(0, "call_1", "forge", `{"lang":`),
		tc(0, "", "", `"python"}`),
	})
	if len(merged) != 1 || merged[0].ID != "call_1" || merged[0].Function.Name != "forge" {
		t.Fatalf("merged = %+v", merged)
	}
	if merged[0].Function.Arguments != `{"lang":"python"}` {
		t.Fatalf("args = %q", merged[0].Function.Arguments)
	}
}

func TestMergeToolCalls_MultiCallByIndex(t *testing.T) {
	merged := mergeToolCalls([]ToolCall{
		tc(0, "call_a", "f1", "{}"),
		tc(1, "call_b", "f2", "{}"),
	})
	if len(merged) != 2 {
		t.Fatalf("len = %d", len(merged))
	}
	if merged[0].Function.Name != "f1" || merged[1].Function.Name != "f2" {
		t.Fatalf("order = %v", merged)
	}
}

func TestMergeToolCalls_AllIndexZeroByID(t *testing.T) {
	merged := mergeToolCalls([]ToolCall{
		tc(0, "a", "f1", `{"x":`),
		tc(0, "b", "f2", "{}"),
		tc(0, "a", "", `1}`),
	})
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2 (grouped by ID)", len(merged))
	}
	if merged[0].Function.Name != "f1" || merged[0].Function.Arguments != `{"x":1}` {
		t.Fatalf("first = %+v", merged[0])
	}
	if merged[1].Function.Name != "f2" {
		t.Fatalf("second = %+v", merged[1])
	}
}

func TestMergeToolCalls_IndexGapsSkipped(t *testing.T) {
	merged := mergeToolCalls([]ToolCall{
		tc(0, "a", "f1", "{}"),
		tc(2, "c", "f3", "{}"),
	})
	if len(merged) != 2 {
		t.Fatalf("len = %d, want 2 (gap skipped)", len(merged))
	}
	if merged[1].Index != 2 || merged[1].Function.Name != "f3" {
		t.Fatalf("second = %+v", merged[1])
	}
}

func TestMergeToolCalls_MissingIDGetsFallback(t *testing.T) {
	merged := mergeToolCalls([]ToolCall{
		{Index: 0, Type: "", Function: FunctionCall{Name: "f1", Arguments: "{}"}},
	})
	if len(merged) != 1 {
		t.Fatalf("len = %d", len(merged))
	}
	if merged[0].ID == "" {
		t.Fatal("fallback ID not generated")
	}
	if merged[0].Type != "function" {
		t.Fatalf("type = %q, want default function", merged[0].Type)
	}
}

// ==== isRetryable ====

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"partial stream", errPartialStream, false},
		{"nil", nil, false},
		{"429", &LLMError{StatusCode: 429}, true},
		{"500", &LLMError{StatusCode: 500}, true},
		{"503", &LLMError{StatusCode: 503}, true},
		{"400", &LLMError{StatusCode: 400}, false},
		{"network", errors.New("connection refused"), true},
		{"wrapped 429", fmt.Errorf("wrap: %w", &LLMError{StatusCode: 429}), true},
	}
	for _, c := range cases {
		if got := isRetryable(c.err); got != c.want {
			t.Errorf("%s: isRetryable = %v, want %v", c.name, got, c.want)
		}
	}
}

// ==== sanitizeMessages ====

func TestSanitizeMessages_Nil(t *testing.T) {
	if got := sanitizeMessages(nil); got != nil {
		t.Fatalf("nil -> %v", got)
	}
}

func TestSanitizeMessages_OrphanToolRemoved(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCallID: "orphan_1", Content: "res"},
	}
	out := sanitizeMessages(in)
	if len(out) != 1 || out[0].Role != "user" {
		t.Fatalf("out = %+v, want orphan tool removed", out)
	}
}

func TestSanitizeMessages_PlaceholderInjected(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}}, Content: ""},
	}
	out := sanitizeMessages(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3 (placeholder injected)", len(out))
	}
	tool := out[2]
	if tool.Role != "tool" || tool.ToolCallID != "c1" {
		t.Fatalf("placeholder = %+v", tool)
	}
	if !strings.Contains(tool.Content, "unavailable") {
		t.Fatalf("placeholder content = %q", tool.Content)
	}
}

func TestSanitizeMessages_EmptyAssistantFixed(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "", ReasoningContent: "推理过程"},
	}
	out := sanitizeMessages(in)
	if out[1].Content != "推理过程" {
		t.Fatalf("assistant content = %q, want reasoning fallback", out[1].Content)
	}
	in2 := []ChatMessage{{Role: "assistant", Content: ""}}
	out2 := sanitizeMessages(in2)
	if out2[0].Content != "[empty response]" {
		t.Fatalf("assistant content = %q, want [empty response]", out2[0].Content)
	}
}

func TestSanitizeMessages_DeepCopy(t *testing.T) {
	in := []ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	out := sanitizeMessages(in)
	out[0].ToolCalls[0].ID = "mutated"
	out[0].ToolCalls[0].Function.Name = "hacked"
	if in[0].ToolCalls[0].ID != "c1" || in[0].ToolCalls[0].Function.Name != "f" {
		t.Fatal("input mutated — deep copy failed")
	}
}

func TestSanitizeMessages_ConsistentPairKept(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "run"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "keep_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "keep_1", Content: "result"},
	}
	out := sanitizeMessages(in)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if out[2].Role != "tool" || out[2].ToolCallID != "keep_1" {
		t.Fatalf("tool msg = %+v", out[2])
	}
}

// ==== doStream (httptest) ====

func saveGlobals(t *testing.T) {
	t.Helper()
	oldSys := sysChanged
	oldCur := currentSystemHash
	oldLast := lastSystemHash
	t.Cleanup(func() {
		sysChanged = oldSys
		currentSystemHash = oldCur
		lastSystemHash = oldLast
	})
}

func newClientWithServer(t *testing.T, srv *httptest.Server) *LLMClient {
	t.Helper()
	cfg := &Config{
		APIKey:         "test-key",
		BaseURL:        srv.URL,
		Model:          "test-model",
		MaxTokens:      2048,
		Temperature:    0.7,
		TopP:           1.0,
		RequestTimeout: 10 * time.Second,
	}
	return NewLLMClient(cfg)
}
