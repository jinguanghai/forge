// ctrlclose_test.go: 控制台关闭事件处理的接线契约。
//
// 不真注册处理器: SetConsoleCtrlHandler 一旦注册, 测试进程收到关闭事件会走
// os.Exit(0) 路径, 改变测试行为。改为源码级结构断言。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wd, name))
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	return string(b)
}

func TestCtrlClose_BothPlatformsDefineHandler(t *testing.T) {
	for _, name := range []string{"ctrlclose_windows.go", "ctrlclose_other.go"} {
		s := readRepoFile(t, name)
		if !strings.Contains(s, "func installCtrlCloseHandler(") {
			t.Errorf("%s 缺 installCtrlCloseHandler —— 跨平台缺口", name)
		}
	}
}

func TestCtrlClose_WindowsSavesCacheBeforeExit(t *testing.T) {
	s := readRepoFile(t, "ctrlclose_windows.go")
	for _, want := range []string{
		"syscall.NewCallback", // 回调必须真注册
		"var ctrlCloseCallback uintptr",
		"ctrlCloseEvent", "ctrlLogoffEvent", "ctrlShutdownEvent",
		"agent.Shutdown()", // 退出前存缓存, 否则下次冷启动全量 miss
		"os.Exit(0)",       // 必须在回调内同步退出(主 goroutine 可能阻塞在 stdin 读取)
	} {
		if !strings.Contains(s, want) {
			t.Errorf("ctrlclose_windows.go 缺 %q", want)
		}
	}
	// 包级持有引用: 回调若被 GC 回收, 窗口关闭时清理失效
	if !strings.Contains(s, "ctrlCloseCallback = syscall.NewCallback(") {
		t.Error("回调必须赋给包级变量, 否则可能被 GC 回收")
	}
}

func TestCtrlClose_NotCalledFromTests(t *testing.T) {
	// 自检: 测试代码不得调用 installCtrlCloseHandler(会真注册处理器, 改变测试进程行为)。
	// 用拼接构造搜索串, 避免本文件自身被命中。
	needle := "installCtrlCloseHandler" + "("
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") || name == "ctrlclose_test.go" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(wd, name))
		if err != nil {
			continue
		}
		if strings.Contains(string(b), needle) {
			t.Errorf("%s 调用了 installCtrlCloseHandler —— 会真注册信号处理器", name)
		}
	}
}

func TestCtrlClose_OtherPlatformNoOp(t *testing.T) {
	s := readRepoFile(t, "ctrlclose_other.go")
	if !strings.Contains(s, "//go:build !windows") {
		t.Error("ctrlclose_other.go 缺 build 约束")
	}
	if !strings.Contains(s, "func installCtrlCloseHandler(agent *AgentRunner) {}") {
		t.Error("非 Windows 实现应为空函数体")
	}
}
