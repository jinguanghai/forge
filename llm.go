package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ─── Types ──────────────────────────────────────────────────

type ChatMessage struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	// Images 多模态图片 (识图, 官方模型 deepseek-v4-flash-vision-exp)。
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

// ─── Retryable errors ──────────────────────────────────────

var (
	retryableHTTPStatuses = map[int]bool{
		429: true, // rate limited
		500: true,
		502: true,
		503: true,
		504: true,
	}
)

// ─── LLM Client ────────────────────────────────────────────

type LLMClient struct {
	cfg       *Config
	client    *http.Client
	transport *http.Transport

	mu      sync.RWMutex
	healthy bool
}

func NewLLMClient(cfg *Config) *LLMClient {
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
	}

	return &LLMClient{
		cfg: cfg,
		client: &http.Client{
			Transport: transport,
			Timeout:   cfg.RequestTimeout,
		},
		transport: transport,
		healthy:   true,
	}
}

func (c *LLMClient) Shutdown() {
	c.transport.CloseIdleConnections()
}

func (c *LLMClient) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthy
}

// ChatCompletionStream sends a streaming chat completion request.
// Returns a channel that receives StreamEvents.
// The channel is closed when streaming completes.
func (c *LLMClient) ChatCompletionStream(
	ctx context.Context,
	messages []ChatMessage,
	tools []json.RawMessage,
	model ...string, // 可选: 指定模型 (路由用); 缺省用 cfg.Model
) <-chan StreamEvent {
	ch := make(chan StreamEvent, 32)

	// 模型选择: 显式传入 > cfg.Model
	useModel := c.cfg.Model
	if len(model) > 0 && strings.TrimSpace(model[0]) != "" {
		useModel = strings.TrimSpace(model[0])
	}

	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				select {
				case ch <- StreamEvent{Type: "error", Error: fmt.Errorf("llm stream panic: %v", r)}:
				default:
				}
			}
		}()

		const maxRetries = 3
		for attempt := 0; attempt < maxRetries; attempt++ {
			if attempt > 0 {
				delay := time.Duration(math.Pow(2, float64(attempt))) * time.Second
				delay += time.Duration(mathrand.Int63n(int64(delay))) // jitter
				select {
				case <-ctx.Done():
					ch <- StreamEvent{Type: "error", Error: ctx.Err()}
					return
				case <-time.After(delay):
				}
			}

			err := c.doStream(ctx, messages, tools, ch, useModel)
			if err == nil {
				c.setHealthy(true)
				return
			}

			// Only retry on transient errors
			if !isRetryable(err) || attempt == maxRetries-1 {
				c.setHealthy(false)
				ch <- StreamEvent{Type: "error", Error: fmt.Errorf("LLM request failed after %d attempts: %w", attempt+1, err)}
				return
			}
		}
	}()

	return ch
}

// sysChanged 全局: doStream 里比对 system 前缀后设置, 流式解析处读取。
// 单会话顺序请求, 无并发竞争 (若有并发需改为 per-request)。
var sysChanged bool

// hardContextLimit is DeepSeek V4's real (undocumented) context ceiling:
// 2^20 = 1,048,576 tokens. The server 400s at or above it (measured by
// deepseek-harness probe_6b, 2026-05-09). Local pre-flight check avoids the
// silent off-by-one rejection.
const hardContextLimit = 1_048_576

// estimateTokens approximates local token count. DeepSeek's tokenizer is
// ~3.6-5.9 chars/token for English ASCII; Chinese CJK ~1-2 chars/token.
// We use a conservative per-rune estimate (upper bound) so we never
// UNDER-estimate and hit the server's hard 400.
func estimateTokens(msgs []ChatMessage) int {
	total := 0
	for _, m := range msgs {
		for _, r := range m.Content {
			if r > 0x2E80 { // CJK / full-width: ~1 token per rune
				total++
			} else {
				total++ // conservative: even ASCII counted 1:1 (upper bound)
			}
		}
		for range m.ReasoningContent {
			total++
		}
		for _, tc := range m.ToolCalls {
			for range tc.Function.Arguments {
				total++
			}
		}
		// 识图: 官方文档规定每图自动缩放后 ≤384 tokens (800×800 量级)
		for range m.Images {
			total += 384
		}
	}
	return total
}

