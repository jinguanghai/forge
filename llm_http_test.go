package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ==== 建议3: LLM HTTP 传输层 + SSE 解析测试 ====

func testLLMClient() *LLMClient {
	return NewLLMClient(&Config{
		APIKey:         "test-key",
		BaseURL:        "http://localhost:9999/v1",
		Model:          "test-model",
		MaxTokens:      2048,
		Temperature:    0.7,
		TopP:           1.0,
		RequestTimeout: 10 * time.Second,
	})
}

func drainEvents(ch chan StreamEvent) []StreamEvent {
	var events []StreamEvent
	for {
		select {
		case ev := <-ch:
			events = append(events, ev)
		default:
			return events
		}
	}
}

func sseLine(payload string) string { return "data: " + payload + "\n" }

func typesOf(events []StreamEvent) []string {
	types := make([]string, 0, len(events))
	for _, ev := range events {
		types = append(types, ev.Type)
	}
	return types
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- parseSSE: 普通文本流 ----
func TestParseSSE_PlainContent(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"content":"你好"},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"content":"，世界"},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	if got, want := typesOf(events), []string{"content", "content", "done"}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if events[0].Content != "你好" || events[1].Content != "，世界" {
		t.Fatalf("content mismatch: %+v", events)
	}
}

// ---- parseSSE: [DONE] 标记 ----
func TestParseSSE_DoneMarker(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"content":"x"},"finish_reason":null}]}`) +
		"data: [DONE]\n\n"
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	if got, want := typesOf(events), []string{"content", "done"}; !equalStrings(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
}

// ---- parseSSE: 工具调用分片 -> 合并 (finish_reason=tool_calls) ----
func TestParseSSE_ToolCallsMerged(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"forge","arguments":""}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"lang\":"}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"python\"}"}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	ch := make(chan StreamEvent, 64)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	got := typesOf(events)
	if len(got) != 4 || got[0] != "tool_call_delta" || got[3] != "tool_call_done" {
		t.Fatalf("event types = %v", got)
	}
	done := events[3]
	if len(done.ToolCalls) != 1 {
		t.Fatalf("merged tool calls = %d, want 1", len(done.ToolCalls))
	}
	tc := done.ToolCalls[0]
	if tc.ID != "call_1" || tc.Function.Name != "forge" {
		t.Fatalf("merged call = %+v", tc)
	}
	if tc.Function.Arguments != `{"lang":"python"}` {
		t.Fatalf("arguments = %q, want merged", tc.Function.Arguments)
	}
}

// ---- parseSSE: DeepSeek 怪癖 — tool delta 后 stop 也要合并 ----
func TestParseSSE_ToolCallsStopQuirk(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"fmt","arguments":"{a:"}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 64)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	got := typesOf(events)
	if len(got) != 3 || got[2] != "tool_call_done" {
		t.Fatalf("event types = %v, want [tool_call_delta tool_call_delta tool_call_done]", got)
	}
	done := events[2]
	if len(done.ToolCalls) != 1 || done.ToolCalls[0].ID != "call_9" {
		t.Fatalf("merged = %+v", done.ToolCalls)
	}
	if done.ToolCalls[0].Function.Arguments != "{a:1}" {
		t.Fatalf("args = %q", done.ToolCalls[0].Function.Arguments)
	}
}

// ---- parseSSE: 多工具调用全 index=0 -> 按 ID 分组 ----
func TestParseSSE_ToolCallsAllIndexZeroMultiID(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a1","function":{"name":"f1","arguments":""}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"b2","function":{"name":"f2","arguments":""}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)
	ch := make(chan StreamEvent, 64)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	done := events[len(events)-1]
	if done.Type != "tool_call_done" || len(done.ToolCalls) != 2 {
		t.Fatalf("done = %+v", done)
	}
	if done.ToolCalls[0].Function.Name != "f1" || done.ToolCalls[1].Function.Name != "f2" {
		t.Fatalf("order = %v", done.ToolCalls)
	}
}

// ---- parseSSE: usage 块触发缓存统计写入 ----
func TestParseSSE_UsageRecordsCacheStat(t *testing.T) {
	oldPath := cacheStatPath
	tmp := filepath.Join(t.TempDir(), "cache_stats.jsonl")
	cacheStatPath = tmp
	defer func() { cacheStatPath = oldPath }()

	body := sseLine(`{"choices":[],"usage":{"prompt_tokens":150,"completion_tokens":10,"total_tokens":160,"prompt_cache_hit_tokens":100,"prompt_cache_miss_tokens":50}}`) +
		sseLine(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "cache-test-model")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	data, rerr := os.ReadFile(tmp)
	if rerr != nil {
		t.Fatalf("cache stats not written: %v", rerr)
	}
	if !strings.Contains(string(data), `"hit":100`) || !strings.Contains(string(data), `"miss":50`) {
		t.Fatalf("cache stat content = %s", string(data))
	}
	if !strings.Contains(string(data), `"model":"cache-test-model"`) {
		t.Fatalf("model not recorded: %s", string(data))
	}
}

// ---- parseSSE: API 错误块 -> LLMError ----
func TestParseSSE_APIError(t *testing.T) {
	body := sseLine(`{"error":{"message":"bad api key","type":"auth","code":"401"}}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	var le *LLMError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want *LLMError", err)
	}
	if le.Message != "bad api key" {
		t.Fatalf("message = %q", le.Message)
	}
}

