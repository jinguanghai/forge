package main

import (
	"context"
	"errors"
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

// ---- parseSSE: 工具轮次 (finish_reason=tool_calls) 也延迟收尾读 usage ----
func TestParseSSE_ToolCallsUsageRecorded(t *testing.T) {
	oldPath := cacheStatPath
	tmp := filepath.Join(t.TempDir(), "cache_stats.jsonl")
	cacheStatPath = tmp
	defer func() { cacheStatPath = oldPath }()

	body := sseLine(`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"forge","arguments":"{}"}}]},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`) +
		sseLine(`{"choices":[],"usage":{"prompt_tokens":200,"completion_tokens":5,"total_tokens":205,"prompt_cache_hit_tokens":180,"prompt_cache_miss_tokens":20}}`) +
		"data: [DONE]\n\n"
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "cache-tool-model")
	if err != nil {
		t.Fatalf("parseSSE error: %v", err)
	}
	events := drainEvents(ch)
	// 事件序列: [tool_call_delta, tool_call_done]; 收尾阶段 (usage/[DONE])
	// 被消费但不发多余事件 (无人消费) —— 不能出现第三个 "done" 事件。
	if len(events) != 2 || events[1].Type != "tool_call_done" {
		t.Fatalf("events = %v, want [tool_call_delta tool_call_done]", typesOf(events))
	}
	data, rerr := os.ReadFile(tmp)
	if rerr != nil {
		t.Fatalf("cache stats not written: %v", rerr)
	}
	if !strings.Contains(string(data), `"hit":180`) || !strings.Contains(string(data), `"miss":20`) {
		t.Fatalf("cache stat content = %s", string(data))
	}
}

// ---- 请求体字节级确定性 (前缀缓存铁律守卫) ----
// 相同 messages 的两次请求, 序列化后的 body 必须逐字节相同 —— 任何随机字段
// (如消息级 id) 都会在首个消息处断裂 DeepSeek 前缀缓存。
func TestDoStream_DeterministicBody(t *testing.T) {
	saveGlobals(t)
	var mu sync.Mutex
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)
	msgs := []ChatMessage{
		{Role: "system", Content: "SYS"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "forge", Arguments: `{"lang":"python"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "42"},
	}
	for i := 0; i < 2; i++ {
		ch := make(chan StreamEvent, 16)
		if err := c.doStream(context.Background(), msgs, nil, ch, "test-model"); err != nil {
			t.Fatalf("doStream: %v", err)
		}
		drainEvents(ch)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want 2", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("请求体非确定性 —— 前缀缓存将被首个差异处断裂:\n%s\nvs\n%s", bodies[0], bodies[1])
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