// Summarize 非流式摘要 (前缀缓存复用):
// 把 messages 压缩成 ≤maxLen 字中文摘要。
// 用于历史压缩 (compactHistory): 超 token 阈值时先把最旧段浓缩成摘要再丢弃原文。
// 轻量模型优先 (ModelFlash), 显式关闭 thinking 省 token; 失败返回 error, 调用方降级不阻塞。
//
// 前缀缓存复用: 旧实现把 messages 拍扁成单条 user 消息 → 请求前缀
// 与真实对话请求完全不同 → 每次摘要 100% 缓存未命中 (付费 token)。
// 新实现: messages 原样作为请求前缀 (调用方保证含 system), 摘要指令追加为最后一条
// user 消息 (最后一条已是 user 则合并进其 Content, 避免连续两条 user) →
// 请求 = [system, 历史前段..., 指令] 与真实请求共享前缀 → DeepSeek 前缀缓存命中,
// 只有指令 token 是新增付费。
func (c *LLMClient) Summarize(ctx context.Context, messages []ChatMessage, maxLen int) (string, error) {
	instruction := fmt.Sprintf(
		"你是会话摘要器。请用中文把以上对话压缩成不超过 %d 字的摘要。必须保留: ①原始任务/目标 ②关键决策与结论 ③已完成事项 ④未完成事项 ⑤重要约定/约束。只输出摘要正文，不要任何前缀、标题或解释。", maxLen)
	reqMsgs := make([]ChatMessage, 0, len(messages)+1)
	reqMsgs = append(reqMsgs, messages...)
	if n := len(reqMsgs); n > 0 && reqMsgs[n-1].Role == "user" {
		reqMsgs[n-1].Content += "\n\n" + instruction // 合并: 保持前缀不变, 避免连续两条 user
	} else {
		reqMsgs = append(reqMsgs, ChatMessage{Role: "user", Content: instruction})
	}
	model := c.cfg.Model
	if c.cfg.ModelFlash != "" {
		model = c.cfg.ModelFlash
	}
	reqBody := chatRequest{
		Model:       model,
		Messages:    reqMsgs,
		Stream:      false,
		MaxTokens:   maxLen * 3,
		Temperature: 0.2,
		Thinking:    &ThinkingConfig{Type: "disabled"},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal summarize request: %w", err)
	}
	apiURL := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create summarize request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("summarize http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if rerr != nil {
			return "", fmt.Errorf("summarize read error body: %w", rerr)
		}
		return "", fmt.Errorf("summarize failed status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode summarize response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("summarize: empty content")
	}
	summary := strings.TrimSpace(out.Choices[0].Message.Content)
	if runes := []rune(summary); len(runes) > maxLen*2 {
		summary = string(runes[:maxLen*2])
	}
	return summary, nil
}