// ---- parseSSE: content_filter 终止 ----
func TestParseSSE_ContentFilter(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{},"finish_reason":"content_filter"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	var le *LLMError
	if !errors.As(err, &le) || le.Type != "content_filter" {
		t.Fatalf("err = %v, want content_filter LLMError", err)
	}
}

// ---- parseSSE: 畸形 JSON 行跳过, 不 panic ----
func TestParseSSE_MalformedJSONSkipped(t *testing.T) {
	body := sseLine(`{not valid json`) +
		sseLine(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	if got, want := typesOf(events), []string{"content", "done"}; !equalStrings(got, want) {
		t.Fatalf("event types = %v", got)
	}
}

// ---- parseSSE: 噪音行(注释/空行/非data)跳过 ----
func TestParseSSE_SkipNoise(t *testing.T) {
	body := ": keep-alive comment\n" +
		"\n" +
		sseLine(`{"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`) +
		"random line\n" +
		sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	if got, want := typesOf(events), []string{"content", "done"}; !equalStrings(got, want) {
		t.Fatalf("event types = %v", got)
	}
}

// ---- parseSSE: reasoning_content 事件 ----
func TestParseSSE_ReasoningContent(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"reasoning_content":"让我想想"},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"content":"答案"},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	if len(events) != 3 || events[0].Type != "reasoning" || events[0].Content != "让我想想" {
		t.Fatalf("events = %+v", events)
	}
}

// stagedReader: 分两段供数据, 段1读完在 gate 上阻塞, gate 关闭后才给段2。
// 用于构造"已发事件后仍阻塞等待下一行"的 SSE 流, 消除内存 reader 的竞态。
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

func TestDoStream_Success(t *testing.T) {
	saveGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`))
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ch := make(chan StreamEvent, 32)
	err := c.doStream(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil, ch, "test-model")
	if err != nil {
		t.Fatalf("doStream error: %v", err)
	}
	events := drainEvents(ch)
	if got, want := typesOf(events), []string{"content", "done"}; !equalStrings(got, want) {
		t.Fatalf("events = %v", got)
	}
}

func TestDoStream_HTTP400JSONError(t *testing.T) {
	saveGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"invalid api key","type":"auth","code":"40101"}}`)
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ch := make(chan StreamEvent, 32)
	err := c.doStream(context.Background(), nil, nil, ch, "m")
	var le *LLMError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v, want *LLMError", err)
	}
	if le.StatusCode != 400 || le.Message != "invalid api key (40101)" {
		t.Fatalf("LLMError = %+v", le)
	}
}

func TestDoStream_HTTP429(t *testing.T) {
	saveGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit"}}`)
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ch := make(chan StreamEvent, 32)
	err := c.doStream(context.Background(), nil, nil, ch, "m")
	var le *LLMError
	if !errors.As(err, &le) || le.StatusCode != 429 {
		t.Fatalf("err = %v, want 429 LLMError", err)
	}
}

func TestDoStream_PlainBodyKept(t *testing.T) {
	saveGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "Internal Server Error")
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ch := make(chan StreamEvent, 32)
	err := c.doStream(context.Background(), nil, nil, ch, "m")
	var le *LLMError
	if !errors.As(err, &le) || le.Body != "Internal Server Error" {
		t.Fatalf("err = %v", err)
	}
}

