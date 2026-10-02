package main

// cov_memory_fold_more_test.go — 记忆折叠/展开 分支补测 (全部在临时 workDir 内)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCovFold_FilePathAndStamp(t *testing.T) {
	wd := t.TempDir()
	p := memoryFilePath(wd)
	if !strings.HasPrefix(p, wd) {
		t.Errorf("memoryFilePath 未落在 workDir 内: %s", p)
	}
	if s := nowStamp(); s == "" {
		t.Error("nowStamp 为空")
	}
	if got := orDash(""); got == "" {
		t.Error("orDash(\"\") 为空")
	}
	if got := orDash("x"); got != "x" {
		t.Errorf("orDash(\"x\")=%q", got)
	}
	if got := truncateCN("中文很长的文本内容", 3); displayWidth(got) > 3*2+2 {
		t.Errorf("truncateCN 未截断: %q", got)
	}
}

func TestCovFold_Lifecycle(t *testing.T) {
	wd := t.TempDir()
	// updateFoldedItems 以 memory.json 为唯一真相源: 缺文件即报错(设计如此), 先落最小记忆文件
	if err := os.WriteFile(memoryFilePath(wd), []byte(`{"identity":"cov-test","folded_memory":{"items":[{}]}}`), 0644); err != nil {
		t.Fatal(err)
	}
	items, err := foldedItems(wd)
	if err != nil {
		t.Logf("空目录 foldedItems: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("预置 1 项应回读为 1, got %d", len(items))
	}
	// 护栏分支: 增删条目必须被拒绝 (折叠记忆只允许就地修改)
	if err := updateFoldedItems(wd, func(in []FoldedItem) []FoldedItem {
		return append(in, FoldedItem{})
	}); err == nil {
		t.Error("updateFoldedItems 允许增删条目 —— 护栏失效")
	}
	// 正常分支: 保持数量不变
	if err := updateFoldedItems(wd, func(in []FoldedItem) []FoldedItem { return in }); err != nil {
		t.Fatalf("updateFoldedItems(不改数量): %v", err)
	}
	items, err = foldedItems(wd)
	if err != nil {
		t.Fatalf("回读 foldedItems: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("回读应有 1 项, got %d", len(items))
	}
	if it := findFoldedItem(items, ""); it == nil && len(items) > 0 {
		t.Log("findFoldedItem 空名未命中(可接受)")
	}
	if s := listFoldedText(items); s == "" && len(items) > 0 {
		t.Log("listFoldedText 空(可接受)")
	}
	_ = ListFoldedTasks(wd)
	recordUnfold(wd, "")
	MarkHinted(wd, "")
	if _, err := UnfoldPreview(wd, "不存在"); err != nil {
		t.Logf("UnfoldPreview 未命中: %v", err)
	}
	if _, err := UnfoldDeep(wd, "不存在"); err != nil {
		t.Logf("UnfoldDeep 未命中: %v", err)
	}
	_ = FindRelevantFold(wd, "随便一句输入")
	_ = MemDiagMetrics(wd)
}

func TestCovFold_MatchCmd(t *testing.T) {
	for _, s := range []string{"展开X", "深入X", "展开", "普通文本", "", "展开<名称>"} {
		kind, name := matchFoldUnfoldCmd(s)
		t.Logf("matchFoldUnfoldCmd(%q) = (%d, %q)", s, kind, name)
	}
}

func TestCovFold_MemoryFileUnderWorkDir(t *testing.T) {
	wd := t.TempDir()
	// 记录文件路径必须在 workDir 内, 不得回落到真实工作目录
	p := memoryFilePath(wd)
	if filepath.Dir(p) == "" || !strings.Contains(p, filepath.Base(wd)) {
		t.Errorf("memoryFilePath 越界: %s", p)
	}
}
