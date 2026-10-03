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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// selfReplaceChildEnv 子进程标记: 置 1 时子进程只做一件事 —— 调 runSelfReplace。

// selfReplaceSpawnRecordEnv 子进程内接管 spawn 的方式: 值为 selfReplaceSpawnFail
// 表示模拟启动失败; 其它非空值视为「把 spawn 参数写进这个文件」。
const selfReplaceSpawnRecordEnv = "FORGE_SELFREPLACE_SPAWN"

// selfReplaceSpawnFail 见 selfReplaceSpawnRecordEnv。
const selfReplaceSpawnFail = "FAIL"
const selfReplaceChildEnv = "FORGE_SELFREPLACE_CHILD"

// childHookName 子进程入口的测试名 (父测试用 -test.run 精确指定)。
const childHookName = "TestSelfReplace_ChildHook"

// TestSelfReplace_ChildHook 子进程入口: 在隔离目录内调用 runSelfReplace。
// 全量跑时环境变量未设 → Skip (它只作为隔离子进程的落点存在)。
// TestSelfReplace_ChildHook 子进程入口: 在隔离目录内调用 runSelfReplace。
// 全量跑时环境变量未设 → Skip (它只作为隔离子进程的落点存在)。
func TestSelfReplace_ChildHook(t *testing.T) {
	if os.Getenv(selfReplaceChildEnv) != "1" {
		t.Skip("仅作为隔离测试的子进程入口")
	}
	// 子进程内必须接管两个副作用, 否则会出真事故:
	//   ① 真 spawn 会把测试二进制的副本当铸剑炉拉起来 —— 它不带 -test.run,
	//      会跑全套测试, 既递归又污染;
	//   ② os.Exit 会让 testing 框架来不及输出结果, 父测试只看到空输出。
	// 产品路径(真 forge.exe)不受影响 —— 那里走默认实现。
	switch spec := os.Getenv(selfReplaceSpawnRecordEnv); spec {
	case "":
		spawnSelfProcess = func(string, string) error { return nil }
	case selfReplaceSpawnFail:
		spawnSelfProcess = func(string, string) error { return errors.New("probe: spawn refused") }
	default:
		spawnSelfProcess = func(exePath, workDir string) error {
			return os.WriteFile(spec, []byte(exePath+"\n"+workDir), 0o644)
		}
	}
	selfReplaceExit = func(int) {}
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

// runSelfReplaceChildRecording 同 runSelfReplaceChild, 但让子进程把 spawn 的参数
// 写进文件, 供父测试断言「重启了谁」。spawn 是收不回的副作用, 只能这样观察。
// mode 为 selfReplaceSpawnFail 时模拟启动失败; 其它值视为记录文件路径。
func runSelfReplaceChildRecording(t *testing.T, probe, dir, mode string) (string, []string) {
	t.Helper()
	cmd := exec.Command(probe, "-test.run", "^"+childHookName+"$", "-test.v")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), selfReplaceChildEnv+"=1", selfReplaceSpawnRecordEnv+"="+mode)
	b, err := cmd.CombinedOutput()
	out := string(b)
	if err != nil {
		t.Fatalf("隔离子进程失败: %v\n%s", err, out)
	}
	if mode == selfReplaceSpawnFail {
		return out, nil
	}
	raw, rerr := os.ReadFile(mode)
	if rerr != nil {
		return out, nil // 没记录 = 没 spawn
	}
	return out, strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// TestRunSelfReplace_SpawnsReplacement 就位后必须重启, 且目标恰是就位后的 forge.exe。
// 钉住的根因(20261003): 不重启则进程名与文件名不一致, 外部「按名字关进程」的脚本
// 全部失明 —— 升级脚本关不掉旧进程, 同时起两个实例, 还报「升级成功」。
func TestRunSelfReplace_SpawnsReplacement(t *testing.T) {
	dir, probe := stageSelfReplaceProbe(t, "forge_new.exe", func(d string) {
		if err := os.WriteFile(filepath.Join(d, "forge.exe"), []byte("OLD-FORGE-EXE"), 0o755); err != nil {
			t.Fatalf("布景失败: %v", err)
		}
	})
	rec := filepath.Join(dir, "spawn_record.txt")
	out, args := runSelfReplaceChildRecording(t, probe, dir, rec)
	if len(args) < 2 {
		t.Fatalf("就位后未 spawn 替代进程 —— 进程名将与文件名不一致, 外部脚本会失明\n%s", out)
	}
	wantExe := filepath.Join(dir, "forge.exe")
	if !strings.EqualFold(args[0], wantExe) {
		t.Errorf("spawn 目标 = %q, 期望 %q", args[0], wantExe)
	}
	if !strings.EqualFold(args[1], dir) {
		t.Errorf("spawn 工作目录 = %q, 期望 %q", args[1], dir)
	}
	ev, err := os.ReadFile(filepath.Join(dir, ".forge", "events.jsonl"))
	if err != nil {
		t.Fatalf("未留痕: %v", err)
	}
	if !strings.Contains(string(ev), `"restart":"spawned"`) {
		t.Errorf("留痕缺 restart=spawned: %s", ev)
	}
}

// TestRunSelfReplace_SpawnFailureKeepsDeploy spawn 失败时: 就位成果必须保住
// (绝不回滚 —— 回滚等于把刚就位的新版丢掉), 但要留痕 restart=failed 并提示手动重启。
func TestRunSelfReplace_SpawnFailureKeepsDeploy(t *testing.T) {
	dir, probe := stageSelfReplaceProbe(t, "forge_new.exe", func(d string) {
		if err := os.WriteFile(filepath.Join(d, "forge.exe"), []byte("OLD-FORGE-EXE"), 0o755); err != nil {
			t.Fatalf("布景失败: %v", err)
		}
	})
	out, _ := runSelfReplaceChildRecording(t, probe, dir, selfReplaceSpawnFail)

	self, err := os.Executable()
	if err != nil {
		t.Skipf("拿不到测试二进制路径: %v", err)
	}
	si, err := os.Stat(self)
	if err != nil {
		t.Fatalf("stat 测试二进制失败: %v", err)
	}
	di, err := os.Stat(filepath.Join(dir, "forge.exe"))
	if err != nil {
		t.Fatalf("spawn 失败后 forge.exe 不该消失: %v\n%s", err, out)
	}
	if di.Size() != si.Size() {
		t.Errorf("spawn 失败后 forge.exe 被改动: 大小 %d != %d", di.Size(), si.Size())
	}
	ev, _ := os.ReadFile(filepath.Join(dir, ".forge", "events.jsonl"))
	if !strings.Contains(string(ev), `"restart":"failed"`) {
		t.Errorf("留痕缺 restart=failed: %s", ev)
	}
	if !strings.Contains(string(ev), `"result":"ok"`) {
		t.Errorf("就位成功这件事必须仍记为 result=ok: %s", ev)
	}
	if !strings.Contains(out, "自动重启失败") {
		t.Errorf("应提示用户手动重启, 实际输出:\n%s", out)
	}
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
