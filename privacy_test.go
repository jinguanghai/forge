package main

import "testing"

// privacy_test.go — 对话原文落盘总开关的判据 (F4 测试镜像)。

func TestPersistConversationDefaultOff(t *testing.T) {
	t.Setenv("FORGE_PERSIST_CONVO", "")
	if persistConversation() {
		t.Fatal("默认必须为 false(隐私优先): 用户原文不应自动落盘")
	}
}

func TestPersistConversationExplicitOn(t *testing.T) {
	t.Setenv("FORGE_PERSIST_CONVO", "1")
	if !persistConversation() {
		t.Fatal("FORGE_PERSIST_CONVO=1 时必须为 true")
	}
}

func TestTurnDetailRedacted(t *testing.T) {
	t.Setenv("FORGE_PERSIST_CONVO", "")
	got := turnDetail("某段用户原文")
	if got == "某段用户原文" || got == "" {
		t.Fatalf("默认必须脱敏且非空, 得到 %q", got)
	}
	t.Setenv("FORGE_PERSIST_CONVO", "1")
	if got := turnDetail("abc"); got != "abc" {
		t.Fatalf("开启落盘时应原样返回, 得到 %q", got)
	}
}
