// agent_stream_loop_test.go — 消息装配的纯度契约。
//
// buildStreamMessages 必须"读 history 不改 history": 跨轮 reasoning 清理只作用于
// 返回的请求副本。若污染 a.history, 落盘检查点会丢掉 reasoning, 且重放时
// 与线上请求不再逐字节一致 → 前缀缓存断裂。
package main

import "testing"

func TestBuildStreamMessages_AppendsUserTurn(t *testing.T) {
	a := &AgentRunner{cfg: &Config{WorkDir: t.TempDir()}, history: []ChatMessage{
		{Role: "user", Content: "第一问"},
		{Role: "assistant", Content: "答1"},
	}}
	msgs, userContent, _ := a.buildStreamMessages("第二问")
	if len(msgs) != 3 {
		t.Fatalf("期望 history+1 条, got %d", len(msgs))
	}
	if msgs[2].Role != "user" || msgs[2].Content != userContent {
		t.Errorf("末条应为当前 user 轮: %+v", msgs[2])
	}
	if userContent == "" {
		t.Error("userContent 为空")
	}
}

func TestBuildStreamMessages_ClearsReasoningWithoutPollutingHistory(t *testing.T) {
	a := &AgentRunner{cfg: &Config{WorkDir: t.TempDir()}, history: []ChatMessage{
		{Role: "user", Content: "第一问"},
		{Role: "assistant", Content: "答1", ReasoningContent: "思考1"},
	}}
	msgs, _, _ := a.buildStreamMessages("第二问")

	if msgs[1].ReasoningContent != "" {
		t.Errorf("跨轮 reasoning 未清理: %q", msgs[1].ReasoningContent)
	}
	if a.history[1].ReasoningContent != "思考1" {
		t.Errorf("a.history 被污染 —— 检查点会丢 reasoning 且重放不再逐字节一致: %q",
			a.history[1].ReasoningContent)
	}
}

func TestBuildStreamMessages_KeepsReasoningForToolCalls(t *testing.T) {
	a := &AgentRunner{cfg: &Config{WorkDir: t.TempDir()}, history: []ChatMessage{
		{Role: "assistant", Content: "", ReasoningContent: "思考", ToolCalls: []ToolCall{{ID: "c1"}}},
		{Role: "tool", Content: "r", ToolCallID: "c1"},
	}}
	msgs, _, _ := a.buildStreamMessages("下一问")
	if msgs[0].ReasoningContent != "思考" {
		t.Errorf("带 tool_calls 的 assistant 的 reasoning 必须保留: %q", msgs[0].ReasoningContent)
	}
}

func TestBuildStreamMessages_EmptyInputStillAppendsTurn(t *testing.T) {
	a := &AgentRunner{cfg: &Config{WorkDir: t.TempDir()}}
	msgs, _, _ := a.buildStreamMessages("")
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Errorf("空历史时应只追加一条 user: %+v", msgs)
	}
}
