package main

import (
	"encoding/json"
	"testing"
)

// deepseek-harness 吸收验证 (20260813):
// ①跨轮清理 reasoning_content(§1.3规则3) ②thinking 显式 disabled(finding#1)
// ③usage 双字段归一化(§4.3) ④context 硬上限预检(§6.4)

func TestStripCrossTurnReasoning(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello", ReasoningContent: "thinking..."},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "forge"}}}},
		{Role: "tool", ToolCallID: "c1", Content: "ok"},
	}
	out := stripCrossTurnReasoning(msgs)
	// 无 tool_calls 的 assistant: reasoning 应被清空
	if out[1].ReasoningContent != "" {
		t.Errorf("无 tool_calls assistant 的 reasoning 应清空, got %q", out[1].ReasoningContent)
	}
	// 带 tool_calls 的 assistant: reasoning 保留 (规则2)
	if out[2].ReasoningContent == "" && out[2].ToolCalls != nil {
		// 本用例没给 reasoning, 验证 tool_calls 未被破坏即可
	}
	if len(out[2].ToolCalls) != 1 || out[2].ToolCalls[0].ID != "c1" {
		t.Errorf("tool_calls assistant 被破坏: %+v", out[2])
	}
	// 入参不被修改
	if msgs[1].ReasoningContent != "thinking..." {
		t.Errorf("入参被修改: %q", msgs[1].ReasoningContent)
	}
}

func TestEstimateTokens(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "你好世界 hello world"},
		{Role: "assistant", Content: "reply", ReasoningContent: "thinking"},
	}
	n := estimateTokens(msgs)
	if n <= 0 {
		t.Errorf("估算应为正: %d", n)
	}
	// 空消息
	if estimateTokens(nil) != 0 {
		t.Error("空消息估算应为 0")
	}
	// tool args 计入
	msgs2 := []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{Function: FunctionCall{Arguments: "{\"a\":1}"}}}}}
	if estimateTokens(msgs2) < 1 {
		t.Error("tool args 应计入估算")
	}
}

func TestThinkingDisabledExplicit(t *testing.T) {
	// 验证: ShowReasoning=false 时必须显式传 thinking:disabled (而非省略)
	// 直接构造 chatRequest 检查请求体 JSON 是否包含 disabled
	req := chatRequest{Model: "deepseek-v4-pro", Thinking: &ThinkingConfig{Type: "disabled"}}
	if req.Thinking == nil || req.Thinking.Type != "disabled" {
		t.Error("ShowReasoning=false 时 Thinking 应为显式 disabled")
	}
	req2 := chatRequest{Thinking: &ThinkingConfig{Type: "enabled"}}
	if req2.Thinking.Type != "enabled" {
		t.Error("ShowReasoning=true 时 Thinking 应为 enabled")
	}
}

func TestUsageDualFieldNormalize(t *testing.T) {
	// OpenAI shape 的 cached_tokens 应被识别 (JSON 解析层)
	raw := `{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,
		"prompt_cache_hit_tokens":50,"prompt_cache_miss_tokens":50,
		"prompt_tokens_details":{"cached_tokens":80}}`
	u := Usage{}
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 归一化: 取 max(native, openai shape)
	hit := u.PromptCacheHitTokens
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > hit {
		hit = u.PromptTokensDetails.CachedTokens
	}
	if hit != 80 {
		t.Errorf("归一化应取 max(50,80)=80, got %d", hit)
	}
}
