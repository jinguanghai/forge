package main

// selfreplace_isolated_test.go — 自替换 (runSelfReplace) 的隔离测试 (20261001)
//
// 旧用例把"测试二进制所在目录"直接当快照基线, 而该目录由外部决定(例如 %TEMP%)。
// 一旦有别的进程在其中增删文件, 比对就假失败 —— 判据被环境噪声污染。
// 现在由测试自建隔离目录, 只放自己布景的文件, 基线完全可控。
//
// runSelfReplace 的行为由 os.Executable() 的文件名决定(恰为 forge_new 才就位),
// 而当前进程改不了自己的名字, 所以走子进程: 把测试二进制复制成目标文件名再运行。
// 守卫路径与就位路径因此都能在隔离目录里真实走一遍, 绝不碰生产 forge.exe。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfReplaceChildEnv 子进程标记: 置 1 时子进程只做一件事 —— 调 runSelfReplace。
const selfReplaceChildEnv = "FORGE_SELFREPLACE_CHILD"

// childHookName 子进程入口的测试名 (父测试用 -test.run 精确指定)。
const childHookName = "TestSelfReplace_ChildHook"

// TestSelfReplace_ChildHook 子进程入口: 在隔离目录内调用 runSelfReplace。
// 全量跑时环境变量未设 → Skip (它只作为隔离子进程的落点存在)。
func TestSelfReplace_ChildHook(t *testing.T) {
	if os.Getenv(selfReplaceChildEnv) != "1" {
		t.Skip("仅作为隔离测试的子进程入口")
	}
	runSelfReplace()
}

// copyFileForTest 逐字节复制 (不保留属性, 只求内容一致)。
func copyFileForTest(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读源文件失败 %s: %v", src, err)
	}
	if err := os.WriteFile(dst, b, 0o755); err != nil {
		t.Fatalf("写目标文件失败 %s: %v", dst, err)
	}
}

// stageSelfReplaceProbe 建隔离目录并布景, 再把测试二进制复制成 asName 放进去。
func stageSelfReplaceProbe(t *testing.T, asName string, setup func(dir string)) (dir, probe string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Skipf("拿不到测试二进制路径: %v", err)
	}
	dir = t.TempDir()
	if setup != nil {
		setup(dir)
	}
	probe = filepath.Join(dir, asName)
	copyFileForTest(t, self, probe)
	return dir, probe
}

// runSelfReplaceChild 以子进程运行 probe (测试二进制的副本), 只跑子进程入口。
func runSelfReplaceChild(t *testing.T, probe, dir string) string {
	t.Helper()
	cmd := exec.Command(probe, "-test.run", "^"+childHookName+"$", "-test.v")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), selfReplaceChildEnv+"=1")
	b, err := cmd.CombinedOutput()
	out := string(b)
	if err != nil {
		t.Fatalf("隔离子进程失败: %v\n%s", err, out)
	}
	return out
}