func TestDoStream_NetworkError(t *testing.T) {
	saveGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()
	cfg := &Config{APIKey: "k", BaseURL: url, Model: "m", RequestTimeout: 2 * time.Second}
	c := NewLLMClient(cfg)
	ch := make(chan StreamEvent, 32)
	err := c.doStream(context.Background(), nil, nil, ch, "m")
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
	if !strings.Contains(err.Error(), "http request") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoStream_RequestShape(t *testing.T) {
	saveGlobals(t)
	type capture struct {
		model  string
		stream bool
		msgs   int
		auth   string
		ct     string
		maxTok int
		temp   float64
		usage  bool
		think  bool
	}
	got := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		json.NewDecoder(r.Body).Decode(&req)
		got <- capture{
			model:  req.Model,
			stream: req.Stream,
			msgs:   len(req.Messages),
			auth:   r.Header.Get("Authorization"),
			ct:     r.Header.Get("Content-Type"),
			maxTok: req.MaxTokens,
			temp:   req.Temperature,
			usage:  req.StreamOptions != nil && req.StreamOptions.IncludeUsage,
			think:  req.Thinking != nil,
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	cfg := &Config{
		APIKey: "shape-key", BaseURL: srv.URL, Model: "shape-model",
		MaxTokens: 512, Temperature: 0.3, TopP: 0.9,
		RequestTimeout: 10 * time.Second, ShowReasoning: true,
	}
	c := NewLLMClient(cfg)
	ch := make(chan StreamEvent, 32)
	msgs := []ChatMessage{{Role: "user", Content: "hello"}, {Role: "user", Content: "again"}}
	err := c.doStream(context.Background(), msgs, nil, ch, "shape-model")
	if err != nil {
		t.Fatalf("doStream error: %v", err)
	}
	cap := <-got
	if cap.model != "shape-model" || !cap.stream || cap.msgs != 2 {
		t.Fatalf("capture = %+v", cap)
	}
	if cap.auth != "Bearer shape-key" || !strings.Contains(cap.ct, "application/json") {
		t.Fatalf("headers = auth:%q ct:%q", cap.auth, cap.ct)
	}
	if cap.maxTok != 512 || cap.temp != 0.3 {
		t.Fatalf("params = %+v", cap)
	}
	if !cap.usage {
		t.Fatal("stream_options.include_usage not set")
	}
	if !cap.think {
		t.Fatal("thinking config not set when ShowReasoning=true")
	}
	if len(msgs) != 2 || msgs[0].Content != "hello" {
		t.Fatal("input messages mutated")
	}
}

// ==== ChatCompletionStream 接口层 ====

func TestChatCompletionStream_Full(t *testing.T) {
	saveGlobals(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"a"},"finish_reason":null}]}`))
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"b"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ch := c.ChatCompletionStream(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}, nil, "m")
	var content strings.Builder
	closed := false
	for ev := range ch {
		switch ev.Type {
		case "content":
			content.WriteString(ev.Content)
		case "error":
			t.Fatalf("stream error: %v", ev.Error)
		case "done":
			closed = true
		}
	}
	if !closed {
		t.Fatal("no done event")
	}
	if content.String() != "ab" {
		t.Fatalf("content = %q", content.String())
	}
}

func TestChatCompletionStream_RetryOn500(t *testing.T) {
	saveGlobals(t)
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			w.WriteHeader(500)
			io.WriteString(w, "boom")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"recovered"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ch := c.ChatCompletionStream(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil, "m")
	var got string
	for ev := range ch {
		if ev.Type == "content" {
			got = ev.Content
		}
		if ev.Type == "error" {
			t.Fatalf("stream error: %v", ev.Error)
		}
	}
	if got != "recovered" {
		t.Fatalf("content = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("calls = %d, want retry after 500", calls)
	}
}

func TestChatCompletionStream_NonRetryableError(t *testing.T) {
	saveGlobals(t)
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"bad request","type":"invalid_request_error"}}`)
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	ch := c.ChatCompletionStream(context.Background(), nil, nil, "m")
	sawError := false
	for ev := range ch {
		if ev.Type == "error" {
			sawError = true
			if !strings.Contains(ev.Error.Error(), "bad request") {
				t.Fatalf("error = %v", ev.Error)
			}
		}
	}
	if !sawError {
		t.Fatal("expected error event for 400")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (400 not retryable)", calls)
	}
}

func TestLLMClient_HealthyAndShutdown(t *testing.T) {
	c := testLLMClient()
	if !c.IsHealthy() {
		t.Fatal("new client should be healthy")
	}
	c.Shutdown()
	if !c.IsHealthy() {
		t.Fatal("shutdown should not mark unhealthy")
	}
}
