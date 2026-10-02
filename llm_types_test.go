package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// 无图时必须与旧格式字节级一致 —— 前缀缓存铁律: 任何字段差异都是缓存断裂点。
func TestLLMTypes_NoImages_ByteStable(t *testing.T) {
	b, err := json.Marshal(ChatMessage{Role: "user", Content: "hi"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"role":"user","content":"hi"}`
	if string(b) != want {
		t.Fatalf("无图序列化 = %s, 期望 %s", b, want)
	}
}

func TestLLMTypes_WithImages_ContentBecomesArray(t *testing.T) {
	m := ChatMessage{Role: "user", Content: "看图", Images: []ImagePart{{URL: "data:image/png;base64,AAA"}}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL *struct {
				URL    string `json:"url"`
				Detail string `json:"detail"`
			} `json:"image_url"`
		} `json:"content"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, b)
	}
	if len(got.Content) != 2 {
		t.Fatalf("期望 2 个内容块 (text+image), 实际 %d: %s", len(got.Content), b)
	}
	if got.Content[0].Type != "text" || got.Content[0].Text != "看图" {
		t.Fatalf("首块应为 text 且保留原文: %s", b)
	}
	if got.Content[1].Type != "image_url" || got.Content[1].ImageURL == nil ||
		got.Content[1].ImageURL.URL != "data:image/png;base64,AAA" {
		t.Fatalf("次块应为 image_url: %s", b)
	}
	if strings.Contains(string(b), `"detail"`) {
		t.Fatalf("空 Detail 不应上线 (omitempty): %s", b)
	}
}

func TestLLMTypes_WithImages_EmptyContentSkipped(t *testing.T) {
	b, _ := json.Marshal(ChatMessage{Role: "user", Images: []ImagePart{{URL: "http://x/1.png"}}})
	if strings.Contains(string(b), `"type":"text"`) {
		t.Fatalf("空 Content 不应产生 text 块: %s", b)
	}
	if !strings.Contains(string(b), `"type":"image_url"`) {
		t.Fatalf("应保留 image_url 块: %s", b)
	}
}

func TestLLMTypes_EmptyIDOmitted(t *testing.T) {
	b, _ := json.Marshal(ChatMessage{Role: "assistant", Content: "x"})
	if strings.Contains(string(b), `"id"`) {
		t.Fatalf("空 ID 不得上线 (请求体字节级确定性): %s", b)
	}
}

func TestLLMTypes_ToolCallRoundTrip(t *testing.T) {
	raw := `{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function",` +
		`"function":{"name":"forge","arguments":"{\"a\":1}"}}]}`
	var m ChatMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].Function.Name != "forge" {
		t.Fatalf("解析结果 = %+v", m.ToolCalls)
	}
	if m.ToolCalls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("arguments 应原样保留: %q", m.ToolCalls[0].Function.Arguments)
	}
	b, _ := json.Marshal(m)
	if !strings.Contains(string(b), `"arguments":"{\"a\":1}"`) {
		t.Fatalf("回写 arguments 应重新转义: %s", b)
	}
}

func TestLLMTypes_UsageDetails(t *testing.T) {
	raw := `{"prompt_tokens":10,"prompt_cache_hit_tokens":7,` +
		`"prompt_tokens_details":{"cached_tokens":7},` +
		`"completion_tokens_details":{"reasoning_tokens":42}}`
	var u Usage
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if u.PromptCacheHitTokens != 7 || u.PromptTokensDetails == nil || u.PromptTokensDetails.CachedTokens != 7 {
		t.Fatalf("缓存命中双字段都应解析: %+v", u)
	}
	if u.CompletionTokensDetail == nil || u.CompletionTokensDetail.ReasoningTokens != 42 {
		t.Fatalf("reasoning_tokens 应解析: %+v", u.CompletionTokensDetail)
	}
}
