package main

import (
	"strings"
	"testing"
)

// ─── mergeToolCalls: 流式 tool_call 分片合并 ───

func TestLLMSanitize_MergeToolCalls_ByIndex(t *testing.T) {
	got := mergeToolCalls([]ToolCall{
		{Index: 0, ID: "call_a", Type: "function", Function: FunctionCall{Name: "forge", Arguments: `{"code":"x`}},
		{Index: 0, Function: FunctionCall{Arguments: `"}`}},
		{Index: 1, ID: "call_b", Function: FunctionCall{Name: "python", Arguments: `{}`}},
	})
	if len(got) != 2 {
		t.Fatalf("期望 2 个工具调用, 实际 %d: %+v", len(got), got)
	}
	if got[0].ID != "call_a" || got[0].Function.Name != "forge" || got[0].Function.Arguments != `{"code":"x"}` {
		t.Fatalf("index 0 分片应拼接参数: %+v", got[0])
	}
	if got[0].Type != "function" || got[1].Type != "function" {
		t.Fatalf("空 Type 应补 function: %+v", got)
	}
	if got[1].ID != "call_b" || got[1].Function.Name != "python" {
		t.Fatalf("index 1 应独立: %+v", got[1])
	}
}

// 部分端点不发 index (全为 0) → 必须按 ID 分组, 否则多个调用被并成一坨垃圾。
func TestLLMSanitize_MergeToolCalls_AllIndexZeroGroupsByID(t *testing.T) {
	got := mergeToolCalls([]ToolCall{
		{ID: "call_a", Function: FunctionCall{Name: "forge", Arguments: "{"}},
		{ID: "call_b", Function: FunctionCall{Name: "python", Arguments: "{}"}},
		{ID: "call_a", Function: FunctionCall{Arguments: "}"}},
	})
	if len(got) != 2 {
		t.Fatalf("应按 ID 分 2 组, 实际 %d: %+v", len(got), got)
	}
	if got[0].ID != "call_a" || got[0].Function.Arguments != "{}" {
		t.Fatalf("首组应为 call_a 且参数拼接: %+v", got[0])
	}
	if got[1].ID != "call_b" || got[1].Function.Name != "python" {
		t.Fatalf("次组应为 call_b: %+v", got[1])
	}
}

func TestLLMSanitize_MergeToolCalls_EmptyReturnsNil(t *testing.T) {
	if mergeToolCalls(nil) != nil || mergeToolCalls([]ToolCall{}) != nil {
		t.Fatal("空输入应返回 nil")
	}
}

// index 有洞时不得产出零值条目 (会破坏 API 校验)。
func TestLLMSanitize_MergeToolCalls_SkipsIndexGaps(t *testing.T) {
	got := mergeToolCalls([]ToolCall{
		{Index: 0, ID: "call_a", Function: FunctionCall{Name: "a", Arguments: "{}"}},
		{Index: 5, ID: "call_b", Function: FunctionCall{Name: "b", Arguments: "{}"}},
	})
	if len(got) != 2 {
		t.Fatalf("应跳过 index 1..4 空洞, 实际 %d: %+v", len(got), got)
	}
	if got[0].Index != 0 || got[1].Index != 5 {
		t.Fatalf("index 应保留原值: %+v", got)
	}
}

func TestLLMSanitize_MergeToolCalls_GeneratesFallbackID(t *testing.T) {
	got := mergeToolCalls([]ToolCall{{Index: 0, Function: FunctionCall{Name: "a", Arguments: "{}"}}})
	if len(got) != 1 || !strings.HasPrefix(got[0].ID, "call_") {
		t.Fatalf("缺 ID 必须补 (API 强制要求): %+v", got)
	}
}

// ─── 空 assistant 兜底: 已知 HTTP 400 事故 ───

func TestLLMSanitize_EmptyAssistantUsesReasoning(t *testing.T) {
	out := []ChatMessage{{Role: "assistant", ReasoningContent: "我思考了"}}
	fixEmptyAssistantContent(out)
	if out[0].Content != "我思考了" {
		t.Fatalf("应优先用 reasoning 填充, 实际 %q", out[0].Content)
	}
}

func TestLLMSanitize_EmptyAssistantPlaceholder(t *testing.T) {
	out := []ChatMessage{{Role: "assistant"}}
	fixEmptyAssistantContent(out)
	if out[0].Content != "[empty response]" {
		t.Fatalf("无 reasoning 应填占位符, 实际 %q", out[0].Content)
	}
}

