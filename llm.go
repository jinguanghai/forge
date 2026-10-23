package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// hardContextLimit is DeepSeek V4's real (undocumented) context ceiling:
// 2^20 = 1,048,576 tokens. The server 400s at or above it (measured by
// deepseek-harness probe_6b, 2026-05-09). Local pre-flight check avoids the
// silent off-by-one rejection.
const hardContextLimit = 1_048_576

// sysChanged 全局: doStream 里比对 system 前缀后设置, 流式解析处读取。
// 单会话顺序请求, 无并发竞争 (若有并发需改为 per-request)。
var sysChanged bool

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
				// 故障兜底 (failover): 主路径 DeepSeek 不可用 → MiniMax 兜一次。
				// 三重前提: ①可重试错误(429/5xx/网络; errPartialStream 与 401/400 已被
				// isRetryable 排除) ②用户未取消(ctx 已死则重发无意义) ③MiniMax 可用且非识图。
				// 兜底成功不改变主路径 —— 下次请求仍先走 DeepSeek。
				if isRetryable(err) && ctx.Err() == nil {
					if fe, ok := c.failoverEndpoint(useModel, messages); ok {
						if ferr := c.doStreamTo(ctx, messages, tools, ch, fe.Model, fe); ferr == nil {
							c.setHealthy(true)
							return
						}
					}
				}
				c.setHealthy(false)
				ch <- StreamEvent{Type: "error", Error: fmt.Errorf("LLM request failed after %d attempts: %w", attempt+1, err)}
				return
			}
		}
	}()

	return ch
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

func (c *LLMClient) setHealthy(h bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.healthy = h
}

// resolveEndpoint 决定本次请求的主路径端点 —— 恒为 DeepSeek。
// 模型取 preferModel (调用方 pickModel / 识图模型), 空则回 cfg.Model。
//
// 20261011 (T31 成本复核, 主人拍板): 原"高峰时段切 MiniMax 省钱"已撤销 ——
// MiniMax 单价 ≈ DeepSeek 高峰价且 tokenizer 更费(同文 1.73x), 切换是负收益;
// 高峰时段的正确应对是错峰或接受价格, 不是换厂商。MiniMax 改作故障兜底, 见 failoverEndpoint。
func (c *LLMClient) resolveEndpoint(preferModel string, _ []ChatMessage) Endpoint {
	m := preferModel
	if m == "" {
		m = c.cfg.Model
	}
	return Endpoint{Provider: EndpointDeepSeek, APIKey: c.cfg.APIKey, BaseURL: c.cfg.BaseURL, Model: m}
}

// failoverEndpoint 返回 DeepSeek 请求失败后的兜底端点 (MiniMax)。
// ok=false 的两种情形:
//
//	①MiniMax 三字段不齐备 (未配置兜底, 正常态);
//	②请求含图片 —— MiniMax-M3 不识图, 降级会丢能力, 宁可如实报错。
//
// 第三种情形 (已输出半截内容) 由调用方判定: errPartialStream 不可重试, 自然不兜底。
func (c *LLMClient) failoverEndpoint(_ string, msgs []ChatMessage) (Endpoint, bool) {
	if c.cfg.MiniMaxAPIKey == "" || c.cfg.MiniMaxBaseURL == "" || c.cfg.MiniMaxModel == "" {
		return Endpoint{}, false
	}
	for _, m := range msgs {
		if len(m.Images) > 0 {
			return Endpoint{}, false
		}
	}
	return Endpoint{
		Provider: EndpointMiniMax,
		APIKey:   c.cfg.MiniMaxAPIKey,
		BaseURL:  c.cfg.MiniMaxBaseURL,
		Model:    c.cfg.MiniMaxModel,
	}, true
}
