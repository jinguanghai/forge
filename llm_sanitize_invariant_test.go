package main

import (
	"encoding/json"
	"testing"
)

// ─── 不变式哨兵: tool_call_id 同源 / 请求体确定性 / 流式续片归属 ───
//
// 背景 (20261004 实测): llm_sanitize.go 有两处静默缺陷, 而旧用例全绿、分支零覆盖。
//   A. assistant 侧与 tool 侧各自用 crypto/rand 补 id → 两侧不相等 →
//      dropOrphanToolMessages 判孤儿删除 → 真实工具结果被换成
//      "[system] tool result unavailable" (静默数据丢失); 且同输入两次输出字节不同
//      → 违反 llm_types.go 的前缀缓存铁律 (未命中价是命中价 30 倍)。
//   B. 无 index 的流式端点里, 不带 id 的 arguments 续片被 continue 丢弃 → args 截断。
// 本文件把「探针实测」转正为常驻判据 (公理三: 不可执行的约束等于不存在)。

// 缺陷 A: 两侧 id 必须同源, 真实工具结果不得被丢。
func TestLLMSanitizeInvariant_EmptyIDsStayPaired(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{Type: "function", Function: FunctionCall{Name: "forge", Arguments: `{"action":"run"}`}}}},
		{Role: "tool", Content: "REAL_RESULT_XYZ"},
	}
	got := sanitizeMessages(in)
	if len(got) != 3 {
		t.Fatalf("真实工具结果不得被丢弃, 期望 3 条, 实际 %d: %+v", len(got), got)
	}
	if got[2].Role != "tool" || got[2].Content != "REAL_RESULT_XYZ" {
		t.Fatalf("真实工具结果被替换: %+v", got[2])
	}
	if got[1].ToolCalls[0].ID == "" || got[1].ToolCalls[0].ID != got[2].ToolCallID {
		t.Fatalf("两侧 id 必须同源相等: tc.id=%q tool_call_id=%q",
			got[1].ToolCalls[0].ID, got[2].ToolCallID)
	}
}

// 缺陷 A-prime: 前缀缓存铁律 —— 同输入两次 sanitize 必须字节相同 (禁 rand)。
func TestLLMSanitizeInvariant_DeterministicBytes(t *testing.T) {
	in := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "forge", Arguments: "{}"}}}},
		{Role: "tool", Content: "r"},
	}
	a, err := json.Marshal(sanitizeMessages(in))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(sanitizeMessages(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("同输入两次输出不同 (前缀缓存断裂点):\n a=%s\n b=%s", a, b)
	}
}

// 结构断言: 输出里每条 tool 消息的 tool_call_id 必须能在「紧邻前序
// assistant(tool_calls) 块」内找到 —— 否则 API 直接 400。
func TestLLMSanitizeInvariant_EveryToolIDMatchedInBlock(t *testing.T) {
	cases := [][]ChatMessage{
		{
			{Role: "assistant", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "a"}}}},
			{Role: "tool", Content: "r"},
		},
		{
			{Role: "assistant", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "a"}}, {Function: FunctionCall{Name: "b"}}}},
			{Role: "tool", Content: "r1"},
			{Role: "tool", Content: "r2"},
		},
		{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_x", Function: FunctionCall{Name: "a"}}}},
			{Role: "tool", Content: "r"},
		},
		{
			{Role: "assistant", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "a"}}}},
			{Role: "tool", ToolCallID: "call_y", Content: "r"},
		},
	}
	for ci, in := range cases {
		got := sanitizeMessages(in)
		for i := range got {
			if got[i].Role != "tool" {
				continue
			}
			bi := nearestToolCallBlock(got, i)
			if bi < 0 {
				t.Fatalf("case %d: 输出的 tool 消息无前置块 (本应被 drop): %+v", ci, got)
			}
			found := false
			for _, tc := range got[bi].ToolCalls {
				if tc.ID != "" && tc.ID == got[i].ToolCallID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("case %d: tool_call_id %q 在块 %d 内无匹配 (API 会 400): %+v",
					ci, got[i].ToolCallID, bi, got)
			}
		}
	}
}

