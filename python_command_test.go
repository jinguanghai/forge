package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakePythonPath 造一个只含指定命令名的假 PATH 目录 (Windows 自动补 .exe)。
// 用途: 把「候选序列」从环境里隔离出来 —— 否则判据读到的是本机真实 PATH,
// 在「本机恰好有 python」的机器上永远绿, 换台机器才红 (判据不可复现)。
func fakePythonPath(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		p := filepath.Join(dir, n+exeSuffix())
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("造假解释器 %s 失败: %v", p, err)
		}
	}
	return dir
}

// TestPythonCommandPlatformOrder: 平台优先项必须排第一 (Windows=python, 其他=python3),
// 且两者都可用时选中平台优先项。T25 的核心诉求就是「Linux 只有 python3 时可用」。
func TestPythonCommandPlatformOrder(t *testing.T) {
	cands := pythonCandidates()
	want := "python3"
	if runtime.GOOS == "windows" {
		want = "python"
	}
	if len(cands) != 2 || cands[0] != want {
		t.Fatalf("候选序列 = %v, 首元素应为 %q (平台优先项)", cands, want)
	}
	t.Setenv("PATH", fakePythonPath(t, cands[0], cands[1]))
	t.Setenv("FORGE_PYTHON", "")
	got := pythonCommand()
	if base := filepath.Base(got); base != cands[0]+exeSuffix() {
		t.Fatalf("pythonCommand() = %q, 期望平台优先项 %q", got, cands[0]+exeSuffix())
	}
}

// TestPythonCommandFallsBackToSecondCandidate: 平台优先项缺失时必须回落到次候选。
// 这是 T25 的实际场景 (Linux 无 python, 只有 python3); 删掉回落分支本用例即报红。
func TestPythonCommandFallsBackToSecondCandidate(t *testing.T) {
	second := pythonCandidates()[1]
	t.Setenv("PATH", fakePythonPath(t, second))
	t.Setenv("FORGE_PYTHON", "")
	got := pythonCommand()
	if base := filepath.Base(got); base != second+exeSuffix() {
		t.Fatalf("只剩 %q 时 pythonCommand() = %q, 期望回落到 %q", second, got, second+exeSuffix())
	}
}

// TestPythonCommandOverrideAndVisibleFailure: FORGE_PYTHON 逃生通道生效;
// 指向不存在的命令时不得把坏值当结果返回, 且两者都找不到时回落字面 "python"
// (fail-visible: 让「命令缺失」错误自然浮现, 不伪装成别的失败)。
func TestPythonCommandOverrideAndVisibleFailure(t *testing.T) {
	dir := fakePythonPath(t, pythonCandidates()[0])
	override := filepath.Join(dir, pythonCandidates()[0]+exeSuffix())
	t.Setenv("PATH", dir)
	t.Setenv("FORGE_PYTHON", override)
	if got := pythonCommand(); got != override {
		t.Fatalf("FORGE_PYTHON=%q 未生效, got %q", override, got)
	}
	bad := filepath.Join(dir, "no-such-python")
	t.Setenv("FORGE_PYTHON", bad)
	if got := pythonCommand(); got == bad {
		t.Fatalf("FORGE_PYTHON 指向不存在的命令 %q, 不得原样返回", bad)
	}
	t.Setenv("PATH", t.TempDir())
	t.Setenv("FORGE_PYTHON", "")
	if got := pythonCommand(); got != "python" {
		t.Fatalf("PATH 为空且无覆盖时应回落字面 python, got %q", got)
	}
}

// TestExpandArgsResolvesPython: 编译器表里的 "python" 必须经 expandArgs 归一化
// (Check/Exec 两条都走这里), 其他语言名不得被改动。
func TestExpandArgsResolvesPython(t *testing.T) {
	t.Setenv("PATH", fakePythonPath(t, pythonCandidates()[0]))
	t.Setenv("FORGE_PYTHON", "")
	args, place := expandArgs([]string{"python", "-u", "{file}"}, "src.py", ".", ".")
	if len(args) != 3 || !place || args[1] != "-u" || args[2] != "src.py" {
		t.Fatalf("expandArgs 展开结果异常: %v (place=%v)", args, place)
	}
	if args[0] == "python" {
		t.Fatal("expandArgs 未归一化 python —— T25 在 Linux 上会失效 (只有 python3)")
	}
	if args[0] != pythonCommand() {
		t.Fatalf("expandArgs[0] = %q, 期望与 pythonCommand() 一致 (%q)", args[0], pythonCommand())
	}
	other, _ := expandArgs([]string{"node", "-"}, "s.js", ".", ".")
	if other[0] != "node" {
		t.Fatalf("非 python 名字被改动: %v", other)
	}
}

// TestNoBarePythonExecInProduction: 结构哨兵 —— 生产代码里不得再出现裸
// exec.Command("python", …), 且 4 个自托管 gate 必须走 pythonCommand()。
// 白名单只有 asr.go / tts.go (硬编码在 //go:build windows 内, Linux 走 stub),
// 且本用例连它们的 build tag 一起断言 —— 否则去掉 tag 后硬编码会静默泄漏到 Linux。
func TestNoBarePythonExecInProduction(t *testing.T) {
	root := "."
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读根目录失败: %v", err)
	}
	skip := map[string]bool{"asr.go": true, "tts.go": true}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if skip[name] {
			continue
		}
		assertNoBarePythonExec(t, filepath.Join(root, name))
	}
	for _, f := range []string{"asr.go", "tts.go"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		if !strings.Contains(string(b), "//go:build windows") {
			t.Errorf("%s 含裸 python 调用却无 //go:build windows —— Linux 上会用到硬编码命令", f)
		}
	}
	// 根包 5 个调用点必须接线 (删掉任一处接线, 本断言报红)。
	wired := map[string]string{
		"forge_exec_util.go": "resolveExecutable(",
		"forge_gate_file.go": "resolveExecutable(",
		"forge_gate_host.go": "pythonCommand()",
		"main_startup.go":    "pythonCommand()",
		"upgrade.go":         "pythonCommand()",
	}
	for f, needle := range wired {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		if !strings.Contains(string(b), needle) {
			t.Errorf("%s 缺 T25 接线 %q —— python 在 Linux 上会解析不到解释器", f, needle)
		}
	}
	// 自托管 gate (单文件编译, 无法引用根包 → 各自内联同名 helper)
	ft := filepath.Join(".forge", "forge-tools")
	for _, f := range []string{"math_gate.go", "logic_gate.go", "regex_gate.go", "knowledge_gate.go"} {
		p := filepath.Join(ft, f)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", p, err)
		}
		s := string(b)
		if !strings.Contains(s, "func pythonCommand() string") {
			t.Errorf("%s 缺内联 pythonCommand —— Linux 上该 gate 无法启动", f)
		}
		if !strings.Contains(s, "pythonCommand()") {
			t.Errorf("%s 未调用 pythonCommand", f)
		}
		assertNoBarePythonExec(t, p)
	}
}

// assertNoBarePythonExec 逐行扫描: exec.Command 同行出现 "python" 字面量即违规。
func assertNoBarePythonExec(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "exec.Command") && strings.Contains(line, `"python"`) {
			t.Errorf("%s:%d 裸 exec.Command(\"python\") —— 应走 pythonCommand(): %s", path, i+1, strings.TrimSpace(line))
		}
	}
}