func (c *LLMClient) doStream(
	ctx context.Context,
	messages []ChatMessage,
	tools []json.RawMessage,
	ch chan<- StreamEvent,
	model string, // 本次请求使用的模型 (路由选择)
) error {
	// 上下文硬上限预检 (§6.4 规则1): len(messages_tokens) + max_tokens <= 1_048_576
	estIn := estimateTokens(messages)
	if c.cfg.MaxTokens > 0 && estIn+c.cfg.MaxTokens > hardContextLimit {
		return &LLMError{
			StatusCode: 0,
			Message: fmt.Sprintf("请求超上下文硬上限: 估算 %d tokens (messages) + %d (max_tokens) > %d。请清理历史或减小 max_tokens。",
				estIn, c.cfg.MaxTokens, hardContextLimit),
			Type: "context_length_exceeded",
		}
	}
	// ─── 前缀指纹守卫 ──────
	// system 前缀进程内应恒定; 若变化 (运行中 memory.json 被改) 立即告警,
	// 并记录 sys_changed 事件到 cache_stats.jsonl 供 /cache 汇总。
	sysChanged = false
	if currentSystemHash != "" && lastSystemHash != "" && currentSystemHash != lastSystemHash {
		sysChanged = true
		fmt.Fprintf(os.Stderr, "\n%s 缓存前缀变更: system 指纹 %s -> %s (新前缀首次请求将全量未命中, 之后自动恢复)\n",
			color(ansi.yellow, "⚠️"), lastSystemHash, currentSystemHash)
	}
	lastSystemHash = currentSystemHash

	// ── FORGE_DEBUG_REQ: 打印请求消息构成 (缓存诊断) ──
	if os.Getenv("FORGE_DEBUG_REQ") != "" {
		var sb strings.Builder
		totalCh := 0
		fmt.Fprintf(&sb, "[debug-req] model=%s msgs=%d estTokens=%d\n", model, len(messages), estimateTokens(messages))
		for i, m := range messages {
			ch := len([]rune(m.Content))
			totalCh += ch
			role := m.Role
			if m.ToolCallID != "" {
				role += "(toolcall)"
			}
			if len(m.ToolCalls) > 0 {
				role += "(toolcalls)"
			}
			preview := m.Content
			if len(preview) > 90 {
				preview = preview[:90] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", "\\n")
			fmt.Fprintf(&sb, "  [%d] %s ch=%d: %q\n", i, role, ch, preview)
		}
		fmt.Fprintf(&sb, "  totalChars=%d\n", totalCh)
		fmt.Fprintln(os.Stderr, sb.String())
	}
	reqBody := chatRequest{
		Model:         model,
		Messages:      messages,
		Stream:        true,
		MaxTokens:     c.cfg.MaxTokens,
		Temperature:   c.cfg.Temperature,
		TopP:          c.cfg.TopP,
		Tools:         tools,
		StreamOptions: &StreamOptions{IncludeUsage: true}, // 流式末尾返回 usage → 缓存度量
	}

	// Thinking 模式显式声明 (deepseek-harness finding #1):
	// deepseek-v4-pro/flash 默认 thinking=enabled —— 若用户关闭 ShowReasoning 而
	// 不显式传 thinking:disabled, 服务端仍按默认开启 → 平凡提示也烧 ~30 reasoning
	// tokens (账单 2-5 倍)。因此两种状态都显式下发。
	if c.cfg.ShowReasoning {
		reqBody.Thinking = &ThinkingConfig{Type: "enabled"}
		// V4-Pro 思考强度 (2026-08 新增 low/high/max): 平凡任务用 low 省输出token(输出计价),
		// 复杂任务用 high/max。仅在思考开启时下发, 避免 API 400。
		if c.cfg.ReasoningEffort != "" {
			reqBody.ReasoningEffort = c.cfg.ReasoningEffort
		}
	} else {
		reqBody.Thinking = &ThinkingConfig{Type: "disabled"}
	}

	// Deep-copy messages to avoid mutating the caller's slice (prevents tool_call_id
	// mismatch when the same messages are reused across retries or turns).
	reqBody.Messages = sanitizeMessages(messages)

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	apiURL := strings.TrimRight(c.cfg.BaseURL, "/") + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if rerr != nil {
			return fmt.Errorf("stream read error body: %w", rerr)
		}
		msg := string(body)
		// Try to parse JSON error body for cleaner message
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
			msg = errResp.Error.Message
			if errResp.Error.Code != "" {
				msg = msg + " (" + errResp.Error.Code + ")"
			}
		}
		return &LLMError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
			Message:    msg,
		}
	}

	return c.parseSSE(ctx, resp.Body, ch, model)
}

