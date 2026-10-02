// tool_ref_test.go: 超大工具输出"落盘引用"的行为契约。
//
// 阈值边界与落盘内容必须严格: 引用的全部意义是"内容不丢, 只是换了个位置",
// 一旦落盘内容被截断或改名, 模型读回的就成了另一份东西。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolRef_SmallOutputNoSpill(t *testing.T) {
	dir := t.TempDir()
	small := strings.Repeat("x", 100)
	got := goalAnchorRef(small, "任务A", 0, dir)
	want := goalAnchor(small, "任务A", 0)
	if got != want {
		t.Errorf("小输出应原样走 goalAnchor:\n got %q\nwant %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, ".forge", "toolcache")); err == nil {
		t.Error("小输出不应创建 toolcache 目录")
	}
}

func TestToolRef_LargeOutputSpills(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("中", 20000)
	got := goalAnchorRef(big, "任务B", 2, dir)

	if !strings.Contains(got, "工具输出过大") {
		t.Errorf("超阈值应报落盘: %q", got[:120])
	}
	if !strings.Contains(got, "20000 rune") {
		t.Errorf("应报原始长度: %q", got[:120])
	}
	// 20000 - 头2000 - 尾1200 = 16800
	if !strings.Contains(got, "省略 16800 rune") {
		t.Errorf("省略计数不符: %q", got[:200])
	}
	// turn 从 0 起, 展示为第 turn+1 轮
	if !strings.Contains(got, "第3轮") {
		t.Errorf("轮次显示应为 turn+1: %q", got[len(got)-80:])
	}
	if !strings.Contains(got, "任务B") {
		t.Errorf("应保留原始任务锚点: %q", got[len(got)-80:])
	}

	cacheDir := filepath.Join(dir, ".forge", "toolcache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("toolcache 未创建: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应恰好 1 个落盘文件, 实际 %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, "tool_003_") {
		t.Errorf("命名应为 tool_%03d_前缀: %s", 3, name)
	}
	if !strings.HasSuffix(name, ".md") {
		t.Errorf("落盘文件应为 .md: %s", name)
	}
	b, err := os.ReadFile(filepath.Join(cacheDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != big {
		t.Errorf("落盘内容与原文不一致: %d 字节 vs %d 字节", len(b), len(big))
	}
	if !strings.Contains(got, name) {
		t.Errorf("摘要应给出落盘路径: %q", got[:200])
	}
}

func TestToolRef_StoreNaming(t *testing.T) {
	dir := t.TempDir()
	out := strings.Repeat("y", 5000)

	p1 := storeToolOutputRef(out, 0, dir)
	p2 := storeToolOutputRef(out, 0, dir)
	if p1 == "" || p2 == "" {
		t.Fatal("落盘失败")
	}
	// 同内容同轮 → 同名(幂等, 不产生重复文件)
	if p1 != p2 {
		t.Errorf("同内容同轮应同名: %q vs %q", p1, p2)
	}
	// 不同轮 → 不同名
	if p3 := storeToolOutputRef(out, 1, dir); p3 == p1 {
		t.Error("不同轮应产生不同文件名")
	}
	// 不同内容 → 不同名(内容哈希参与命名)
	if p4 := storeToolOutputRef(out+"z", 0, dir); p4 == p1 {
		t.Error("不同内容应产生不同文件名")
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".forge", "toolcache"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("应产生 3 个不同文件, 实际 %d", len(entries))
	}
}

func TestToolRef_UnwritableWorkDir(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// workDir 是文件 → MkdirAll 失败 → 必须返回空串, 不得 panic
	if got := storeToolOutputRef("data", 0, blocker); got != "" {
		t.Errorf("不可写路径应返回空串, 实际 %q", got)
	}
}

func TestToolRef_ThresholdBoundary(t *testing.T) {
	dir := t.TempDir()
	// 恰好等于阈值 → 不落盘(判据是 <=)
	exact := strings.Repeat("z", goalAnchorRefOutputThreshold)
	if _, err := os.Stat(filepath.Join(dir, ".forge", "toolcache")); err == nil {
		t.Fatal("前置条件失败: 目录不应存在")
	}
	_ = goalAnchorRef(exact, "t", 0, dir)
	if _, err := os.Stat(filepath.Join(dir, ".forge", "toolcache")); err == nil {
		t.Error("恰好等于阈值不应落盘")
	}
	// 超一个 rune → 落盘
	over := exact + "z"
	_ = goalAnchorRef(over, "t", 0, dir)
	if _, err := os.Stat(filepath.Join(dir, ".forge", "toolcache")); err != nil {
		t.Error("超过阈值应落盘")
	}
}
