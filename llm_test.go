package main

import (
	"testing"
	"time"
)

func TestLLMClient_EstimateTokens_Basic(t *testing.T) {
	if got := estimateTokens(nil); got != 0 {
		t.Fatalf("空消息应为 0, 实际 %d", got)
	}
	if got := estimateTokens([]ChatMessage{{Content: "abc"}}); got != 3 {
		t.Fatalf("ASCII 保守按 1:1 上界计, 实际 %d", got)
	}
	if got := estimateTokens([]ChatMessage{{Content: "中文"}}); got != 2 {
		t.Fatalf("CJK 按 1 rune = 1 token, 实际 %d", got)
	}
}

func TestLLMClient_EstimateTokens_ReasoningAndToolArgs(t *testing.T) {
	got := estimateTokens([]ChatMessage{{
		ReasoningContent: "xy",
		ToolCalls:        []ToolCall{{Function: FunctionCall{Arguments: "{}"}}},
	}})
	if got != 4 {
		t.Fatalf("reasoning 2 + args 2 = 4, 实际 %d", got)
	}
}

// 识图: 官方规定每图缩放后 ≤384 tokens。
func TestLLMClient_EstimateTokens_Images(t *testing.T) {
	got := estimateTokens([]ChatMessage{{Images: []ImagePart{{URL: "a"}, {URL: "b"}}}})
	if got != 768 {
		t.Fatalf("2 图应为 768, 实际 %d", got)
	}
}

func TestLLMClient_Lifecycle(t *testing.T) {
	c := NewLLMClient(&Config{RequestTimeout: 5 * time.Second})
	if c == nil {
		t.Fatal("NewLLMClient 返回 nil")
	}
	if !c.IsHealthy() {
		t.Fatal("新建 client 应为健康")
	}
	c.setHealthy(false)
	if c.IsHealthy() {
		t.Fatal("setHealthy(false) 应生效")
	}
	c.Shutdown() // 不得 panic
}

// withMiniMaxWindow 注入窗口状态, 测试结束自动还原。
func withMiniMaxWindow(t *testing.T, open bool) {
	t.Helper()
	old := minimaxWindowNow
	minimaxWindowNow = func() bool { return open }
	t.Cleanup(func() { minimaxWindowNow = old })
}

func miniMaxReadyConfig() *Config {
	return &Config{
		APIKey: "ds-key", BaseURL: "https://ds.example", Model: "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: "https://mm.example", MiniMaxModel: "minimax-m3",
	}
}

// 识图必须强制 DeepSeek —— 路由降级会丢识图能力。
func TestLLMClient_ResolveEndpoint_VisionForcesDeepSeek(t *testing.T) {
	withMiniMaxWindow(t, true)
	c := NewLLMClient(miniMaxReadyConfig())
	ep := c.resolveEndpoint("", []ChatMessage{{Role: "user", Images: []ImagePart{{URL: "x"}}}})
	if ep.Provider != EndpointDeepSeek {
		t.Fatalf("识图应强制 DeepSeek, 实际 %s", ep.Provider)
	}
	if ep.APIKey != "ds-key" || ep.Model != "deepseek-flash" {
		t.Fatalf("端点参数不对: %+v", ep)
	}
}

func TestLLMClient_ResolveEndpoint_MiniMaxWhenWindowOpen(t *testing.T) {
	withMiniMaxWindow(t, true)
	c := NewLLMClient(miniMaxReadyConfig())
	ep := c.resolveEndpoint("", []ChatMessage{{Role: "user", Content: "hi"}})
	if ep.Provider != EndpointMiniMax || ep.APIKey != "mm-key" || ep.Model != "minimax-m3" {
		t.Fatalf("窗口开启且三字段齐备应走 MiniMax: %+v", ep)
	}
}

func TestLLMClient_ResolveEndpoint_WindowClosedUsesDeepSeek(t *testing.T) {
	withMiniMaxWindow(t, false)
	c := NewLLMClient(miniMaxReadyConfig())
	ep := c.resolveEndpoint("", []ChatMessage{{Role: "user", Content: "hi"}})
	if ep.Provider != EndpointDeepSeek {
		t.Fatalf("窗口关闭应回 DeepSeek: %+v", ep)
	}
}

func TestLLMClient_ResolveEndpoint_IncompleteMiniMaxConfig(t *testing.T) {
	withMiniMaxWindow(t, true)
	cfg := miniMaxReadyConfig()
	cfg.MiniMaxModel = "" // 三字段缺一 → 不激活
	c := NewLLMClient(cfg)
	if ep := c.resolveEndpoint("", nil); ep.Provider != EndpointDeepSeek {
		t.Fatalf("三字段不齐不应走 MiniMax: %+v", ep)
	}
}

func TestLLMClient_ResolveEndpoint_PreferModelWins(t *testing.T) {
	withMiniMaxWindow(t, false)
	c := NewLLMClient(miniMaxReadyConfig())
	if ep := c.resolveEndpoint("deepseek-v4-pro", nil); ep.Model != "deepseek-v4-pro" {
		t.Fatalf("preferModel 非空应优先: %+v", ep)
	}
	if ep := c.resolveEndpoint("", nil); ep.Model != "deepseek-flash" {
		t.Fatalf("preferModel 空应回落 cfg.Model: %+v", ep)
	}
}
