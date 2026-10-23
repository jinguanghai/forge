package main

// git_autosave_test.go — 自动存档哨兵 (fractal F4 镜像)
//
// 钉住四件事 (双轨制的全部契约):
//   ① 存档【不移动主线】—— HEAD / refs/heads/main 一字不动;
//   ② 存档【不碰真实 index】—— 工作区里已 staged 的内容不得被搅乱;
//   ③ 清理【只删 autosave 命名空间】—— 超期删、新鲜留、主线分支永不入选;
//   ④ scope 与 self/upgrade 快照一致 —— 诱饵文件(未 ignore 的 txt / 子目录新增)不入档。
//
// 另有一个隔离契约: 测试期 autosaveOnTurn 必须被抑制 (init 置位), 否则任何以
// 生产根为 WorkDir 的用例都会往真实仓库写 ref —— 教训「测试隔离必须覆盖生产根」。
// 抑制只拦入口, 不拦 gitAutosave (用例需要能直接打核心函数)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() { autosaveSuppressed = true }

func autosaveInitRepo(t *testing.T, wd string) {
	t.Helper()
	gitCmd(t, wd, "init", "-b", "main")
	gitCmd(t, wd, "config", "core.autocrlf", "false")
	gitCmd(t, wd, "config", "user.name", "t")
	gitCmd(t, wd, "config", "user.email", "t@t")
}

