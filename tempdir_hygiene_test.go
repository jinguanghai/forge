package main

// tempdir_hygiene_test.go — .forge-temp 启动清空(5S 出口约定)的测试。
//
// 铁律: 本文件所有用例一律用 t.TempDir() 当 workDir, 绝不碰生产 D:\forge\.forge-temp。
// 理由(实测约束): NewForge 有 17 个测试调用点, 其中多个以 D:\forge 为 workDir;
// 若用例直接操作生产临时目录, 会与 browser_gate_test.go 写截图等用例互踩。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func mkTempWorkDir(t *testing.T) (string, string) {
	t.Helper()
	wd := t.TempDir()
	dir := filepath.Join(wd, ForgeTempDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("建 .forge-temp 失败: %v", err)
	}
	return wd, dir
}

func TestCleanupWorkTempDir_EmptyDir(t *testing.T) {
	wd, dir := mkTempWorkDir(t)
	cleanupWorkTempDir(wd)
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("目录本身应保留: %v", err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("应为空, 实得 %d 项", len(ents))
	}
}

func TestCleanupWorkTempDir_RemovesFilesAndDirs(t *testing.T) {
	wd, dir := mkTempWorkDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.tmp", "sub/b.txt", "sub/deep/c.bin"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte("xx"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(wd, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanupWorkTempDir(wd)

	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("应清空(含嵌套目录), 残留 %d 项", len(ents))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("目录本身应保留: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("workDir 下非 .forge-temp 的文件不得被碰: %v", err)
	}
}

func TestCleanupWorkTempDir_MissingDirCreated(t *testing.T) {
	wd := t.TempDir()
	dir := filepath.Join(wd, ForgeTempDir)
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("前置条件失败: 目录不应存在")
	}
	cleanupWorkTempDir(wd)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Errorf("应建出目录使「恒存在」成立: err=%v", err)
	}
}

// 占用中的条目必须跳过且不阻塞启动(同 hygiene_redcard.py 的 is_locked 语义)。
func TestCleanupWorkTempDir_LockedEntrySkipped(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 独占打开语义")
	}
	wd, dir := mkTempWorkDir(t)
	lockedPath := filepath.Join(dir, "locked.log")
	f, err := os.OpenFile(lockedPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if os.Remove(lockedPath) == nil {
		t.Skip("本平台允许删除已打开文件, 锁定分支无法构造")
	}
	free := filepath.Join(dir, "free.tmp")
	if err := os.WriteFile(free, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanupWorkTempDir(wd)

	if _, err := os.Stat(lockedPath); err != nil {
		t.Errorf("被占用条目应保留(不阻塞启动即可): %v", err)
	}
	if _, err := os.Stat(free); err == nil {
		t.Error("未被占用的条目应被清掉")
	}
}

// 熔断: 体量异常 = 资产被错放的信号, 此时只留痕不删除。
func TestCleanupWorkTempDir_BreakerSkipsOversize(t *testing.T) {
	oldN, oldB := tempDirMaxEntries, tempDirMaxBytes
	defer func() { tempDirMaxEntries, tempDirMaxBytes = oldN, oldB }()

	wd, dir := mkTempWorkDir(t)
	for _, n := range []string{"1.tmp", "2.tmp", "3.tmp"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	tempDirMaxEntries, tempDirMaxBytes = 2, 1<<20 // 项数超限
	cleanupWorkTempDir(wd)
	if ents, _ := os.ReadDir(dir); len(ents) != 3 {
		t.Errorf("项数熔断应跳过清理, 实得 %d 项", len(ents))
	}

	tempDirMaxEntries, tempDirMaxBytes = 2000, 4 // 字节超限(3 字节? 上面每份 1 字节)
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), []byte(strings.Repeat("x", 64)), 0644); err != nil {
		t.Fatal(err)
	}
	cleanupWorkTempDir(wd)
	if ents, _ := os.ReadDir(dir); len(ents) != 4 {
		t.Errorf("字节熔断应跳过清理, 实得 %d 项", len(ents))
	}
}

func TestDirTreeSize_Recursive(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, "a", "b"), 0755); err != nil {
		t.Fatal(err)
	}
	for p, n := range map[string]int{"x.bin": 10, "a/y.bin": 20, "a/b/z.bin": 30} {
		if err := os.WriteFile(filepath.Join(wd, filepath.FromSlash(p)), []byte(strings.Repeat("q", n)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if got := dirTreeSize(wd); got != 60 {
		t.Errorf("递归字节数 = %d, 期望 60", got)
	}
}

func TestCleanupWorkTempDir_LoggingBehaviour(t *testing.T) {
	oldPath := eventsPath
	defer func() { eventsPath = oldPath }()

	wd, dir := mkTempWorkDir(t)
	initEventLog(wd)

	cleanupWorkTempDir(wd) // 空目录: 不该留痕(避免噪音)
	logFile := filepath.Join(wd, ".forge", "events.jsonl")
	if b, err := os.ReadFile(logFile); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		t.Errorf("空目录不应写事件, 实得: %s", string(b))
	}

	if err := os.WriteFile(filepath.Join(dir, "x.tmp"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	cleanupWorkTempDir(wd)
	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("清理后应写出事件: %v", err)
	}
	if !strings.Contains(string(b), "hygiene") || !strings.Contains(string(b), ".forge-temp") {
		t.Errorf("事件内容不符: %s", string(b))
	}
}

// 接线哨兵: 扫全包(不写死文件名)确认调用点存在, 并钉死「不得放进 NewForge」的实测约束。
func TestCleanupWorkTempDir_WiredInStartup(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	callers := []string{}
	for _, fn := range files {
		if strings.HasSuffix(fn, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(fn)
		if rerr != nil {
			continue
		}
		if strings.Contains(string(b), "cleanupWorkTempDir(cfg.WorkDir)") {
			callers = append(callers, fn)
		}
	}
	if len(callers) == 0 {
		t.Fatal("接线丢失: 全包找不到 cleanupWorkTempDir(cfg.WorkDir) 调用点")
	}

	b, err := os.ReadFile("forge.go")
	if err != nil {
		t.Skipf("读 forge.go 失败: %v", err)
	}
	body := string(b)
	idx := strings.Index(body, "func NewForge(")
	if idx < 0 {
		t.Fatal("找不到 NewForge 定义")
	}
	tail := body[idx:]
	if end := strings.Index(tail, "\n}\n"); end > 0 {
		tail = tail[:end]
	}
	if strings.Contains(tail, "cleanupWorkTempDir") {
		t.Error("NewForge 内出现 cleanupWorkTempDir —— 17 个测试调用点会互踩生产临时目录")
	}
}
