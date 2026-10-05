package main

import (
	"fmt"
	"strings"
)

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
	// claimed[k] = 第 k 条 assistant 消息里已被 tool 响应认领的 tool_calls 下标
	claimed := make(map[int]map[int]bool)
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
					// 确定性补全 (禁 rand): 同输入恒得同一 id → 前缀缓存稳定
					out[i].ToolCalls[j].ID = autoToolCallID(i, j)
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

		// For tool role messages, ensure tool_call_id is present.
		// 关键: 绝不能独立随机生成 —— assistant 侧 tool_calls[j].id 与 tool 侧
		// tool_call_id 必须相等 (API 硬契约)。两侧各自随机 → dropOrphanToolMessages
		// 判为孤儿 → 真实工具结果被换成占位符 (静默数据丢失, 实测 20261004)。
		// 这里回填到「紧邻前序 assistant(tool_calls) 块内尚未被认领的调用」,
		// 与块内 id 同源 (同一条 out[bi].ToolCalls[bj].ID)。
		if out[i].Role == "tool" {
			bi := nearestToolCallBlock(out, i)
			if bi >= 0 {
				if out[i].ToolCallID == "" {
					bj := firstUnclaimedToolCall(out[bi].ToolCalls, claimed[bi])
					if bj >= 0 {
						out[i].ToolCallID = out[bi].ToolCalls[bj].ID
						markClaimed(claimed, bi, bj)
					} else {
						// 块内调用都已有响应 → 多余 tool 消息, 补确定性 id 后交给
						// dropOrphanToolMessages 处理 (无匹配 id 必被删)
						out[i].ToolCallID = autoToolCallID(i, 0)
					}
				} else {
					for j := range out[bi].ToolCalls {
						if out[bi].ToolCalls[j].ID == out[i].ToolCallID {
							markClaimed(claimed, bi, j)
						}
					}
				}
			} else if out[i].ToolCallID == "" {
				// 真孤儿 (无前置块): 补确定性 id 保 API 形状, 随后被 drop 掉
				out[i].ToolCallID = autoToolCallID(i, 0)
			}
		}
	}

	return out
}

// autoToolCallID 生成确定性的工具调用 id。
//
// 铁律 (llm_types.go): 请求体必须字节级确定性 —— DeepSeek 前缀缓存按 token
// 前缀匹配, 任何随机字段都可能是断裂点 (未命中价是命中价 30 倍)。故此处禁用
// crypto/rand: 同一输入恒得同一 id。msgIdx < 0 表示流式合并场景 (无消息下标)。
func autoToolCallID(msgIdx, callIdx int) string {
	if msgIdx < 0 {
		return fmt.Sprintf("call_auto_idx_%d", callIdx)
	}
	return fmt.Sprintf("call_auto_%d_%d", msgIdx, callIdx)
}

// nearestToolCallBlock 返回第 i 条 tool 消息所属 assistant(tool_calls) 块的下标。
// 块定义与 dropOrphanToolMessages 一致: 中间只允许 tool 消息; 否则返回 -1。
func nearestToolCallBlock(out []ChatMessage, i int) int {
	for k := i - 1; k >= 0; k-- {
		if out[k].Role == "assistant" && len(out[k].ToolCalls) > 0 {
			return k
		}
		if out[k].Role != "tool" {
			return -1
		}
	}
	return -1
}

// firstUnclaimedToolCall 返回块内第一个尚未被 tool 响应认领的调用下标 (-1 = 无)。
func firstUnclaimedToolCall(tcs []ToolCall, used map[int]bool) int {
	for j := range tcs {
		if !used[j] {
			return j
		}
	}
	return -1
}

// markClaimed 标记块 bi 内第 j 个调用已被认领。
func markClaimed(claimed map[int]map[int]bool, bi, j int) {
	m := claimed[bi]
	if m == nil {
		m = make(map[int]bool)
		claimed[bi] = m
	}
	m[j] = true
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
		var last *acc
		for _, tc := range deltas {
			var a *acc
			if tc.ID == "" {
				// 真实流式协议里续片只带 index + arguments (不带 id) —— 归属
				// 「最近一个已建 acc」; 只有首个分片就无 id 且无 acc 时才丢弃
				// (无归属线索, 建不出调用)。
				if last == nil {
					continue
				}
				a = last
			} else {
				cur, ok := byID[tc.ID]
				if !ok {
					cur = &acc{id: tc.ID, index: tc.Index}
					byID[tc.ID] = cur
					order = append(order, tc.ID)
				}
				a = cur
				last = cur
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
			// 确定性补全 (API 强制要求 id; 禁 rand 保前缀缓存稳定)
			id = autoToolCallID(-1, a.index)
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