func autosaveWrite(t *testing.T, wd, rel, body string) {
	t.Helper()
	p := filepath.Join(wd, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func autosaveRefs(t *testing.T, wd string) string {
	t.Helper()
	return strings.TrimSpace(gitCmd(t, wd, "for-each-ref", "--format=%(refname)", "refs/autosave/"))
}

// TestAutosaveRefTime 时间戳解析: 只认 autosave 命名空间, 容忍 -N 冲突后缀。
func TestAutosaveRefTime(t *testing.T) {
	ok := []string{"refs/autosave/20261009_143000", "refs/autosave/20261009_143000-2"}
	for _, r := range ok {
		if _, valid := autosaveRefTime(r); !valid {
			t.Errorf("应可解析: %s", r)
		}
	}
	bad := []string{"refs/heads/main", "refs/autosave/nonsense", "refs/autosave/", "20261009_143000"}
	for _, r := range bad {
		if _, valid := autosaveRefTime(r); valid {
			t.Errorf("不应解析: %s", r)
		}
	}
	if tm, valid := autosaveRefTime("refs/autosave/20261009_143000"); valid {
		if tm.Year() != 2026 || int(tm.Month()) != 10 || tm.Day() != 9 {
			t.Errorf("解析结果错误: %v", tm)
		}
	}
}

// TestAutosaveOnTurnSuppressedInTest 隔离契约: 测试期入口必须被抑制。
func TestAutosaveOnTurnSuppressedInTest(t *testing.T) {
	if !autosaveSuppressed {
		t.Fatal("测试期应处于抑制态 (init 未生效)")
	}
	wd := t.TempDir()
	autosaveInitRepo(t, wd)
	autosaveWrite(t, wd, "a.go", "package main\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")
	autosaveWrite(t, wd, "a.go", "package main\n// changed\n")
	if h := autosaveOnTurn(wd); h != "" {
		t.Errorf("抑制态不应存档, got %q", h)
	}
	if refs := autosaveRefs(t, wd); refs != "" {
		t.Errorf("抑制态不应产生 ref: %q", refs)
	}
}

// TestGitAutosaveDoesNotMoveHead 双轨制核心: 存档只加独立 ref, 主线与真实 index 不动。
func TestGitAutosaveDoesNotMoveHead(t *testing.T) {
	wd := t.TempDir()
	autosaveInitRepo(t, wd)
	autosaveWrite(t, wd, "a.go", "package main\n// v1\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")

	// 真实 index 里先放一份 staged 内容 —— 存档绝不能搅乱它
	autosaveWrite(t, wd, "staged.go", "package main\n// staged marker\n")
	gitCmd(t, wd, "add", "staged.go")
	stagedBefore := gitCmd(t, wd, "diff", "--cached", "--name-only")
	if stagedBefore == "" {
		t.Fatal("前置条件失败: 应存在 staged 内容")
	}

	headBefore := gitCmd(t, wd, "rev-parse", "HEAD")
	mainBefore := gitCmd(t, wd, "rev-parse", "refs/heads/main")

	autosaveWrite(t, wd, "a.go", "package main\n// v2 changed\n")
	if h := gitAutosave(wd, "test"); h == "" {
		t.Fatal("有改动时应建存档")
	}

	if got := gitCmd(t, wd, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD 被移动: %s -> %s", headBefore, got)
	}
	if got := gitCmd(t, wd, "rev-parse", "refs/heads/main"); got != mainBefore {
		t.Errorf("主线分支被移动: %s -> %s", mainBefore, got)
	}
	if got := gitCmd(t, wd, "diff", "--cached", "--name-only"); got != stagedBefore {
		t.Errorf("真实 index 被搅乱: %q -> %q", stagedBefore, got)
	}
	refs := autosaveRefs(t, wd)
	if refs == "" {
		t.Fatal("应产生自动存档 ref")
	}
	ref := strings.Split(refs, "\n")[0]
	if show := gitCmd(t, wd, "show", "--name-status", "--format=", ref); !strings.Contains(show, "a.go") {
		t.Errorf("存档未含本次改动:\n%s", show)
	}
	// 工作区文件仍在 (存档不删/不改用户文件)
	b, err := os.ReadFile(filepath.Join(wd, "a.go"))
	if err != nil || !strings.Contains(string(b), "v2 changed") {
		t.Errorf("工作区文件被改动: %v %q", err, string(b))
	}
}

// TestGitAutosaveNoChangeNoRef 无改动不产生噪音存档。
func TestGitAutosaveNoChangeNoRef(t *testing.T) {
	wd := t.TempDir()
	autosaveInitRepo(t, wd)
	autosaveWrite(t, wd, "a.go", "package main\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")
	if h := gitAutosave(wd, "test"); h != "" {
		t.Errorf("无改动不应建存档, got %q", h)
	}
	if refs := autosaveRefs(t, wd); refs != "" {
		t.Errorf("无改动却产生 ref: %q", refs)
	}
}

// TestGitAutosavePrune 清理只删超期 autosave, 新鲜与主线永不入选。
func TestGitAutosavePrune(t *testing.T) {
	wd := t.TempDir()
	autosaveInitRepo(t, wd)
	autosaveWrite(t, wd, "a.go", "package main\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")
	head := gitCmd(t, wd, "rev-parse", "HEAD")

	oldRef := "refs/autosave/" + time.Now().AddDate(0, 0, -40).Format("20060102_150405")
	freshRef := "refs/autosave/" + time.Now().Format("20060102_150405")
	gitCmd(t, wd, "update-ref", oldRef, head)
	gitCmd(t, wd, "update-ref", freshRef, head)

	if n := gitAutosavePrune(wd, 30); n != 1 {
		t.Errorf("应删 1 条超期存档, got %d", n)
	}
	refs := autosaveRefs(t, wd)
	if strings.Contains(refs, oldRef) {
		t.Errorf("超期存档未被删除: %s", refs)
	}
	if !strings.Contains(refs, freshRef) {
		t.Errorf("新鲜存档被误删: %s", refs)
	}
	if got := gitCmd(t, wd, "rev-parse", "refs/heads/main"); got != head {
		t.Errorf("主线分支被动: %s -> %s", head, got)
	}
}

// TestGitAutosaveScopeBait scope 与 self/upgrade 快照一致: 诱饵不入档。
func TestGitAutosaveScopeBait(t *testing.T) {
	wd := t.TempDir()
	autosaveInitRepo(t, wd)
	autosaveWrite(t, wd, ".gitignore", "*.log\n")
	autosaveWrite(t, wd, "a.go", "package main\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")

	autosaveWrite(t, wd, "bait.txt", "BAIT-TXT\n")
	autosaveWrite(t, wd, "secrets.env", "SECRET=1\n")
	autosaveWrite(t, wd, "sub/x.go", "package sub\n// sub bait\n")
	autosaveWrite(t, wd, "a.go", "package main\n// changed\n")

	if h := gitAutosave(wd, "bait"); h == "" {
		t.Fatal("有改动时应建存档")
	}
	refs := autosaveRefs(t, wd)
	if refs == "" {
		t.Fatal("应产生自动存档 ref")
	}
	show := gitCmd(t, wd, "show", "--name-status", "--format=", strings.Split(refs, "\n")[0])
	for _, bait := range []string{"bait.txt", "secrets.env", "sub/x.go"} {
		if strings.Contains(show, bait) {
			t.Errorf("诱饵被误入档: %s\n%s", bait, show)
		}
	}
	if !strings.Contains(show, "a.go") {
		t.Errorf("已跟踪改动应入档:\n%s", show)
	}
}

// TestGitAutosaveDisabledByEnv 环境开关: FORGE_AUTOSAVE=0 时存档与清理都停。
func TestGitAutosaveDisabledByEnv(t *testing.T) {
	t.Setenv("FORGE_AUTOSAVE", "0")
	wd := t.TempDir()
	autosaveInitRepo(t, wd)
	autosaveWrite(t, wd, "a.go", "package main\n")
	gitCmd(t, wd, "add", "-A")
	gitCmd(t, wd, "commit", "-m", "init")
	autosaveWrite(t, wd, "a.go", "package main\n// changed\n")
	if h := gitAutosave(wd, "x"); h != "" {
		t.Errorf("开关关闭时不应存档, got %q", h)
	}
	if n := gitAutosavePrune(wd, 30); n != 0 {
		t.Errorf("开关关闭时不应清理, got %d", n)
	}
}

// TestAutosaveWiredInFinalize 接线哨兵: finalize 的正常收尾分支必须调用 autosaveOnTurn。
//
// 为什么需要 (接线断裂 = 静默失效): finalize 的既有用例都以 t.TempDir() 为 WorkDir,
// 且测试期 autosaveOnTurn 被抑制 —— 把那一行删掉, 所有用例照样全绿, 而自动存档
// 从此再不触发。判据只能读源码文本; 且必须剥注释行 (教训: 注释里提及不算接线)。
func TestAutosaveWiredInFinalize(t *testing.T) {
	src, err := os.ReadFile("agent_stream_final.go")
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		code = append(code, l)
	}
	if !strings.Contains(strings.Join(code, "\n"), "autosaveOnTurn(f.agent.cfg.WorkDir)") {
		t.Error("finalize 未接线 autosaveOnTurn —— 会话收尾自动存档静默失效")
	}
}
