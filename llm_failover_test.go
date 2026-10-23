package main

// llm_failover_test.go — 故障兜底 (failover) 端到端哨兵 (20261011)。
//
// 背景: 原"高峰时段自动切 MiniMax 省钱"实测为负收益 (T31 成本复核 ——
// MiniMax 单价 ≈ DeepSeek 高峰价, 且同一份 messages 在 MiniMax tokenizer 下
// 计数高 1.73x, 各缓存命中率下均贵 1.39~3.90x), 主人拍板撤销;
// MiniMax 改作 DeepSeek 请求失败时的兜底, 不参与时段路由。
//
// 本哨兵钉三件事 (缺一即退化为"兜底接线被摘掉也全绿"):
//  1. DeepSeek 可重试失败 (5xx) 重试耗尽 → 自动落到 MiniMax, 用户看到完整回复;
//  2. 不可重试失败 (401 配置错) 不兜底 —— 配置问题必须显式报错, 不得被兜底掩盖;
//  3. 用户取消 (ctx 已死) 不兜底 —— 重发无意义且会拖住退出。

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// 场景1: DeepSeek 5xx → 重试耗尽 → MiniMax 兜底成功。
// 注: 重试退避是硬编码 2^n 秒 + jitter, 本用例须等满两轮退避 (约 6~12s)。
func TestChatCompletionStream_FailoverOnDeepSeekDown(t *testing.T) {
	saveGlobals(t)
	var mmCalls int32
	dsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, `{"error":{"message":"upstream boom","type":"server_error"}}`)
	}))
	defer dsSrv.Close()
	mmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&mmCalls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"兜底成功"},"finish_reason":null}]}`))
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer mmSrv.Close()

	cfg := &Config{
		APIKey: "ds-key", BaseURL: dsSrv.URL, Model: "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: mmSrv.URL, MiniMaxModel: "MiniMax-M3",
		MaxTokens: 256, RequestTimeout: 10 * time.Second, StreamTimeout: 10 * time.Second,
	}
	c := NewLLMClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var got []string
	var content, errMsg string
	for ev := range c.ChatCompletionStream(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil, "deepseek-flash") {
		got = append(got, ev.Type)
		if ev.Type == "content" {
			content += ev.Content
		}
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.Error()
		}
	}
	if errMsg != "" {
		t.Fatalf("兜底应成功, 却收到 error 事件: %s (events=%v)", errMsg, got)
	}
	if n := atomic.LoadInt32(&mmCalls); n == 0 {
		t.Fatalf("DeepSeek 不可用时应调用 MiniMax 兜底, 实际 0 次; events=%v", got)
	}
	if content != "兜底成功" {
		t.Fatalf("兜底内容丢失: %q (events=%v)", content, got)
	}
	if !c.IsHealthy() {
		t.Fatal("兜底成功后客户端应判健康")
	}
}

// 场景2: 401 (不可重试) 不得兜底 —— 兜底会把"密钥配错"伪装成"服务抖动"。
func TestChatCompletionStream_NoFailoverOnAuthError(t *testing.T) {
	saveGlobals(t)
	var mmCalls int32
	dsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"message":"invalid api key","type":"auth"}}`)
	}))
	defer dsSrv.Close()
	mmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&mmCalls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`))
	}))
	defer mmSrv.Close()

	cfg := &Config{
		APIKey: "bad", BaseURL: dsSrv.URL, Model: "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: mmSrv.URL, MiniMaxModel: "MiniMax-M3",
		MaxTokens: 256, RequestTimeout: 10 * time.Second, StreamTimeout: 10 * time.Second,
	}
	c := NewLLMClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var errMsg string
	for ev := range c.ChatCompletionStream(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil, "deepseek-flash") {
		if ev.Type == "error" && ev.Error != nil {
			errMsg = ev.Error.Error()
		}
	}
	if n := atomic.LoadInt32(&mmCalls); n != 0 {
		t.Fatalf("401 是配置错, 不得兜底 (会掩盖密钥问题), 实际调用 MiniMax %d 次", n)
	}
	if errMsg == "" {
		t.Fatal("401 必须上报错误")
	}
}

// 场景3: 用户取消 (ctx 已死) 不得兜底 —— 重发无意义且拖住退出。
func TestChatCompletionStream_NoFailoverOnCancel(t *testing.T) {
	saveGlobals(t)
	var mmCalls int32
	// 慢响应 (1.5s) 而非永久挂起: 客户端 400ms 即取消, 但服务端 handler 必须
	// 自行返回 —— 否则 httptest.Server.Close() 会等活跃连接直到测试超时。
	dsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"too late"},"finish_reason":"stop"}]}`))
	}))
	defer dsSrv.Close()
	mmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&mmCalls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`))
	}))
	defer mmSrv.Close()

	cfg := &Config{
		APIKey: "ds-key", BaseURL: dsSrv.URL, Model: "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: mmSrv.URL, MiniMaxModel: "MiniMax-M3",
		MaxTokens: 256, RequestTimeout: 10 * time.Second, StreamTimeout: 10 * time.Second,
	}
	c := NewLLMClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	for range c.ChatCompletionStream(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil, "deepseek-flash") {
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("取消后应立即返回, 实际耗时 %v", elapsed)
	}
	if n := atomic.LoadInt32(&mmCalls); n != 0 {
		t.Fatalf("用户取消不得触发兜底, 实际调用 MiniMax %d 次", n)
	}
}
