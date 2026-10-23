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

// TestResolveEndpoint 验证主路径端点解析 —— 恒 DeepSeek (20261011 撤销 MiniMax 高峰路由)。
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
	c := NewLLMClient(cfg)

	// 规则1: preferModel (pickModel 结果) 优先
	if ep := c.resolveEndpoint("deepseek-v4-pro", nil); ep.Provider != EndpointDeepSeek || ep.Model != "deepseek-v4-pro" {
		t.Fatalf("rule1 preferModel 优先错误: %+v", ep)
	}
	// 规则2: preferModel 空 → 回 cfg.Model
	if ep := c.resolveEndpoint("", nil); ep.Provider != EndpointDeepSeek || ep.Model != "deepseek-flash" {
		t.Fatalf("rule2 默认回退错误: %+v", ep)
	}
	// 规则3: 识图同样走 DeepSeek (主路径本来如此; MiniMax-M3 不识图)
	msgs := []ChatMessage{{Role: "user", Content: "看图", Images: []ImagePart{{URL: "http://x/i.png"}}}}
	if ep := c.resolveEndpoint("deepseek-flash", msgs); ep.Provider != EndpointDeepSeek || ep.Model != "deepseek-flash" {
		t.Fatalf("rule3 识图错误: %+v", ep)
	}
	// 规则4: MiniMax 未配置 → 仍是 DeepSeek
	cfg2 := &Config{APIKey: "ds", BaseURL: "https://api.deepseek.com/v1", Model: "m", ModelFlash: "f"}
	c2 := NewLLMClient(cfg2)
	if ep := c2.resolveEndpoint("m", nil); ep.Provider != EndpointDeepSeek {
		t.Fatalf("rule4 无 MiniMax 错误: %+v", ep)
	}
	// 规则5: MiniMax 端点只由 failoverEndpoint 给出, 主路径永不返回它
	if ep, ok := c.failoverEndpoint("", nil); !ok || ep.Provider != EndpointMiniMax {
		t.Fatalf("rule5 兜底端点错误: ok=%v ep=%+v", ok, ep)
	}
}

// TestThinkingTypePerProvider 回归测试 (2026-09-10 高峰路由事故):
// MiniMax 端点只认 thinking.type = "adaptive"/"disabled", 发 "enabled" 会 400。
// 20261011 起 MiniMax 只出现在故障兜底路径, 故主路径走 doStream、兜底走 doStreamTo 分别验证。
func TestThinkingTypePerProvider(t *testing.T) {
	saveGlobals(t)

	// DeepSeek 与 MiniMax 都指到本地 mock, 两条路径各抓一次请求体
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
	msgs := []ChatMessage{{Role: "user", Content: "hi"}}
	ctx := context.Background()

	// 主路径 (DeepSeek): thinking.enabled + reasoning_effort
	if err := c.doStream(ctx, msgs, nil, make(chan StreamEvent, 32), "deepseek-v4-pro"); err != nil {
		t.Fatalf("DeepSeek doStream: %v", err)
	}
	ds := strOf(dsBody.Load())
	if !strings.Contains(ds, `"thinking":{"type":"enabled"}`) {
		t.Fatalf("DeepSeek 端点 thinking.type 应为 enabled, 请求体: %s", ds)
	}
	if !strings.Contains(ds, `"reasoning_effort":"high"`) {
		t.Fatalf("DeepSeek 端点应下发 reasoning_effort: %s", ds)
	}

	// 兜底路径 (MiniMax): thinking.adaptive, 且不得出现 reasoning_effort
	mmEp, ok := c.failoverEndpoint("deepseek-v4-pro", msgs)
	if !ok {
		t.Fatal("MiniMax 三字段齐备应可兜底")
	}
	if err := c.doStreamTo(ctx, msgs, nil, make(chan StreamEvent, 32), mmEp.Model, mmEp); err != nil {
		t.Fatalf("MiniMax doStreamTo: %v", err)
	}
	mm := strOf(mmBody.Load())
	if !strings.Contains(mm, `"thinking":{"type":"adaptive"}`) {
		t.Fatalf("MiniMax 端点 thinking.type 应为 adaptive, 请求体: %s", mm)
	}
	if strings.Contains(mm, "reasoning_effort") {
		t.Fatalf("reasoning_effort 是 DeepSeek 专属参数, 不应发给 MiniMax: %s", mm)
	}
	if !strings.Contains(mm, `"model":"MiniMax-M3"`) {
		t.Fatalf("MiniMax 端点模型应锚定 MiniMax-M3: %s", mm)
	}
}

func strOf(v interface{}) string {
	s, _ := v.(string)
	return s
}