// 块内调用都已有响应 → 多余 tool 消息必须被 drop (不得污染请求体)。
func TestLLMSanitizeInvariant_SurplusToolDropped(t *testing.T) {
	got := sanitizeMessages([]ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "a"}}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		{Role: "tool", Content: "surplus"},
	})
	for _, m := range got {
		if m.Role == "tool" && m.Content == "surplus" {
			t.Fatalf("多余 tool 消息应被删除: %+v", got)
		}
	}
}

// 缺失响应要插在「已有响应之后」(而非紧跟 assistant), 否则打断 tool 块。
func TestLLMSanitizeInvariant_MissingResponseAfterExisting(t *testing.T) {
	got := injectMissingToolResponses([]ChatMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1"}, {ID: "call_2"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "r1"},
		{Role: "user", Content: "next"},
	})
	if len(got) != 4 {
		t.Fatalf("应补 1 条占位响应, 期望 4 条, 实际 %d: %+v", len(got), got)
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call_2" {
		t.Fatalf("占位响应应插在已有 tool 之后: %+v", got)
	}
}

// 缺陷 B: 无 index 端点里不带 id 的 arguments 续片必须归到最近的调用上。
func TestLLMSanitizeInvariant_MergeIDLessContinuationKeepsArgs(t *testing.T) {
	got := mergeToolCalls([]ToolCall{
		{Index: 0, ID: "call_1", Type: "function", Function: FunctionCall{Name: "forge", Arguments: `{"action":"run",`}},
		{Index: 0, Function: FunctionCall{Arguments: `"code":"print(1)"}`}},
		{Index: 0, ID: "call_2", Type: "function", Function: FunctionCall{Name: "forge", Arguments: `{"action":"run",`}},
		{Index: 0, Function: FunctionCall{Arguments: `"code":"print(2)"}`}},
	})
	if len(got) != 2 {
		t.Fatalf("期望 2 个调用, 实际 %d: %+v", len(got), got)
	}
	want := []string{`{"action":"run","code":"print(1)"}`, `{"action":"run","code":"print(2)"}`}
	for i, tc := range got {
		if !json.Valid([]byte(tc.Function.Arguments)) {
			t.Fatalf("调用[%d] args 被截断 (非法 JSON): %q", i, tc.Function.Arguments)
		}
		if tc.Function.Arguments != want[i] {
			t.Fatalf("调用[%d] 续片归属错: %q (期望 %q)", i, tc.Function.Arguments, want[i])
		}
	}
}

// 首个分片就无 id 且无 acc → 无归属线索, 跳过而不产出零值调用。
func TestLLMSanitizeInvariant_MergeOrphanFragmentSkipped(t *testing.T) {
	got := mergeToolCalls([]ToolCall{
		{Index: 0, Function: FunctionCall{Arguments: `{"junk":`}},
		{Index: 0, ID: "call_1", Function: FunctionCall{Name: "a", Arguments: "{}"}},
		{Index: 0, ID: "call_2", Function: FunctionCall{Name: "b", Arguments: "{}"}},
	})
	if len(got) != 2 || got[0].ID != "call_1" || got[1].ID != "call_2" {
		t.Fatalf("无归属片段应被跳过: %+v", got)
	}
}

// 合并 fallback id 也必须确定 (禁 rand)。
func TestLLMSanitizeInvariant_MergeFallbackIDDeterministic(t *testing.T) {
	in := []ToolCall{{Index: 3, Function: FunctionCall{Name: "a", Arguments: "{}"}}}
	a := mergeToolCalls(in)
	b := mergeToolCalls(in)
	if len(a) != 1 || a[0].ID == "" {
		t.Fatalf("缺 id 必须补 (API 强制要求): %+v", a)
	}
	if a[0].ID != b[0].ID {
		t.Fatalf("fallback id 必须确定: %q vs %q", a[0].ID, b[0].ID)
	}
}
