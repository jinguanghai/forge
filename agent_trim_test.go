// agent_trim_test.go — 硬裁剪的缓存友好契约。
//
// trimHistory 的判据不是"删够条数"而是"从尾部成对删": 头部前缀必须逐字节稳定,
// 否则 DeepSeek 前缀缓存自 system 之后全量 miss (实测命中率 0%)。
package main

import "testing"

func TestTrimHistory_DefaultMaxWhenUnset(t *testing.T) {
	a := &AgentRunner{cfg: &Config{}, headLen: 1}
	a.history = []ChatMessage{{Role: "system", Content: "s"}}
	for i := 0; i < 30; i++ {
		a.history = append(a.history,
			ChatMessage{Role: "user", Content: "u"},
			ChatMessage{Role: "assistant", Content: "a"})
	}
	if len(a.history) != 61 {
		t.Fatalf("前置: history 应为 61 条, got %d", len(a.history))
	}
	a.trimHistory()
	if len(a.history) > 40 {
		t.Errorf("MaxHistoryMessages<=0 应兜底 40, got %d", len(a.history))
	}
	if a.history[0].Content != "s" {
		t.Error("system 头被删除")
	}
}

func TestTrimHistory_KeepsHeadPrefixStable(t *testing.T) {
	a := &AgentRunner{cfg: &Config{MaxHistoryMessages: 5}, headLen: 1}
	a.history = []ChatMessage{
		{Role: "system", Content: "s"},
		{Role: "user", Content: "u1"}, {Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"}, {Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"}, {Role: "assistant", Content: "a3"},
	}
	a.trimHistory()
	if len(a.history) > 5 {
		t.Errorf("未裁到上限: %d", len(a.history))
	}
	if len(a.history) < 3 || a.history[1].Content != "u1" || a.history[2].Content != "a1" {
		t.Errorf("头部前缀被改动 (应从尾部成对删) —— 前缀缓存会断: %+v", a.history)
	}
}

func TestTrimHistory_NeverDropsHeadBelowOne(t *testing.T) {
	a := &AgentRunner{cfg: &Config{MaxHistoryMessages: 1}, headLen: 0}
	a.history = []ChatMessage{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}
	a.trimHistory()
	if len(a.history) < 1 || a.history[0].Content != "s" {
		t.Errorf("headLen<1 应兜底为 1, system 不得被删: %+v", a.history)
	}
}

func TestTrimHistory_StopsAtHeadOnly(t *testing.T) {
	a := &AgentRunner{cfg: &Config{MaxHistoryMessages: 1}, headLen: 1}
	a.history = []ChatMessage{{Role: "system", Content: "s"}}
	a.trimHistory()
	if len(a.history) != 1 {
		t.Errorf("只剩固定头时不应再删: %+v", a.history)
	}
}

func TestTrimTurnMessages_DropsOnlyOldestToolTurn(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "s"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1"}}},
		{Role: "tool", Content: "r1", ToolCallID: "c1"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c2"}}},
		{Role: "tool", Content: "r2", ToolCallID: "c2"},
	}
	got := trimTurnMessages(msgs, 4)
	if len(got) != 3 {
		t.Fatalf("应删掉最旧一个工具轮 (2 条), got %d 条: %+v", len(got), got)
	}
	if got[0].Content != "s" {
		t.Error("头部被删除")
	}
	if got[1].ToolCalls[0].ID != "c2" {
		t.Errorf("删错了轮次 (应保留较新的 c2): %+v", got)
	}
}
