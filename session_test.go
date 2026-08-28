package main

// session_test.go — 三期 DMAIC I1 会话隔离测试
// 覆盖: 会话创建 / 检查点隔离 / legacy 兼容 / 事件 session 字段 / 记忆会话过滤

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetSessionState 复位全局会话状态 (测试隔离)
func resetSessionState() {
	setCurrentSession("")
	setEventSession("")
}

func TestCreateSession(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	s, err := createSession(wd)
	if err != nil {
		t.Fatalf("createSession: %v", err)
	}
	if s.ID == "" {
		t.Fatal("会话 id 为空")
	}
	if _, err := os.Stat(filepath.Join(sessionDir(wd, s.ID), "session.json")); err != nil {
		t.Fatalf("session.json 未创建: %v", err)
	}
	if currentSession() != s.ID {
		t.Fatalf("当前会话未切换: got %q want %q", currentSession(), s.ID)
	}
}

func TestCheckpointSessionIsolation(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	// 会话 A: 保存历史
	setCurrentSession("sA")
	if err := os.MkdirAll(filepath.Dir(checkpointPath(wd)), 0755); err != nil {
		t.Fatal(err)
	}
	histA := []ChatMessage{
		{Role: "user", Content: "A 的任务"},
		{Role: "assistant", Content: "A 的回答"},
	}
	a := &AgentRunner{history: histA}
	if err := a.saveCheckpoint(wd); err != nil {
		t.Fatalf("saveCheckpoint A: %v", err)
	}

	// 会话 B: 保存不同历史
	setCurrentSession("sB")
	histB := []ChatMessage{
		{Role: "user", Content: "B 的任务"},
		{Role: "assistant", Content: "B 的回答"},
	}
	b := &AgentRunner{history: histB}
	if err := b.saveCheckpoint(wd); err != nil {
		t.Fatalf("saveCheckpoint B: %v", err)
	}

	// 回 A 读回: 必须还是 A 的历史 (未串味)
	setCurrentSession("sA")
	h, _, err := loadCheckpoint(wd)
	if err != nil {
		t.Fatalf("loadCheckpoint A: %v", err)
	}
	if len(h) != 2 || h[0].Content != "A 的任务" {
		t.Fatalf("会话 A 历史被串味: %+v", h)
	}

	// 文件落位验证
	if _, err := os.Stat(checkpointSessionPath(wd, "sA")); err != nil {
		t.Fatalf("sA checkpoint 不存在: %v", err)
	}
	if _, err := os.Stat(checkpointSessionPath(wd, "sB")); err != nil {
		t.Fatalf("sB checkpoint 不存在: %v", err)
	}
}

func TestLegacyCheckpointPath(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	got := checkpointPath(wd)
	want := filepath.Join(wd, ".forge", "checkpoint.json")
	if got != want {
		t.Fatalf("legacy checkpoint 路径: got %q want %q", got, want)
	}

	// legacy 模式下检查点读写应与 v3.0 一致
	hist := []ChatMessage{{Role: "user", Content: "legacy 任务"}}
	a := &AgentRunner{history: hist}
	if err := a.saveCheckpoint(wd); err != nil {
		t.Fatalf("legacy saveCheckpoint: %v", err)
	}
	h, _, err := loadCheckpoint(wd)
	if err != nil || len(h) != 1 || h[0].Content != "legacy 任务" {
		t.Fatalf("legacy 检查点读写不一致: %v %+v", err, h)
	}
}

func TestSessionEventField(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()
	initEventLog(wd)

	setEventSession("sTest")
	logEvent(EvToolCalled, "test-gate", nil)
	setEventSession("")
	logEvent(EvToolCalled, "legacy-gate", nil)

	evs := lastEvents(10)
	if len(evs) < 2 {
		t.Fatalf("事件数不足: %d", len(evs))
	}
	if evs[0].Session != "sTest" {
		t.Fatalf("会话事件 session 字段: got %q want %q", evs[0].Session, "sTest")
	}
	if evs[1].Session != "" {
		t.Fatalf("legacy 事件 session 应为空: got %q", evs[1].Session)
	}
}

func TestRecallMemorySessionFilter(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	mem := map[string]interface{}{
		"key_findings": []interface{}{
			map[string]interface{}{"title": "全局经验", "content": "DeepSeek 前缀缓存全局锚点 20260817"},
			map[string]interface{}{"title": "会话A经验", "content": "会话A的专属经验 20260817", "session": "sA"},
			map[string]interface{}{"title": "会话B经验", "content": "会话B的专属经验 20260817", "session": "sB"},
		},
	}
	data, err := json.Marshal(mem)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveMemory(wd, data); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// 会话 A: 应召回全局 + A, 不含 B
	setCurrentSession("sA")
	block, n := RecallMemory(wd, "经验", 10)
	if n == 0 {
		t.Fatal("会话A应召回经验")
	}
	if strings.Contains(block, "会话B") {
		t.Fatalf("会话A召回串入会话B内容: %s", block)
	}
	if !strings.Contains(block, "会话A") {
		t.Fatalf("会话A应包含本会话经验: %s", block)
	}

	// legacy 模式: 全部召回
	setCurrentSession("")
	_, n2 := RecallMemory(wd, "经验", 10)
	if n2 < 3 {
		t.Fatalf("legacy 应召回全部 3 条, got %d", n2)
	}
}
