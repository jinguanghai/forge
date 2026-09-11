package main

import (
	"testing"
)

// TestDetectUnverifiedClaim: 虚报检测 gate 核心正反路径。
func TestDetectUnverifiedClaim(t *testing.T) {
	cases := []struct {
		name     string
		asst     string
		messages []ChatMessage
		want     bool
	}{
		{
			name: "完成态声称 + 无工具证据 -> 虚报",
			asst: "已完成，commit 2d1d843 已提交，构建通过，工作区 clean",
			messages: []ChatMessage{
				{Role: "user", Content: "帮我提交"},
			},
			want: true,
		},
		{
			name: "完成态声称 + 有工具证据 -> 放行",
			asst: "已完成，commit 2d1d843 已提交，构建通过",
			messages: []ChatMessage{
				{Role: "user", Content: "帮我提交"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "1", Function: FunctionCall{Name: ForgeToolName}}}},
				{Role: "tool", ToolCallID: "1", Content: "ok"},
			},
			want: false,
		},
		{
			name: "有工具证据(role=tool) -> 放行",
			asst: "已验证通过，测试全绿",
			messages: []ChatMessage{
				{Role: "tool", ToolCallID: "x", Content: "PASS"},
			},
			want: false,
		},
		{
			name: "纯思维结论(无操作性词) -> 不误伤",
			asst: "已完成分析，结论是该方案正确",
			messages: []ChatMessage{
				{Role: "user", Content: "分析一下"},
			},
			want: false,
		},
		{
			name: "普通对话(无完成词) -> 不误伤",
			asst: "好的，我来查看一下当前状态",
			messages: []ChatMessage{
				{Role: "user", Content: "hi"},
			},
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectUnverifiedClaim(c.asst, c.messages)
			if got != c.want {
				t.Errorf("detectUnverifiedClaim(%q) = %v, want %v", c.asst, got, c.want)
			}
		})
	}
}

// TestHasToolEvidence: 工具证据识别。
func TestHasToolEvidence(t *testing.T) {
	if !hasToolEvidence([]ChatMessage{{Role: "tool", ToolCallID: "a"}}) {
		t.Errorf("role=tool 应识别为有证据")
	}
	if !hasToolEvidence([]ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "b"}}}}) {
		t.Errorf("assistant 带 tool_calls 应识别为有证据")
	}
	if hasToolEvidence([]ChatMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "ok"}}) {
		t.Errorf("纯对话不应识别为有工具证据")
	}
}