func (c *LLMClient) parseSSE(ctx context.Context, body io.Reader, ch chan<- StreamEvent, model string) error {
	_ = model // usage 记录用 (见下方 cr.Usage 分支)
	scanner := bufio.NewScanner(body)
	// Use larger buffer for long SSE lines (tool call arguments can be large)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024)

	st := &sseState{}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			if st.sentEvents > 0 {
				return errPartialStream
			}
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue // not a data line
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if strings.TrimSpace(data) == "[DONE]" {
			if !st.toolDone {
				ch <- StreamEvent{Type: "done", Truncated: st.pendingFinish == "length", ReasoningTokens: usageReasoningTokens(st.lastUsage)}
			}
			return nil
		}

		var cr chatResponse
		if err := json.Unmarshal([]byte(data), &cr); err != nil {
			continue // skip malformed lines
		}
		if stop, err := c.handleSSEChunk(st, ch, model, cr); stop || err != nil {
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		if st.sentEvents > 0 {
			return errPartialStream
		}
		return fmt.Errorf("read stream: %w", err)
	}

	if !st.toolDone {
		ch <- StreamEvent{Type: "done", Truncated: st.pendingFinish == "length", ReasoningTokens: usageReasoningTokens(st.lastUsage)}
	}
	return nil
}

// sseState 封装 parseSSE 循环内的可变状态，供 handleSSEChunk 读写。
type sseState struct {
	toolCallAccum []ToolCall
	lastUsage     *Usage
	pendingFinish string
	toolDone      bool
	sentEvents    int
}

// handleSSEChunk 处理一个已解析的流式 chunk。返回 stop=true 表示应结束解析
// （content_filter 等终止情况），err 非 nil 表示遇到错误。
func (c *LLMClient) handleSSEChunk(st *sseState, ch chan<- StreamEvent, model string, cr chatResponse) (bool, error) {
	// API-level error in streaming response
	if cr.Error != nil {
		return true, &LLMError{Message: cr.Error.Message, Type: cr.Error.Type}
	}

	// Usage block: with stream_options.include_usage=true DeepSeek sends a
	// final chunk whose choices is empty and usage carries cache hit/miss.
	// Must be handled BEFORE the choices==0 skip below.
	if cr.Usage != nil {
		st.lastUsage = cr.Usage // 保存供 done 事件携带 (推理消耗度量)
	}
	if cr.Usage != nil && (cr.Usage.PromptCacheHitTokens > 0 || cr.Usage.PromptCacheMissTokens > 0) {
		// 双字段归一化 (§4.3): DeepSeek native 与 OpenAI shape 同时存在, 取 max 防漂移
		hit := cr.Usage.PromptCacheHitTokens
		if cr.Usage.PromptTokensDetails != nil && cr.Usage.PromptTokensDetails.CachedTokens > hit {
			hit = cr.Usage.PromptTokensDetails.CachedTokens
		}
		miss := cr.Usage.PromptCacheMissTokens
		recordCacheStat(model, hit, miss, currentSystemHash, sysChanged)
		// 会话级实时命中率: 经事件通道把本次 hit/miss 带回 agent.stats。
		// 收尾阶段(toolDone)不再发事件, 保持只读收尾契约(与 reasoning/content/delta 一致)。
		if !st.toolDone {
			ch <- StreamEvent{Type: "cache_usage", CacheHit: hit, CacheMiss: miss}
		}
	} else if sysChanged {
		// 前缀断裂事件即使无 usage 也记录, 供 /cache 汇总
		recordCacheStat(model, 0, 0, currentSystemHash, sysChanged)
	}

	if len(cr.Choices) == 0 {
		return false, nil
	}
	choice := cr.Choices[0]

	// Reasoning content (DeepSeek R1)
	if !st.toolDone && choice.Delta.ReasoningContent != "" {
		st.sentEvents++
		ch <- StreamEvent{Type: "reasoning", Content: choice.Delta.ReasoningContent}
	}

	// Text content
	if !st.toolDone && choice.Delta.Content != "" {
		st.sentEvents++
		ch <- StreamEvent{Type: "content", Content: choice.Delta.Content}
	}

	// Tool call deltas
	if !st.toolDone && len(choice.Delta.ToolCalls) > 0 {
		st.sentEvents++
		for _, tc := range choice.Delta.ToolCalls {
			ch <- StreamEvent{Type: "tool_call_delta", ToolCalls: []ToolCall{tc}}
		}
		st.toolCallAccum = append(st.toolCallAccum, choice.Delta.ToolCalls...)
	}

	// Terminal states
	switch choice.FinishReason {
	case "tool_calls":
		merged := mergeToolCalls(st.toolCallAccum)
		ch <- StreamEvent{Type: "tool_call_done", ToolCalls: merged}
		// 工具轮次同样延迟收尾: 继续读流(不再发事件)直到 usage 块/[DONE],
		// 使每个工具轮次的缓存命中/未命中都进入度量闭环, 而非只记最终轮。
		st.toolDone = true
	case "stop", "length":
		// DeepSeek 偶发在工具调用 delta 已发出后仍返回 stop/length（max_tokens 截断或
		// 模型怪癖）。此时 toolCallAccum 是未合并的原始分片：若直接发 done，agent 会把
		// 每个分片（含空 ID、部分 arguments）当成独立工具执行，产生 tool 响应 ID 与
		// assistant.tool_calls 不匹配 → API 400 "did not have response messages"。
		// 因此只要累积了工具调用 delta，就必须统一合并后再收尾。
		if len(st.toolCallAccum) > 0 {
			merged := mergeToolCalls(st.toolCallAccum)
			ch <- StreamEvent{Type: "tool_call_done", ToolCalls: merged}
			st.toolDone = true
		} else {
			// 延迟收尾: 继续等 usage 块([DONE] 前)到达, 以便 done 携带 reasoning_tokens;
			// Truncated 标记统一在收尾处判定 (finish_reason=length ⇒ max_tokens 截断)
			st.pendingFinish = choice.FinishReason
		}
	case "content_filter":
		return true, &LLMError{Message: "content filter triggered", Type: "content_filter"}
	}
	return false, nil
}

