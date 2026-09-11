package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestResolveEndpoint 验证提供商路由端点解析 (MiniMax 高峰省钱) 的五条核心规则。
func TestResolveEndpoint(t *testing.T) {
	cfg := &Config{
		APIKey:         "ds-key",
		BaseURL:        "https://api.deepseek.com/v1",
		Model:          "deepseek-flash",
		ModelFlash:     "deepseek-flash",
		ModelPro:       "deepseek-v4-pro",
		ModelVision:    "deepseek-flash",
		MiniMaxAPIKey:  "mm-key",
		MiniMaxBaseURL: "https://api.minimaxi.com/v1",
		MiniMaxModel:   "MiniMax-M3",
	}

	old := minimaxWindowNow
	defer func() { minimaxWindowNow = old }()
	c := NewLLMClient(cfg)

	// 规则1: MiniMax 窗口 + 三字段齐备 → 走 MiniMax, 模型锚定 MiniMaxModel (忽略 DeepSeek 模型名)
	minimaxWindowNow = func() bool { return true }
	ep := c.resolveEndpoint("deepseek-v4-pro", nil)
	if ep.Provider != EndpointMiniMax || ep.APIKey != "mm-key" ||
		ep.BaseURL != "https://api.minimaxi.com/v1" || ep.Model != "MiniMax-M3" {
		t.Fatalf("rule1 MiniMax 分支错误: %+v", ep)
	}

	// 规则2: 非窗口 → DeepSeek, 模型取调用方 preferModel (pickModel 结果)
	minimaxWindowNow = func() bool { return false }
	ep = c.resolveEndpoint("deepseek-flash", nil)
	if ep.Provider != EndpointDeepSeek || ep.Model != "deepseek-flash" {
		t.Fatalf("rule2 DeepSeek+preferModel 错误: %+v", ep)
	}

	// 规则3: 非窗口 + preferModel 空 → 回 cfg.Model
	ep = c.resolveEndpoint("", nil)
	if ep.Model != "deepseek-flash" {
		t.Fatalf("rule3 默认回退错误: %+v", ep)
	}

	// 规则4: 识图请求强制 DeepSeek (即使处于 MiniMax 窗口, 保护识图不因路由降级)
	minimaxWindowNow = func() bool { return true }
	msgs := []ChatMessage{{Role: "user", Content: "看图", Images: []ImagePart{{URL: "http://x/i.png"}}}}
	ep = c.resolveEndpoint("deepseek-flash", msgs)
	if ep.Provider != EndpointDeepSeek || ep.Model != "deepseek-flash" {
		t.Fatalf("rule4 识图强制 DeepSeek 错误: %+v", ep)
	}

	// 规则5: MiniMax 三字段缺 → 回 DeepSeek
	cfg2 := &Config{APIKey: "ds", BaseURL: "https://api.deepseek.com/v1", Model: "m", ModelFlash: "f"}
	c2 := NewLLMClient(cfg2)
	minimaxWindowNow = func() bool { return true }
	ep = c2.resolveEndpoint("m", nil)
	if ep.Provider != EndpointDeepSeek {
		t.Fatalf("rule5 无 MiniMax 回退错误: %+v", ep)
	}
}

// TestThinkingTypePerProvider 回归测试 (2026-09-10 高峰路由事故):
// MiniMax 端点只认 thinking.type = "adaptive"/"disabled", 发 "enabled" 会 400。
// 验证 doStream 实际下发的请求体: MiniMax→adaptive, DeepSeek→enabled,
// 且 reasoning_effort (DeepSeek 专属) 不发给 MiniMax。
func TestThinkingTypePerProvider(t *testing.T) {
	old := minimaxWindowNow
	defer func() { minimaxWindowNow = old }()

	// DeepSeek 也指到本地 mock, 两个端点都抓请求体
	var dsBody, mmBody atomic.Value
	dsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		dsBody.Store(string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer dsSrv.Close()
	mmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mmBody.Store(string(b))
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer mmSrv.Close()

	cfg := &Config{
		APIKey: "ds-key", BaseURL: dsSrv.URL, Model: "deepseek-v4-pro",
		ModelFlash:    "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: mmSrv.URL, MiniMaxModel: "MiniMax-M3",
		MaxTokens: 256, Temperature: 0.7, TopP: 0.95,
		RequestTimeout: 10 * time.Second, StreamTimeout: 10 * time.Second,
		ShowReasoning: true, ReasoningEffort: "high",
	}
	c := NewLLMClient(cfg)

	drain := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for ev := range c.ChatCompletionStream(ctx, []ChatMessage{{Role: "user", Content: "hi"}}, nil, "deepseek-v4-pro") {
			_ = ev
		}
	}

	// 场景1: MiniMax 窗口 → thinking.type 必须是 adaptive, 且无 reasoning_effort
	minimaxWindowNow = func() bool { return true }
	drain()
	mm := strOf(mmBody.Load())
	if !strings.Contains(mm, `"thinking":{"type":"adaptive"}`) {
		t.Fatalf("MiniMax 窗口 thinking.type 应为 adaptive, 请求体: %s", mm)
	}
	if strings.Contains(mm, "reasoning_effort") {
		t.Fatalf("reasoning_effort 是 DeepSeek 专属参数, 不应发给 MiniMax: %s", mm)
	}
	if !strings.Contains(mm, `"model":"MiniMax-M3"`) {
		t.Fatalf("MiniMax 窗口模型应锚定 MiniMax-M3: %s", mm)
	}

	// 场景2: 非窗口 (DeepSeek) → thinking.type 保持 enabled, reasoning_effort 正常下发
	minimaxWindowNow = func() bool { return false }
	drain()
	ds := strOf(dsBody.Load())
	if !strings.Contains(ds, `"thinking":{"type":"enabled"}`) {
		t.Fatalf("DeepSeek 端点 thinking.type 应为 enabled, 请求体: %s", ds)
	}
	if !strings.Contains(ds, `"reasoning_effort":"high"`) {
		t.Fatalf("DeepSeek 端点应下发 reasoning_effort: %s", ds)
	}
}

func strOf(v interface{}) string {
	s, _ := v.(string)
	return s
}
