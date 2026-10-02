package main

import (
	"encoding/json"
)

// ─── Types ──────────────────────────────────────────────────

type ChatMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	// Images 多模态图片 (识图, 官方模型 deepseek-flash)。
	// json:"-" → 不落盘 history / 不参与 Summarize 等复用路径, 仅当轮请求内存。
	// 非空时自定义 MarshalJSON 把 content 输出为 [text + image_url...] 数组
	// (OpenAI 兼容多模态块); 无图时输出与旧格式字节级一致 (前缀缓存兼容)。
	Images []ImagePart `json:"-"`
	// ID 仅内部占位, 绝不序列化上线 (omitempty + 恒空):
	// ① DeepSeek 前缀缓存按 token 前缀匹配, 任何随机字段都可能是断裂点;
	// ② 请求体必须字节级确定性 (deepseek-harness serialize 不带消息 id,
	//    工具调用 id / tool_call_id 才必需)。实测 20260805 随机 id"无害"是
	//    服务端忽略该字段, 而非缓存容忍随机性 —— 直接不发更稳、token 更省。
	ID string `json:"id,omitempty"`
}

// ImagePart 多模态图片块 (vision)。URL 为 base64 data URL 或公开 http(s) 链接;
// Detail: low(512×512 预处理, 快省) | high/original(保原图) | auto。
type ImagePart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

// MarshalJSON 自定义序列化: 有图 → content 输出多模态数组块 (OpenAI 兼容);
// 无图 → 委托原结构 (字节级一致, 前缀缓存不受影响)。
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	if len(m.Images) == 0 {
		type alias ChatMessage // 新类型避免递归; 字段 tag 与原类型一致
		return json.Marshal(alias(m))
	}
	parts := make([]json.RawMessage, 0, 1+len(m.Images))
	if m.Content != "" {
		tb, err := json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: m.Content})
		if err != nil {
			return nil, err
		}
		parts = append(parts, tb)
	}
	for _, img := range m.Images {
		ib, err := json.Marshal(struct {
			Type     string    `json:"type"`
			ImageURL ImagePart `json:"image_url"`
		}{Type: "image_url", ImageURL: img})
		if err != nil {
			return nil, err
		}
		parts = append(parts, ib)
	}
	wire := struct {
		Role             string            `json:"role"`
		Content          []json.RawMessage `json:"content"`
		ReasoningContent string            `json:"reasoning_content,omitempty"`
		ToolCalls        []ToolCall        `json:"tool_calls,omitempty"`
		ToolCallID       string            `json:"tool_call_id,omitempty"`
	}{
		Role:             m.Role,
		Content:          parts,
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
	}
	return json.Marshal(wire)
}

type ToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type StreamEvent struct {
	Type            string     `json:"type"` // "content", "reasoning", "tool_call_delta", "tool_call_done", "done", "error"
	Content         string     `json:"content,omitempty"`
	Error           error      `json:"-"`
	ToolCalls       []ToolCall `json:"tool_calls,omitempty"`
	Truncated       bool       `json:"truncated,omitempty"`        // finish_reason=length: max_tokens 截断
	ReasoningTokens int        `json:"reasoning_tokens,omitempty"` // 本次推理消耗 token 数 (V4 系列)
	CacheHit        int        `json:"cache_hit,omitempty"`        // 本次请求缓存命中 token (会话命中率)
	CacheMiss       int        `json:"cache_miss,omitempty"`       // 本次请求缓存未命中 token (会话命中率)
}

type chatRequest struct {
	Model           string            `json:"model"`
	Messages        []ChatMessage     `json:"messages"`
	Stream          bool              `json:"stream"`
	MaxTokens       int               `json:"max_tokens,omitempty"`
	Temperature     float64           `json:"temperature,omitempty"`
	TopP            float64           `json:"top_p,omitempty"`
	Tools           []json.RawMessage `json:"tools,omitempty"`
	Thinking        *ThinkingConfig   `json:"thinking,omitempty"`
	ReasoningEffort string            `json:"reasoning_effort,omitempty"` // V4-Pro: low|high|max
	StreamOptions   *StreamOptions    `json:"stream_options,omitempty"`   // 要求流式末尾返回 usage (缓存度量)
}

// StreamOptions requests extra stream metadata. include_usage makes DeepSeek
// return the usage block (with prompt_cache_hit_tokens) right before [DONE].
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ThinkingConfig enables DeepSeek reasoning/thinking mode.
type ThinkingConfig struct {
	Type string `json:"type"` // "enabled" or "disabled"
}

type chatResponse struct {
	Choices []struct {
		Delta struct {
			Role             string     `json:"role"`
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// Usage carries token accounting; DeepSeek returns prompt_cache_hit_tokens /
// prompt_cache_miss_tokens when stream_options.include_usage=true (or on
// non-stream responses).
type Usage struct {
	PromptTokens           int                     `json:"prompt_tokens"`
	CompletionTokens       int                     `json:"completion_tokens"`
	TotalTokens            int                     `json:"total_tokens"`
	PromptCacheHitTokens   int                     `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens  int                     `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails    *PromptTokensDetails    `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetail *CompletionTokensDetail `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails carries the OpenAI-shape cache-hit field. DeepSeek populates
// BOTH prompt_cache_hit_tokens (native) and prompt_tokens_details.cached_tokens
// (OpenAI shape). A vanilla OpenAI parser reads only the latter; we read both and
// take the max so the cache metric survives any field-name drift (pi-mono#3880).
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// CompletionTokensDetail carries per-part completion breakdown (V4 系列返回 reasoning_tokens).
type CompletionTokensDetail struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}
