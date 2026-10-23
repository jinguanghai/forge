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

func miniMaxReadyConfig() *Config {
	return &Config{
		APIKey: "ds-key", BaseURL: "https://ds.example", Model: "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: "https://mm.example", MiniMaxModel: "minimax-m3",
	}
}

// 主路径恒 DeepSeek —— 即使 MiniMax 三字段齐备、即使处于高峰时段。
// 核心判据 (20261011 T31): 谁把"高峰切 MiniMax"加回来, 本用例必须报红。
func TestLLMClient_ResolveEndpoint_AlwaysDeepSeek(t *testing.T) {
	old := peakHourNow
	peakHourNow = func() bool { return true } // 高峰时段
	t.Cleanup(func() { peakHourNow = old })

	c := NewLLMClient(miniMaxReadyConfig())
	cases := []struct {
		name string
		msgs []ChatMessage
	}{
		{"纯文本", []ChatMessage{{Role: "user", Content: "hi"}}},
		{"识图", []ChatMessage{{Role: "user", Images: []ImagePart{{URL: "x"}}}}},
		{"空消息", nil},
	}
	for _, tc := range cases {
		ep := c.resolveEndpoint("", tc.msgs)
		if ep.Provider != EndpointDeepSeek {
			t.Fatalf("[%s] 主路径必须 DeepSeek (高峰也不切厂商), 实际 %s", tc.name, ep.Provider)
		}
		if ep.APIKey != "ds-key" || ep.BaseURL != "https://ds.example" {
			t.Fatalf("[%s] 端点参数不对: %+v", tc.name, ep)
		}
	}
}

// ── 故障兜底 (failoverEndpoint): MiniMax 唯一登场路径 ──

func TestLLMClient_FailoverEndpoint_Ready(t *testing.T) {
	c := NewLLMClient(miniMaxReadyConfig())
	ep, ok := c.failoverEndpoint("deepseek-flash", []ChatMessage{{Role: "user", Content: "hi"}})
	if !ok {
		t.Fatal("三字段齐备且非识图, 应可兜底")
	}
	if ep.Provider != EndpointMiniMax || ep.APIKey != "mm-key" ||
		ep.BaseURL != "https://mm.example" || ep.Model != "minimax-m3" {
		t.Fatalf("兜底端点字段错: %+v", ep)
	}
}

func TestLLMClient_FailoverEndpoint_NotConfigured(t *testing.T) {
	cfg := miniMaxReadyConfig()
	cfg.MiniMaxModel = "" // 三字段缺一
	c := NewLLMClient(cfg)
	if _, ok := c.failoverEndpoint("", nil); ok {
		t.Fatal("三字段不齐不得兜底 (半配置会打到错误端点)")
	}
}

func TestLLMClient_FailoverEndpoint_VisionRefused(t *testing.T) {
	c := NewLLMClient(miniMaxReadyConfig())
	msgs := []ChatMessage{{Role: "user", Images: []ImagePart{{URL: "x"}}}}
	if _, ok := c.failoverEndpoint("", msgs); ok {
		t.Fatal("识图不得兜底到 MiniMax (不识图, 降级会丢能力)")
	}
}

func TestLLMClient_ResolveEndpoint_PreferModelWins(t *testing.T) {
	c := NewLLMClient(miniMaxReadyConfig())
	if ep := c.resolveEndpoint("deepseek-v4-pro", nil); ep.Model != "deepseek-v4-pro" {
		t.Fatalf("preferModel 非空应优先: %+v", ep)
	}
	if ep := c.resolveEndpoint("", nil); ep.Model != "deepseek-flash" {
		t.Fatalf("preferModel 空应回落 cfg.Model: %+v", ep)
	}
}
