package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 端到端: 审计 → 快照 → (重启) → 新进程报告 → 事件日志
func TestEndToEndUpgradeFlow(t *testing.T) {
	work := t.TempDir()
	os.MkdirAll(filepath.Join(work, ".forge", "forge-tools"), 0755)
	os.WriteFile(filepath.Join(work, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(work, "memory.json"), []byte("{}"), 0644)

	// ① 模拟 /upgrade: 写审计 + 快照
	ts := "20260812_999999"
	if err := writeUpgradeAudit(work, ts, "upgrade 自我修改安全三件套"); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if err := createCheckpoint(work, ts, "upgrade"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	// ② 模拟新进程启动: 事件日志初始化
	initEventLog(work)
	logEvent(EvTurnStarted, "新进程启动", nil)
	// ③ 模拟 reportLastUpgrade: 应打印报告并标记 done
	reportLastUpgrade(work)
	a, _ := latestAudit(work)
	if a == nil || a.Status != "done" {
		t.Fatalf("审计应标记 done, got %+v", a)
	}
	// ④ 事件日志应含 self_restart
	evs := lastEvents(0)
	found := false
	for _, e := range evs {
		if e.Type == EvSelfRestart {
			found = true
		}
	}
	if !found {
		t.Fatalf("事件日志应含 self_restart, got %d 条", len(evs))
	}
	// ⑤ 报告已打印到测试日志 (可见"上次自改报告")
}