// usageReasoningTokens 从末尾 usage 块提取推理消耗 (V4 系列 reasoning_tokens),
// 无 usage 信息时返回 0。
func usageReasoningTokens(u *Usage) int {
	if u == nil || u.CompletionTokensDetail == nil {
		return 0
	}
	return u.CompletionTokensDetail.ReasoningTokens
}

func (c *LLMClient) setHealthy(h bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = h
}

// ─── LLMError ───────────────────────────────────────────────

type LLMError struct {
	StatusCode int
	Body       string
	Message    string
	Type       string
}

func (e *LLMError) Error() string {
	if e.StatusCode > 0 {
		// 401: 给用户友好的中文提示
		if e.StatusCode == 401 {
			hint := "请设置正确的 API 密钥 (DEEPSEEK_API_KEY 或 LLM_API_KEY 环境变量，或在 .env 文件中配置)"
			return fmt.Sprintf("LLM HTTP 401 (鉴权失败): %s", hint)
		}
		return fmt.Sprintf("LLM HTTP %d: %s", e.StatusCode, e.Message)
	}
	return e.Message
}

// errPartialStream is returned when an SSE stream breaks after some events
// were already emitted. Retrying would replay the emitted content to the user,
// so it is treated as non-retryable.
var errPartialStream = errors.New("stream interrupted after partial output")

func isRetryable(err error) bool {
	if errors.Is(err, errPartialStream) {
		return false
	}
	if err == nil {
		return false
	}
	if le, ok := err.(*LLMError); ok {
		return retryableHTTPStatuses[le.StatusCode]
	}
	// Network errors are retryable
	return true
}

// ─── Message sanitization ──────────────────────────────────

// sanitizeMessages creates a deep copy of messages with proper IDs and ensures
// tool_call_id consistency between assistant tool calls and tool responses.
// This prevents the "An assistant message with 'tool_calls' must be followed
// by tool messages" error from the API.
func sanitizeMessages(messages []ChatMessage) []ChatMessage {
	out := deepCopyMessages(messages)
	out = dropOrphanToolMessages(out)
	out = injectMissingToolResponses(out)
	fixEmptyAssistantContent(out)
	return out
}

