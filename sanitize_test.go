package main

import "testing"

func TestSanitizeEmptyAssistant(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "", ReasoningContent: "deep thinking..."},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: FunctionCall{Name: "forge", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	out := sanitizeMessages(msgs)
	type check struct {
		idx  int
		want string
	}
	checks := []check{{2, "deep thinking..."}, {3, "[empty response]"}, {4, ""}}
	for _, c := range checks {
		if out[c.idx].Content != c.want {
			t.Errorf("idx %d: got %q want %q", c.idx, out[c.idx].Content, c.want)
		}
	}
	if len(out[4].ToolCalls) != 1 || out[4].ToolCalls[0].ID != "call_1" {
		t.Errorf("tool_calls assistant 被破坏: %+v", out[4])
	}
}
