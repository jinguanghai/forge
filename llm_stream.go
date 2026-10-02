package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

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

	// 提供商路由: 解析本次请求端点 (MiniMax 高峰省钱)
	// 必须在 Thinking 参数之前解析: MiniMax 与 DeepSeek 的思考参数协议不同 ——
	// MiniMax = thinking.type:adaptive (且不认 reasoning_effort);
	// DeepSeek = thinking.type:enabled + reasoning_effort。
	ep := c.resolveEndpoint(model, messages)
	reqBody.Model = ep.Model

	// Thinking 模式显式声明 (deepseek-harness finding #1):
	// deepseek-flash / deepseek-v4-pro 默认 thinking=enabled —— 若用户关闭 ShowReasoning 而
	// 不显式传 thinking:disabled, 服务端仍按默认开启 → 平凡提示也烧 ~30 reasoning
	// tokens (账单 2-5 倍)。因此两种状态都显式下发。
	if c.cfg.ShowReasoning {
		if ep.Provider == EndpointMiniMax {
			// MiniMax-M3 走 adaptive 自适应思考; reasoning_effort 为 DeepSeek 专属, 不下发
			reqBody.Thinking = &ThinkingConfig{Type: "adaptive"}
		} else {
			reqBody.Thinking = &ThinkingConfig{Type: "enabled"}
			// V4-Pro 思考强度 (2026-08 新增 low/high/max): 平凡任务用 low 省输出token(输出计价),
			// 复杂任务用 high/max。仅在思考开启时下发, 避免 API 400。
			if c.cfg.ReasoningEffort != "" {
				reqBody.ReasoningEffort = c.cfg.ReasoningEffort
			}
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

	apiURL := strings.TrimRight(ep.BaseURL, "/") + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.APIKey)
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
			st.sawDone = true
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

	// 流结束但既无 [DONE] 也无 finish_reason = 连接被中途掐断 (代理超时/服务端断开)。
	// 此时内容是不完整的: 不能发 done 让上层当成完整回复, 必须按断流上报,
	// 上层按错误类型分流: 零输出(errStreamTruncated)可重试重发(前缀未变, 缓存仍命中);
	// 已输出半截(errPartialStream)不可重试——重发会造成重复输出, 只上报错误。
	if !st.sawDone && !st.sawFinish {
		if st.sentEvents > 0 {
			return errPartialStream
		}
		return errStreamTruncated
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
	// sawDone / sawFinish 是"流是否正常收尾"的两个独立证据 (20260923 断流续写):
	// 此前收尾兜底不检查任何证据 -> 连接被中途掐断(无 [DONE] 也无 finish_reason)
	// 时仍发 done 事件, agent 把半截回复当成完整回复(静默截断, 最隐蔽的失败)。
	// 双判据而非只看 [DONE]: 兼容端点可能不发 [DONE] 但发 finish_reason,
	// 只看 [DONE] 会把正常流误判为截断。
	sawDone   bool
	sawFinish bool
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
	if choice.FinishReason != "" {
		st.sawFinish = true
	}
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
