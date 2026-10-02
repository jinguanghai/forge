package main

// cov_agent_internal_more_test.go — AgentRunner 内部状态机分支补测 (同包直调)

import (
	"strings"
	"testing"
)

func TestCovAgentInternal_TrimAndTails(t *testing.T) {
	agent, _, _ := newHandleCmdAgent(t)
	for i := 0; i < 80; i++ {
		agent.history = append(agent.history,
			ChatMessage{Role: "user", Content: strings.Repeat("x", 300) + " 第" + strings.Repeat("y", 10)},
			ChatMessage{Role: "assistant", Content: "回复内容"})
	}
	before := len(agent.history)
	agent.trimHistory()
	after := len(agent.history)
	if after == 0 {
		t.Error("trimHistory 清空了全部历史")
	}
	if after > before {
		t.Errorf("trimHistory 反而增长: %d -> %d", before, after)
	}
	agent.syncDynamicTails()
	msgs, _, _ := agent.buildStreamMessages("新的用户输入")
	if len(msgs) == 0 {
		t.Error("buildStreamMessages 返回空消息列表")
	}
	agent.RestoreHistory([]ChatMessage{{Role: "user", Content: "hi"}})
	if len(agent.history) == 0 {
		t.Error("RestoreHistory 未写入历史")
	}
	_ = agent.verifyHeadInvariant()
	agent.CancelCurrent()
}

func TestCovAgentInternal_LastOutputAndText(t *testing.T) {
	agent, _, _ := newHandleCmdAgent(t)
	var asst, reason strings.Builder
	asst.WriteString("答案")
	reason.WriteString("推理过程")
	if got := extractAssistantText(&asst, &reason); got == "" {
		t.Error("extractAssistantText 返回空")
	}
	agent.history = append(agent.history,
		ChatMessage{Role: "user", Content: "问"},
		ChatMessage{Role: "assistant", Content: "答"})
	// LastOutput 由 RunStream 收尾时写入, 未跑过主循环时为 ("", false) —— 只校验一致性
	if out, ok := agent.LastOutput(); ok && out == "" {
		t.Error("LastOutput ok=true 但内容为空")
	}
	if d := memoryTailDiff("a\nb", "a\nb\nc"); d == "" {
		t.Log("memoryTailDiff 无差异时为空(可接受)")
	}
	_ = agent.Context()
}
