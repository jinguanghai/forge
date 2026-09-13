package main

// guard_protect_test.go — 受保护目标清单哨兵 (DMAIC M2)
//
// 事故背景(2026-09 审计): 手写 protectedTargets 只覆盖 19/53 个 .go ——
// nosword.go / cache_stats.go / memory_fold.go / main_commands.go 等核心文件全裸奔,
// 且含 3 条磁盘上已不存在的幽灵路径。
// 本哨兵把「保护集必须自动覆盖工作目录全部 .go、且不得含幽灵路径」固化为死程序判定:
// 清单一旦腐化, 测试即失败 —— 不依赖任何人的记性。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 保护集必须覆盖工作目录下每一个 .go 文件(含 _test.go)。
func TestProtectedTargetsCoversAllGoFiles(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, x := range protectedTargets(wd) {
		got[x] = true
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	total := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		total++
		if !got[strings.ToLower(e.Name())] {
			missing = append(missing, e.Name())
		}
	}
	if total == 0 {
		t.Fatal("工作目录下未发现任何 .go 文件, 测试环境异常")
	}
	if len(missing) > 0 {
		t.Fatalf("保护集漏掉 %d/%d 个源码文件: %v", len(missing), total, missing)
	}
	t.Logf("保护集覆盖 %d 个 .go 文件 + 非源码资产", total)
}

// 保护集内的 .go 必须真实存在(防幽灵路径回归)。
func TestProtectedTargetsNoGhostGoPaths(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range protectedTargets(wd) {
		if !strings.HasSuffix(x, ".go") {
			continue
		}
		if _, err := os.Stat(filepath.Join(wd, x)); err != nil {
			t.Errorf("保护集含幽灵源码路径: %s", x)
		}
	}
}

// 关键非源码资产必须始终在保护集内(扫描实现不得把它们漏掉)。
func TestProtectedTargetsKeepsCriticalAssets(t *testing.T) {
	wd, _ := os.Getwd()
	got := map[string]bool{}
	for _, x := range protectedTargets(wd) {
		got[x] = true
	}
	for _, must := range []string{"memory.json", "forge_cache.gob", ".env", ".forge", "defense_system"} {
		if !got[strings.ToLower(must)] {
			t.Errorf("保护集丢失关键资产: %s", must)
		}
	}
}