func deepCopyMessages(messages []ChatMessage) []ChatMessage {

	if messages == nil {
		return nil
	}
	out := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		out[i] = msg
		// v3.0: 不再生成消息级随机 id —— 请求体必须字节级确定性 (前缀缓存铁律)。
		// ID 字段 omitempty 且恒空 → 不上线。工具调用 id / tool_call_id 是 API
		// 必需字段, 仍按需补齐 (见下)。

		// Deep-copy tool calls and ensure every tool call has valid id and type
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = make([]ToolCall, len(msg.ToolCalls))
			copy(out[i].ToolCalls, msg.ToolCalls)
			for j := range out[i].ToolCalls {
				if out[i].ToolCalls[j].Type == "" {
					out[i].ToolCalls[j].Type = "function"
				}
				if out[i].ToolCalls[j].ID == "" {
					b2 := make([]byte, 8)
					rand.Read(b2)
					out[i].ToolCalls[j].ID = "call_" + hex.EncodeToString(b2)
				}
			}
		} else {
			// Ensure nil slice for clean JSON (omitempty)
			out[i].ToolCalls = nil
		}

		// Deep-copy image parts (base64 字符串不可变, 复制 slice 头防共享即可)
		if len(msg.Images) > 0 {
			out[i].Images = append([]ImagePart(nil), msg.Images...)
		}

		// For tool role messages, ensure tool_call_id is present
		if out[i].Role == "tool" {
			if out[i].ToolCallID == "" {
				b3 := make([]byte, 8)
				rand.Read(b3)
				out[i].ToolCallID = "call_" + hex.EncodeToString(b3)
			}
		}
	}

	return out
}

func dropOrphanToolMessages(out []ChatMessage) []ChatMessage {
	// Remove orphan / misplaced tool messages: DeepSeek rejects any tool message
	// that is NOT a response to the immediately-preceding assistant(tool_calls)
	// block ("Messages with role 'tool' must be a response to a preceding message
	// with 'tool_calls'"). The prior check only matched the id anywhere in the
	// array, so a tool could survive when its block was reordered/truncated
	// upstream — then the API 400'd. Rewritten as a block scan: a tool is valid
	// only if it sits inside the tool-call block opened by the nearest preceding
	// assistant(tool_calls) (with only tool messages in between), and its id
	// matches that block. Anything else is dropped; the placeholder pass below
	// refills genuinely-missing responses.
	for i := 0; i < len(out); i++ {
		if out[i].Role != "tool" {
			continue
		}
		blockStart := -1
		for k := i - 1; k >= 0; k-- {
			if out[k].Role == "assistant" && len(out[k].ToolCalls) > 0 {
				blockStart = k
				break
			}
			if out[k].Role != "tool" {
				break // 前面是 user/普通assistant/其他 → 无合法块
			}
		}
		matched := false
		if blockStart >= 0 {
			for _, tc := range out[blockStart].ToolCalls {
				if tc.ID == out[i].ToolCallID {
					matched = true
					break
				}
			}
		}
		if !matched {
			out = append(out[:i], out[i+1:]...)
			i-- // re-check the shifted position
		}
	}

	return out
}

func injectMissingToolResponses(out []ChatMessage) []ChatMessage {
	// Consistency check: for every assistant message with tool_calls,
	// ensure there are subsequent tool messages with matching tool_call_ids.
	// If a tool response is missing, inject a placeholder to prevent API errors.
	// We iterate in reverse to safely insert without index confusion.
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role == "assistant" && len(out[i].ToolCalls) > 0 {
			// Check if all tool calls have responses between this message
			// and the next non-tool message
			for _, tc := range out[i].ToolCalls {
				found := false
				for k := i + 1; k < len(out); k++ {
					if out[k].Role == "tool" && out[k].ToolCallID == tc.ID {
						found = true
						break
					}
					if out[k].Role != "tool" {
						break
					}
				}
				if !found {
					// Inject placeholder immediately after the last existing tool response
					// or right after the assistant message
					insertAt := i + 1
					for insertAt < len(out) && out[insertAt].Role == "tool" {
						insertAt++
					}
					placeholder := ChatMessage{
						Role:       "tool",
						ToolCallID: tc.ID,
						Content:    "[system] tool result unavailable",
					}
					// 插入位置
					out = append(out[:insertAt], append([]ChatMessage{placeholder}, out[insertAt:]...)...)
				}
			}
		}
	}

	return out
}

