package main

// forge_self_build_test.go — self gate 编译/回滚单元测试 (20261011 F3 拆分配套)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelfRollbackNoteRestoresBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	bak := filepath.Join(dir, "forge.go.bak")
	os.WriteFile(bak, []byte("原版"), 0644)
	os.WriteFile(src, []byte("改坏了的版本"), 0644)

	note := selfRollbackNote(src, bak)
	if !strings.Contains(note, "已自动回滚") {
		t.Fatalf("应报已回滚: %q", note)
	}
	got, _ := os.ReadFile(src)
	if string(got) != "原版" {
		t.Fatalf("源码未被回滚: %q", got)
	}
}

func TestSelfRollbackNoteNoBackup(t *testing.T) {
	// action=build 不备份 -> 无备份时不得报"已回滚"(否则是假证据)
	if note := selfRollbackNote("x.go", ""); note != "" {
		t.Fatalf("无备份应返回空串: %q", note)
	}
}

func TestSelfRollbackNoteUnreadableBackup(t *testing.T) {
	dir := t.TempDir()
	note := selfRollbackNote(filepath.Join(dir, "forge.go"), filepath.Join(dir, "nope.bak"))
	if !strings.Contains(note, "备份不可读") {
		t.Fatalf("备份不可读必须显式报出: %q", note)
	}
}

func TestSelfApplyActionBuildSkipsBackup(t *testing.T) {
	// build 动作只编译不改源码 -> 不得产生备份路径 (否则后续回滚会误写源码)
	f := &Forge{workDir: t.TempDir()}
	bp, res, failed := f.selfApplyAction(filepath.Join(f.workDir, "forge.go"), "", "build", time.Now())
	if failed {
		t.Fatalf("build 动作不应失败: %+v", res)
	}
	if bp != "" {
		t.Fatalf("build 动作不应备份: %q", bp)
	}
}
