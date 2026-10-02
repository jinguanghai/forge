package main

// git_snapshot_test.go — git 快照 scope 边界哨兵 (20260923)
//
// 病根: gitSnapshot 原用 `git add -A` 全量暂存 —— 无 scope 边界, 未被 .gitignore 覆盖的
// 临时日志/探针产物/敏感文件会被 auto 快照固化进 git 历史 (实测 .forge/_audit_*.log 入库,
// 而 .gitignore 里 20260914 已为同类问题打过补丁 —— 打补丁治标, scope 收窄治本)。
// 本哨兵把「快照只收已跟踪改动 + 根目录白名单新增」固化为死程序判定。
//
// 注意: 测试内各文件内容必须互不相同 —— 内容雷同会被 git 识别为 rename(R100),
// --name-only 只显示重命名目标, 导致删除断言假失败 (20260923 实测踩坑)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotAllowedWhitelist 白名单单元判定: 只放行根目录白名单文件
func TestSnapshotAllowedWhitelist(t *testing.T) {
	allow := []string{"forge.go", "git_snapshot_test.go", "go.mod", "go.sum",
		".gitignore", ".env.example", "LICENSE", "README.md"}
	for _, f := range allow {
		if !snapshotAllowed(f) {
			t.Errorf("应放行: %s", f)
		}
	}
	deny := []string{
		"_audit_bait.log", "bait.txt", "memory.json", "events.jsonl",
		".forge/alerts.jsonl", "sub/x.go", "knowledge/x.go", "defense_system/x.py",
		"forge.go.bak", "x.go.tmp", ".env", "secrets.env",
	}
	for _, f := range deny {
		if snapshotAllowed(f) {
			t.Errorf("应拒绝: %s", f)
		}
	}
}

// TestGitSnapshotScopeBait 端到端诱饵测试: 未 ignore 的临时/敏感文件绝不能被快照带入
func TestGitSnapshotScopeBait(t *testing.T) {
	wd := t.TempDir()
	gitCmd(t, wd, "init", "-b", "main")
	gitCmd(t, wd, "config", "core.autocrlf", "false")
	gitCmd(t, wd, "config", "user.name", "t")
	gitCmd(t, wd, "config", "user.email", "t@t")

	// 初始提交: 已跟踪文件 + 真实 .gitignore
	if data, err := os.ReadFile(".gitignore"); err == nil {
		os.WriteFile(filepath.Join(wd, ".gitignore"), data, 0644)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(wd, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# readme v1\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")

	// 诱饵: 未被 .gitignore 覆盖的临时日志/产物/敏感文件 (旧 add -A 会把它们全带入)
	bait := []string{"_audit_bait.log", "bait.txt", "secrets.env", "sub/x.go", "knowledge/x.go"}
	for i, b := range bait {
		write(b, "BAIT-"+string(rune('A'+i))+"\n")
	}
	// 合法变更: 根目录新增源码 + 已跟踪文件改动 + 已跟踪文件删除
	write("newgate.go", "package main\n\n// newgate unique marker\n")
	write("README.md", "# readme v2 changed\n")
	write("doomed.go", "package main\n\n// doomed unique marker\n")
	gitCmd(t, wd, "add", "doomed.go")
	gitCmd(t, wd, "commit", "-m", "add doomed")
	if err := os.Remove(filepath.Join(wd, "doomed.go")); err != nil {
		t.Fatal(err)
	}

	h := gitSnapshot(wd, "bait-test")
	if h == "" {
		t.Fatal("快照应成功")
	}
	// --name-status 能区分 M/A/D, 不受 rename 检测影响
	files := gitCmd(t, wd, "show", "--name-status", "--format=", "HEAD")
	for _, b := range bait {
		if strings.Contains(files, b) {
			t.Errorf("诱饵文件被误入库: %s\nHEAD 变更:\n%s", b, files)
		}
	}
	if !strings.Contains(files, "newgate.go") {
		t.Errorf("根目录新增源码应入库:\n%s", files)
	}
	if !strings.Contains(files, "README.md") {
		t.Errorf("已跟踪文件改动应入库:\n%s", files)
	}
	if !strings.Contains(files, "D\tdoomed.go") {
		t.Errorf("已跟踪文件删除应入库(D 状态):\n%s", files)
	}
	// 诱饵仍存在于工作区且仍未被跟踪 (快照只影响 index/历史, 不删用户文件)
	if _, err := os.Stat(filepath.Join(wd, "_audit_bait.log")); err != nil {
		t.Errorf("诱饵文件不应被删除: %v", err)
	}
	others := gitCmd(t, wd, "ls-files", "--others", "--exclude-standard")
	if !strings.Contains(others, "bait.txt") {
		t.Errorf("诱饵应仍为未跟踪状态:\n%s", others)
	}
}
