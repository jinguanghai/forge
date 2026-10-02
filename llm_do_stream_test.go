package main

// llm_do_stream_test.go — DoStream / ChatCompletionStream 端到端 (httptest 假服务端)。
// 20260927 自 llm_http_test.go 拆出 (同上)。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	if testing.Short() {
		t.Skip("short: 跳过含重试等待的 HTTP 流式测试")
	}
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

// TestSanitizeMessages_MisplacedToolRebuilt 回归防护 (v3.0.0 修复):
// DeepSeek 以 "Messages with role 'tool' must be a response to a preceding
// message with 'tool_calls'" 拒绝任何非"直接前置 assistant(tool_calls) 块内"
// 的 tool 消息。旧实现只按 id 全数组匹配, 上游错位/截断(压缩/恢复/裁剪)后
// 一条 tool 若 id 仍匹配某 assistant, 会被保留 → 请求前置非 tool_calls → 400。
// 修复: 按"块"扫描, tool 必须位于最近前置 assistant(tool_calls) 之后且 id
// 匹配该块, 否则丢弃 (占位 pass 补真缺响应)。
func TestSanitizeMessages_MisplacedToolRebuilt(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "hi"},
		// 错位: tool 紧跟 user (id 匹配后面的 assistant), 上游截断导致
		{Role: "tool", ToolCallID: "c1", Content: "res"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}}},
	}
	out := sanitizeMessages(in)
	for j, m := range out {
		if m.Role != "tool" {
			continue
		}
		prev := ""
		if j > 0 {
			prev = out[j-1].Role
		}
		if prev != "assistant" || len(out[j-1].ToolCalls) == 0 {
			t.Fatalf("[%d] tool 前置=%q (非 assistant(tool_calls)) → 会触发 400", j, prev)
		}
	}
}

// ==== 流收尾证据双判据 (20260923 断流判定) ====

// 已发出内容后流无声结束(无 [DONE] 也无 finish_reason) = 连接被掐断。
// 必须报 errPartialStream 而非静默发 done —— 否则半截回复被当成完整回复(最隐蔽的失败)。
func TestParseSSE_TruncatedAfterEvents(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"content":"半截"},"finish_reason":null}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if !errors.Is(err, errPartialStream) {
		t.Fatalf("err = %v, want errPartialStream", err)
	}
	if got, want := typesOf(drainEvents(ch)), []string{"content"}; !equalStrings(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if isRetryable(err) {
		t.Fatal("已输出半截的断流不可重试: 重发会造成重复输出")
	}
}

// 零输出即断流 = 没有任何内容给用户看过, 重发安全且便宜 -> 必须可重试。
func TestParseSSE_TruncatedNoEvents(t *testing.T) {
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(""), ch, "m")
	if !errors.Is(err, errStreamTruncated) {
		t.Fatalf("err = %v, want errStreamTruncated", err)
	}
	if !isRetryable(err) {
		t.Fatal("零输出断流必须可重试: 重发是唯一恢复手段")
	}
	if evs := drainEvents(ch); len(evs) != 0 {
		t.Fatalf("不应发出任何事件, got %v", typesOf(evs))
	}
}

// 收尾判定必须是 "sawDone || sawFinish" 双判据 —— 20260924 实测:
// MiniMax-M3 只发 finish_reason、从不发 [DONE](DeepSeek 两者都发)。
// 若有人把判定"简化"成只看 [DONE], MiniMax 的每次正常对话都会被误判为断流。
func TestParseSSE_FinishReasonWithoutDoneMarker(t *testing.T) {
	body := sseLine(`{"choices":[{"delta":{"content":"你好"},"finish_reason":null}]}`) +
		sseLine(`{"choices":[{"delta":{"role":"assistant"},"finish_reason":"stop"}]}`)
	ch := make(chan StreamEvent, 32)
	err := testLLMClient().parseSSE(context.Background(), strings.NewReader(body), ch, "m")
	if err != nil {
		t.Fatalf("有 finish_reason 无 [DONE] 属正常收尾, err = %v", err)
	}
	if got, want := typesOf(drainEvents(ch)), []string{"content", "done"}; !equalStrings(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}