func TestLLMSanitize_AssistantWithToolCallsUntouched(t *testing.T) {
	out := []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}}}
	fixEmptyAssistantContent(out)
	if out[0].Content != "" {
		t.Fatalf("有 tool_calls 即合法, 不得改动: %q", out[0].Content)
	}
}

func TestLLMSanitize_UserMessageUntouched(t *testing.T) {
	out := []ChatMessage{{Role: "user"}}
	fixEmptyAssistantContent(out)
	if out[0].Content != "" {
		t.Fatal("非 assistant 角色不得被填充")
	}
}

// ─── deepCopyMessages ───

func TestLLMSanitize_DeepCopyIsolated(t *testing.T) {
	orig := []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_x", Function: FunctionCall{Name: "forge"}}}}}
	cp := deepCopyMessages(orig)
	cp[0].ToolCalls[0].Function.Name = "changed"
	if orig[0].ToolCalls[0].Function.Name != "forge" {
		t.Fatal("修改副本不得影响入参")
	}
}

func TestLLMSanitize_DeepCopyFillsToolCallType(t *testing.T) {
	orig := []ChatMessage{{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_x"}}}}
	cp := deepCopyMessages(orig)
	if cp[0].ToolCalls[0].Type != "function" {
		t.Fatalf("空 Type 应补 function, 实际 %q", cp[0].ToolCalls[0].Type)
	}
	if orig[0].ToolCalls[0].Type != "" {
		t.Fatal("不得修改入参")
	}
}

func TestLLMSanitize_DeepCopyFillsToolCallID(t *testing.T) {
	cp := deepCopyMessages([]ChatMessage{{Role: "tool"}})
	if !strings.HasPrefix(cp[0].ToolCallID, "call_") {
		t.Fatalf("tool 角色缺 tool_call_id 应补齐, 实际 %q", cp[0].ToolCallID)
	}
}

func TestLLMSanitize_DeepCopyNil(t *testing.T) {
	if deepCopyMessages(nil) != nil {
		t.Fatal("nil 输入应返回 nil")
	}
}

// ─── 孤儿 tool / 缺失响应 ───

func TestLLMSanitize_OrphanToolDropped(t *testing.T) {
	got := dropOrphanToolMessages([]ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "tool", ToolCallID: "call_ghost", Content: "x"},
	})
	if len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("无前置 tool_calls 块的 tool 消息应删除: %+v", got)
	}
}

func TestLLMSanitize_ValidToolKept(t *testing.T) {
	got := dropOrphanToolMessages([]ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}},
		{Role: "tool", ToolCallID: "call_1"},
	})
	if len(got) != 2 {
		t.Fatalf("配对完整的 tool 消息应保留: %+v", got)
	}
}

func TestLLMSanitize_MissingResponseInjected(t *testing.T) {
	got := injectMissingToolResponses([]ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}},
		{Role: "user", Content: "next"},
	})
	if len(got) != 3 {
		t.Fatalf("应补 1 条占位响应, 实际 %d: %+v", len(got), got)
	}
	if got[1].Role != "tool" || got[1].ToolCallID != "call_1" || got[1].Content == "" {
		t.Fatalf("占位响应形状不对: %+v", got[1])
	}
}

func TestLLMSanitize_ExistingResponseNotDuplicated(t *testing.T) {
	got := injectMissingToolResponses([]ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	})
	if len(got) != 2 {
		t.Fatalf("已有响应不应重复注入: %+v", got)
	}
}

// ─── 端到端 ───

func TestLLMSanitize_EndToEnd(t *testing.T) {
	in := []ChatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "u"},
		{Role: "assistant", ReasoningContent: "只有推理"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "r"},
		{Role: "tool", ToolCallID: "call_ghost", Content: "孤儿"},
	}
	got := sanitizeMessages(in)
	if len(got) != 5 {
		t.Fatalf("期望 5 条 (孤儿被删), 实际 %d: %+v", len(got), got)
	}
	if got[2].Content != "只有推理" {
		t.Fatalf("空 assistant 应被填充: %+v", got[2])
	}
	for _, m := range got {
		if m.Role == "tool" && m.ToolCallID == "call_ghost" {
			t.Fatal("孤儿 tool 消息应被删除")
		}
	}
	if in[2].Content != "" {
		t.Fatal("sanitizeMessages 不得修改入参")
	}
}
