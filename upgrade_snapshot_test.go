package main

// upgrade_snapshot_test.go — 三期 DMAIC I4 git 快照测试

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitCmd 在指定目录执行 git 命令 (测试用, 带固定身份)
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=forge-test", "GIT_AUTHOR_EMAIL=test@forge.local",
		"GIT_COMMITTER_NAME=forge-test", "GIT_COMMITTER_EMAIL=test@forge.local",
		"GIT_TERMINAL_PROMPT=0")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestGitSnapshotNonGitDir(t *testing.T) {
	wd := t.TempDir()
	if h := gitSnapshot(wd, "test"); h != "" {
		t.Fatalf("非 git 目录应返回空串, got %q", h)
	}
}

func TestGitSnapshotCommits(t *testing.T) {
	wd := t.TempDir()
	gitCmd(t, wd, "init", "-b", "main")

	// 写一个源码文件 (模拟自改前状态)
	if err := os.WriteFile(filepath.Join(wd, "forge.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	h := gitSnapshot(wd, "pre-test")
	if h == "" {
		t.Fatal("git 快照应返回 commit hash")
	}

	// 验证 commit 存在且是 auto 消息
	log := gitCmd(t, wd, "log", "--oneline", "-3")
	if !strings.Contains(log, "auto: pre-selfmod") {
		t.Fatalf("commit 消息不匹配: %s", log)
	}
	if !strings.Contains(log, "pre-test") {
		t.Fatalf("commit 消息应含 reason: %s", log)
	}

	// 再次快照: 应生成新 commit
	h2 := gitSnapshot(wd, "pre-test-2")
	if h2 == "" || h2 == h {
		t.Fatalf("第二次快照应生成新 hash: h=%s h2=%s", h, h2)
	}
}

func TestGitSnapshotIgnoresRuntime(t *testing.T) {
	// .gitignore 应排除 memory.json / events.jsonl —— git add -A 后 commit 不包含它们
	wd := t.TempDir()
	gitCmd(t, wd, "init", "-b", "main")
	gitCmd(t, wd, "config", "core.autocrlf", "false")
	// 复制仓库 .gitignore
	if data, err := os.ReadFile(".gitignore"); err == nil {
		os.WriteFile(filepath.Join(wd, ".gitignore"), data, 0644)
	}
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")

	// 写运行时文件 (应被 ignore) 和源码 (应入库)
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(`{"runtime":true}`), 0644)
	os.MkdirAll(filepath.Join(wd, ".forge"), 0755)
	os.WriteFile(filepath.Join(wd, ".forge", "events.jsonl"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(wd, "agent.go"), []byte("package main\n"), 0644)

	h := gitSnapshot(wd, "pre-test")
	if h == "" {
		t.Fatal("快照失败")
	}

	// 检查最新 commit 里没有 memory.json / events.jsonl
	files := gitCmd(t, wd, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "memory.json") || strings.Contains(files, "events.jsonl") {
		t.Fatalf("运行时文件被误入库:\n%s", files)
	}
	if !strings.Contains(files, "agent.go") {
		t.Fatalf("源码应入库:\n%s", files)
	}
}
