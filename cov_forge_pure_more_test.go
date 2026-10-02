package main

// cov_forge_pure_more_test.go — forge.go 纯函数/文件工具分支补测

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCovForge_AsciiLangEmoji(t *testing.T) {
	langs := []string{"python", "go", "sh", "bash", "node", "javascript", "math", "logic",
		"knowledge", "regex", "chain", "self", "unknown"}
	for _, l := range langs {
		if got := asciiLangEmoji(l); got == "" {
			t.Errorf("asciiLangEmoji(%q) 为空", l)
		}
	}
}

func TestCovForge_HasGoPackageDecl(t *testing.T) {
	yes := []string{"package main", "// c\npackage main", "/* x */\npackage p", "\n\npackage p\nfunc f(){}"}
	no := []string{"", "func main(){}", "import \"fmt\"", "# python", "// only comment"}
	for _, c := range yes {
		if !hasGoPackageDecl(c) {
			t.Errorf("hasGoPackageDecl(%q) 应为 true", c)
		}
	}
	for _, c := range no {
		if hasGoPackageDecl(c) {
			t.Errorf("hasGoPackageDecl(%q) 应为 false", c)
		}
	}
}

func TestCovForge_IsTransientError(t *testing.T) {
	cases := []struct {
		r    ForgeGateResult
		want bool
	}{
		{ForgeGateResult{Timeout: true}, true},
		{ForgeGateResult{EnvFailure: true}, true},
		{ForgeGateResult{Stage: "compile", EnvFailure: true, Error: "gate 二进制缺失"}, true},
		{ForgeGateResult{Stage: "execute", Error: "boom timeout"}, false},
		{ForgeGateResult{Stage: "compile", Error: "syntax error"}, false},
		// 反例(20260927): 环境性关键词出现在错误文本里 ≠ 环境性失败。
		// 旧实现按文本匹配, 用户代码报错/打印含这些词即被误判为瞬时。
		{ForgeGateResult{Stage: "compile", Error: "connection refused"}, false},
		{ForgeGateResult{Stage: "compile", Error: "tool not found"}, false},
		{ForgeGateResult{Stage: "compile", Error: "找不到编译器"}, false},
		{ForgeGateResult{Stage: "compile", Error: "http error 502"}, false},
		{ForgeGateResult{Stage: "compile", Error: "device busy"}, false},
	}
	for i, c := range cases {
		if got := isTransientError(c.r); got != c.want {
			t.Errorf("用例%d: isTransientError=%v, want %v", i, got, c.want)
		}
	}
}

func TestCovForge_SafeExitCode(t *testing.T) {
	if got := safeExitCode(&exec.Cmd{}); got != -1 {
		t.Errorf("未启动 cmd 应返回 -1, got %d", got)
	}
	c := exec.Command("cmd", "/c", "exit 3")
	_ = c.Run()
	if got := safeExitCode(c); got != 3 {
		t.Errorf("exit 3 应返回 3, got %d", got)
	}
}

func TestCovForge_SupportedLangs(t *testing.T) {
	f := &Forge{}
	s := f.supportedLangs()
	if s == "" {
		t.Fatal("supportedLangs 为空")
	}
	for _, l := range []string{"python", "go", "math", "logic", "regex"} {
		if !strings.Contains(s, l) {
			t.Errorf("supportedLangs 缺少 %q: %s", l, s)
		}
	}
}

func TestCovForge_PruneSelfBackups(t *testing.T) {
	wd := t.TempDir()
	src := filepath.Join(wd, "forge.exe")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{".bak_1", ".bak_2", ".bak_3", ".bak_4", ".failed_1", ".stale_1"} {
		if err := os.WriteFile(src+n, []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := filepath.Glob(filepath.Join(wd, "forge.exe*"))
	pruneSelfBackups(wd, src, 1)
	after, _ := filepath.Glob(filepath.Join(wd, "forge.exe*"))
	if len(after) > len(before) {
		t.Errorf("pruneSelfBackups 反而增加文件: %d -> %d", len(before), len(after))
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("pruneSelfBackups 删除了源文件: %v", err)
	}
}

func TestCovForge_CleanupStaleTempDirs(t *testing.T) {
	forgeCleanupStaleTempDirs()
}
