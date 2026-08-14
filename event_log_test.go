package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEventLogRoundTrip(t *testing.T) {
	work := t.TempDir()
	initEventLog(work)
	logEvent(EvTurnStarted, "测试任务", nil)
	logEvent(EvToolCalled, "python", map[string]string{"lang": "python"})
	logEvent(EvToolResult, "python", map[string]interface{}{"ok": true, "duration_ms": 5})

	evs := lastEvents(0)
	if len(evs) != 3 {
		t.Fatalf("应3条事件, got %d", len(evs))
	}
	if evs[0].Type != EvTurnStarted || evs[1].Type != EvToolCalled || evs[2].Type != EvToolResult {
		t.Errorf("事件类型顺序异常: %s %s %s", evs[0].Type, evs[1].Type, evs[2].Type)
	}
	evs2 := lastEvents(2)
	if len(evs2) != 2 || evs2[0].Type != EvToolCalled {
		t.Errorf("lastEvents(2) 异常: %d 条, 首条=%s", len(evs2), evs2[0].Type)
	}
}

func TestEventLogAppendOnly(t *testing.T) {
	work := t.TempDir()
	initEventLog(work)
	path := filepath.Join(work, ".forge", "events.jsonl")
	logEvent(EvMemoryUpdate, "m1", nil)
	b1, _ := os.ReadFile(path)
	logEvent(EvMemoryUpdate, "m2", nil)
	b2, _ := os.ReadFile(path)
	if len(b2) <= len(b1) {
		t.Errorf("事件日志应只追加: len1=%d len2=%d", len(b1), len(b2))
	}
	initEventLog(work) // 重复 init 不应清空
	logEvent(EvError, "e1", nil)
	b3, _ := os.ReadFile(path)
	if len(b3) <= len(b2) {
		t.Errorf("重复 init 不应清空日志")
	}
}