// listNames 目录内文件名, 用于"一个文件都没动"这类断言。
func listNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("列目录失败 %s: %v", dir, err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// 守卫路径: 二进制名不是 forge_new → 立即返回, 不得增删任何文件。
// 名字刻意避开 forge_new, 且隔离目录里只有这一个文件, 故判据是"目录内容逐字不变"。
func TestRunSelfReplace_GuardOnExecutableName(t *testing.T) {
	const probeName = "forge_guard_probe.test.exe"
	dir, probe := stageSelfReplaceProbe(t, probeName, nil)
	before := listNames(t, dir)
	out := runSelfReplaceChild(t, probe, dir)
	after := listNames(t, dir)
	if strings.Join(before, "|") != strings.Join(after, "|") {
		t.Fatalf("守卫失效: 非 forge_new 场景不得增删文件\n前: %v\n后: %v\n子进程输出:\n%s", before, after, out)
	}
}

// 就位路径: 二进制名恰为 forge_new → 备份旧 forge.exe, 再把自己 rename 就位, 并留痕。
func TestRunSelfReplace_RealTriggerIsolated(t *testing.T) {
	oldContent := []byte("OLD-FORGE-EXE-PLACEHOLDER")
	dir, probe := stageSelfReplaceProbe(t, "forge_new.exe", func(d string) {
		if err := os.WriteFile(filepath.Join(d, "forge.exe"), oldContent, 0o755); err != nil {
			t.Fatalf("布景失败: %v", err)
		}
	})
	out := runSelfReplaceChild(t, probe, dir)

	// 1) 原名消失 (已就位)
	if _, err := os.Stat(probe); err == nil {
		t.Fatalf("forge_new.exe 应已改名就位, 实际仍存在\n%s", out)
	}
	// 2) forge.exe 已被新内容覆盖 (大小等于测试二进制)
	self, err := os.Executable()
	if err != nil {
		t.Skipf("拿不到测试二进制路径: %v", err)
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		t.Fatalf("stat 测试二进制失败: %v", err)
	}
	newInfo, err := os.Stat(filepath.Join(dir, "forge.exe"))
	if err != nil {
		t.Fatalf("forge.exe 不存在 —— 未就位\n%s", out)
	}
	if newInfo.Size() != selfInfo.Size() {
		t.Fatalf("forge.exe 大小 %d != 测试二进制 %d —— 内容未真正替换\n%s",
			newInfo.Size(), selfInfo.Size(), out)
	}
	// 3) 旧 exe 已备份且内容原样
	baks, _ := filepath.Glob(filepath.Join(dir, "forge.exe.bak_*"))
	if len(baks) != 1 {
		t.Fatalf("应恰好产生 1 个备份, 实际 %v\n%s", baks, out)
	}
	got, err := os.ReadFile(baks[0])
	if err != nil {
		t.Fatalf("读备份失败: %v", err)
	}
	if string(got) != string(oldContent) {
		t.Fatalf("备份内容不符: %q", got)
	}
	// 4) 留痕: 隔离目录内 .forge/events.jsonl 记 result=ok
	ev, err := os.ReadFile(filepath.Join(dir, ".forge", "events.jsonl"))
	if err != nil {
		t.Fatalf("未留痕: %v\n%s", err, out)
	}
	if !strings.Contains(string(ev), `"result":"ok"`) {
		t.Fatalf("留痕缺 result=ok: %s", ev)
	}
}

// 备份失败路径: 旧 forge.exe 被别的句柄独占(无 FILE_SHARE_DELETE) → 必须中止替换,
// 旧 exe 原样保留、不产生备份、不留半成品 (旧 exe 必须可回滚, 不能裸奔)。
func TestRunSelfReplace_BackupAbortIsolated(t *testing.T) {
	oldContent := []byte("OLD-FORGE-EXE-PLACEHOLDER")
	dir, probe := stageSelfReplaceProbe(t, "forge_new.exe", func(d string) {
		if err := os.WriteFile(filepath.Join(d, "forge.exe"), oldContent, 0o755); err != nil {
			t.Fatalf("布景失败: %v", err)
		}
	})
	// Go 的 os.OpenFile 默认共享读+写但不共享删除 → 子进程的 rename 必失败。
	hold, err := os.OpenFile(filepath.Join(dir, "forge.exe"), os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("持有句柄失败: %v", err)
	}
	defer hold.Close()

	out := runSelfReplaceChild(t, probe, dir)
	if !strings.Contains(out, "自替换中止") {
		t.Fatalf("独占场景应报中止提示, 实际输出:\n%s", out)
	}
	if _, err := os.Stat(probe); err != nil {
		t.Errorf("中止后 forge_new.exe 必须原样保留: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "forge.exe"))
	if err != nil || string(got) != string(oldContent) {
		t.Errorf("中止后旧 forge.exe 必须原样: err=%v content=%q", err, got)
	}
	baks, _ := filepath.Glob(filepath.Join(dir, "forge.exe.bak_*"))
	if len(baks) != 0 {
		t.Errorf("中止不得留下备份: %v", baks)
	}
}