func fixEmptyAssistantContent(out []ChatMessage) {
	// Final guard: never send an assistant message with BOTH empty content and
	// empty tool_calls — the API rejects it with HTTP 400
	// ("Invalid assistant message: content or tool_calls must be set").
	// This happens when the model streams only reasoning_content (e.g. R1)
	// and no plain-text content. Prefer reasoning as content; else placeholder.
	for i := range out {
		if out[i].Role == "assistant" && out[i].Content == "" && len(out[i].ToolCalls) == 0 {
			if strings.TrimSpace(out[i].ReasoningContent) != "" {
				out[i].Content = out[i].ReasoningContent
			} else {
				out[i].Content = "[empty response]"
			}
		}
	}
}

// ─── Tool call merging
// ─── Tool call merging ─────────────────────────────────────

func mergeToolCalls(deltas []ToolCall) []ToolCall {
	if len(deltas) == 0 {
		return nil
	}
	type acc struct {
		index int
		id    string
		typ   string
		name  string
		args  strings.Builder
	}
	// Some streaming APIs omit the `index` field entirely — every delta arrives
	// with Index==0. In that case multiple independent tool calls cannot be told
	// apart by index and would be merged into one garbage call. Detect that and
	// group by ID instead.
	distinctIDs := make(map[string]struct{})
	allIndexZero := true
	for _, tc := range deltas {
		if tc.Index != 0 {
			allIndexZero = false
		}
		if tc.ID != "" {
			distinctIDs[tc.ID] = struct{}{}
		}
	}

	if allIndexZero && len(distinctIDs) > 1 {
		byID := make(map[string]*acc)
		order := make([]string, 0, len(distinctIDs))
		for _, tc := range deltas {
			if tc.ID == "" {
				continue // ID-less fragment cannot be grouped — skip
			}
			a, ok := byID[tc.ID]
			if !ok {
				a = &acc{id: tc.ID, index: tc.Index}
				byID[tc.ID] = a
				order = append(order, tc.ID)
			}
			if tc.Type != "" {
				a.typ = tc.Type
			}
			if tc.Function.Name != "" {
				a.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				a.args.WriteString(tc.Function.Arguments)
			}
		}
		result := make([]ToolCall, 0, len(order))
		for _, id := range order {
			a := byID[id]
			typ := a.typ
			if typ == "" {
				typ = "function"
			}
			result = append(result, ToolCall{
				Index: a.index,
				ID:    a.id,
				Type:  typ,
				Function: FunctionCall{
					Name:      a.name,
					Arguments: a.args.String(),
				},
			})
		}
		if len(result) > 0 {
			return result
		}
	}

	byIndex := make(map[int]*acc)
	maxIndex := -1
	for _, tc := range deltas {
		idx := tc.Index
		if idx > maxIndex {
			maxIndex = idx
		}
		if _, ok := byIndex[idx]; !ok {
			byIndex[idx] = &acc{index: idx}
		}
		a := byIndex[idx]
		if tc.ID != "" {
			a.id = tc.ID
		}
		if tc.Type != "" {
			a.typ = tc.Type
		}
		if tc.Function.Name != "" {
			a.name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			a.args.WriteString(tc.Function.Arguments)
		}
	}

	result := make([]ToolCall, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		a, ok := byIndex[i]
		if !ok {
			// Skip gaps — don't leave zero-value entries that break API validation
			continue
		}
		typ := a.typ
		if typ == "" {
			typ = "function"
		}
		id := a.id
		if id == "" {
			// Generate fallback ID — DeepSeek API requires id on every tool call
			b := make([]byte, 8)
			if _, err := rand.Read(b); err != nil {
				id = fmt.Sprintf("call_%d", time.Now().UnixNano())
			} else {
				id = "call_" + hex.EncodeToString(b)
			}
		}
		result = append(result, ToolCall{
			Index: a.index,
			ID:    id,
			Type:  typ,
			Function: FunctionCall{
				Name:      a.name,
				Arguments: a.args.String(),
			},
		})
	}
	return result
}
