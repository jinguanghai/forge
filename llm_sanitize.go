package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
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
