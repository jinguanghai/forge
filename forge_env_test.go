package main

// forge_env_test.go — 输出截断 / 摘要 / 子进程构造的哨兵。

import (
	"context"
	"strings"
	"testing"
)

func TestForgeEnv_TruncateMsg(t *testing.T) {
	if got := truncateMsg("abc"); got != "abc" {
		t.Errorf("短消息不应被截断, 得到 %q", got)
	}
	// 超长消息必须截断并加省略标记(长度 300 + "...")
	got := truncateMsg(strings.Repeat("x", 400))
	if len(got) != 303 {
		t.Errorf("截断后长度 = %d, 期望 303", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("截断后应带省略号, 得到 %q", got[len(got)-5:])
	}
}

func TestForgeEnv_SummarizeOutput(t *testing.T) {
	got := summarizeOutput("out", "err")
	if !strings.Contains(got, "out") {
		t.Errorf("摘要应含 stdout, 得到 %q", got)
	}
	if !strings.Contains(got, "err") {
		t.Errorf("摘要应含 stderr, 得到 %q", got)
	}
	// 退化用例: stderr 空时不应输出 [stderr] 标签 (P1-2)
	if only := summarizeOutput("just out", ""); strings.Contains(only, "[stderr]") {
		t.Errorf("stderr 为空时不应含 [stderr] 标签, 得到 %q", only)
	}
}

func TestForgeEnv_NewCmdBindsWorkDir(t *testing.T) {
	// 进程 cwd 必须显式绑定(见 lessons: 依赖 getcwd 的组件禁静默兜底)
	f := &Forge{workDir: t.TempDir()}
	cmd := f.newCmd(context.Background(), "go", "version")
	if cmd.Dir != f.workDir {
		t.Errorf("cmd.Dir = %q, 期望 %q", cmd.Dir, f.workDir)
	}
}

func TestForgeEnv_FindGoCommand(t *testing.T) {
	f := &Forge{workDir: t.TempDir()}
	got, err := f.findGoCommand()
	if err != nil {
		t.Fatalf("findGoCommand 失败: %v", err)
	}
	if !strings.Contains(strings.ToLower(got), "go") {
		t.Errorf("findGoCommand = %q, 期望含 \"go\"", got)
	}
}

func TestForgeEnv_CleanupStaleTempDirs(t *testing.T) {
	// 清理函数在无残留时也必须安全返回(启动路径上调用, panic 会阻止铸剑炉启动)
	forgeCleanupStaleTempDirs()
	forgeCleanupStaleTempDirs()
}
