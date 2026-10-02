package main

// forge_exec_util_test.go — 通用进程/归档工具的哨兵。
// (原与 forge_shell.go 同名, sh gate 退役后随实现文件一同改名)
//
// sh gate 已于 20261001 退役, 其判定函数 (shPrefersBash / splitShellSegments /
// shEchoStaticPattern / forgeSplitCommand / forgeFindBash) 与对应测试一并删除;
// 退役判据见 sh_retired_test.go。本文件只保留与 sh 无关的通用工具测试。

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestForgeShell_SafeExitCode(t *testing.T) {
	// ProcessState 为 nil(命令未运行)必须返回 -1 而非 panic
	if got := safeExitCode(&exec.Cmd{}); got != -1 {
		t.Errorf("safeExitCode(未运行的 cmd) = %d, 期望 -1", got)
	}
}

func TestForgeShell_ExpandArgsPassthrough(t *testing.T) {
	// 不含占位符的参数必须原样返回(多一个少一个都会改变编译命令语义)
	args := []string{"-c", "echo hi"}
	got, _ := expandArgs(args, "/src", "/wd", "/tmp")
	if len(got) != len(args) {
		t.Fatalf("参数个数 = %d, 期望 %d", len(got), len(args))
	}
	if got[0] != "-c" {
		t.Errorf("无占位符参数被改写: %q", got[0])
	}
}

func TestForgeShell_ForgeUnzip(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	// 不存在的归档必须返回错误而非 panic
	if err := forgeUnzip(filepath.Join(dir, "nope.zip"), dest); err == nil {
		t.Error("不存在的 zip 应返回错误")
	}
}

func TestForgeShell_ForgeUntarGz(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	if err := forgeUntarGz(filepath.Join(dir, "nope.tar.gz"), dest); err == nil {
		t.Error("不存在的 tar.gz 应返回错误")
	}
}
