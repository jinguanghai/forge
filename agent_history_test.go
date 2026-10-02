// agent_history_test.go — 检查点往返与固定头不变式。
//
// 核心断言: restoreHistoryLocked 重建固定头后 verifyHeadInvariant 必须为真。
// v3.1 血案 (固定头被重写 → 前缀缓存全断) 由这一对函数焊死, 此前无直接用例。
package main

import (
	"os"
	"testing"
)

func newHistAgent(dir string) *AgentRunner {
	return &AgentRunner{cfg: &Config{WorkDir: dir}}
}

func TestSaveLoadCheckpoint_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := newHistAgent(dir)
	a.history = []ChatMessage{
		{Role: "user", Content: "你好"},
		{Role: "assistant", Content: "在"},
	}
	if err := a.saveCheckpoint(dir); err != nil {
		t.Fatalf("saveCheckpoint: %v", err)
	}
	got, savedAt, err := loadCheckpoint(dir)
	if err != nil {
		t.Fatalf("loadCheckpoint: %v", err)
	}
	if len(got) != 2 || got[0].Content != "你好" || got[1].Content != "在" {
		t.Errorf("往返后历史不一致: %+v", got)
	}
	if savedAt == "" {
		t.Error("saved_at 为空")
	}
}

func TestSaveCheckpoint_EmptyWorkDirIsNoop(t *testing.T) {
	a := newHistAgent("")
	a.history = []ChatMessage{{Role: "user", Content: "x"}}
	if err := a.saveCheckpoint(""); err != nil {
		t.Errorf("空 workDir 应静默返回 nil, got %v", err)
	}
}

func TestLoadCheckpoint_MissingFileErrors(t *testing.T) {
	if _, _, err := loadCheckpoint(t.TempDir()); err == nil {
		t.Error("检查点不存在时应报错")
	}
	if _, _, err := loadCheckpoint(""); err == nil {
		t.Error("空 workDir 应报错")
	}
}

func TestRestoreHistoryLocked_SkipsOldFixedHeadBlocks(t *testing.T) {
	dir := t.TempDir()
	a := newHistAgent(dir)
	old := []ChatMessage{
		{Role: "system", Content: "旧 system 全文"},
		{Role: "user", Content: "<memory_context>\n旧记忆\n</memory_context>"},
		{Role: "user", Content: "<folded_archive_index>\n旧索引\n</folded_archive_index>\n展开"},
		{Role: "user", Content: "历史正文"},
		{Role: "assistant", Content: "回答"},
	}
	a.restoreHistoryLocked(old)

	for _, m := range a.history {
		if m.Content == "旧 system 全文" {
			t.Error("旧固定头未跳过 —— 恢复会造成重复注入")
		}
	}
	if !a.verifyHeadInvariant() {
		t.Error("恢复后固定头不变式被破坏")
	}
	tail := a.history[a.headLen:]
	if len(tail) != 2 || tail[0].Content != "历史正文" || tail[1].Content != "回答" {
		t.Errorf("历史正文未完整保留: %+v", tail)
	}
}

func TestRestoreHistoryLocked_SetsHeadLenToHeadSize(t *testing.T) {
	a := newHistAgent(t.TempDir())
	a.restoreHistoryLocked(nil)
	head := a.buildFixedHead()
	if a.headLen != len(head) {
		t.Errorf("headLen=%d, buildFixedHead 长度=%d", a.headLen, len(head))
	}
	if len(a.history) != len(head) {
		t.Errorf("空历史恢复后 history 应恰为固定头: %d vs %d", len(a.history), len(head))
	}
}

func TestVerifyHeadInvariant_DetectsTamperedHead(t *testing.T) {
	a := newHistAgent(t.TempDir())
	a.restoreHistoryLocked(nil)
	if !a.verifyHeadInvariant() {
		t.Fatal("刚恢复的固定头应为真 (基准)")
	}
	a.history[0].Content = "被篡改"
	if a.verifyHeadInvariant() {
		t.Error("头部被篡改却报真 —— 守卫失效")
	}
}

func TestVerifyHeadInvariant_DetectsHeadLenDrift(t *testing.T) {
	a := newHistAgent(t.TempDir())
	a.restoreHistoryLocked(nil)
	a.headLen++
	if a.verifyHeadInvariant() {
		t.Error("headLen 漂移未检出 —— 守卫失效")
	}
}

func TestVerifyHeadInvariant_DetectsShrunkHistory(t *testing.T) {
	a := newHistAgent(t.TempDir())
	a.restoreHistoryLocked(nil)
	a.history = a.history[:0]
	if a.verifyHeadInvariant() {
		t.Error("history 短于 headLen 未检出")
	}
}

func TestBuildFixedHead_CachesFirstValue(t *testing.T) {
	dir := t.TempDir()
	writeTestMemory(t, dir, `{"identity":"first","active_task":"t1"}`)
	a := newHistAgent(dir)
	first := a.buildFixedHead()
	if !a.headCached {
		t.Fatal("首次构建后 headCached 应为真")
	}
	// 磁盘上的记忆改了, 但固定头必须仍是首值 (变更走 syncDynamicTails 尾部 diff)
	if err := os.WriteFile(memoryFilePath(dir), []byte(`{"identity":"second"}`), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	second := a.buildFixedHead()
	if len(first) != len(second) {
		t.Fatalf("固定头长度变化: %d -> %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Content != second[i].Content {
			t.Errorf("固定头第 %d 条被重读改写 —— 前缀缓存会断", i)
		}
	}
}
