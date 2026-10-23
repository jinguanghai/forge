package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// exe_names_test.go —— exe 名单一来源的判据 (T22 第 2 层)。
//
// 病根形态: 写死 "forge.exe" 在 Windows 上永远正确, 在 Linux 上永远错误 ——
// 而错误只在「自升级/自替换」时暴露(编译产物叫 forge 而非 forge.exe),
// 报错还是「编译成功但新版本未生成」这种误导性文案。
// 判据 = 名字必须由平台后缀现场推导, 且 Windows 侧逐字节不变。

func TestMainExeNamePlatformSuffix(t *testing.T) {
	got := mainExeName()
	want := "forge"
	if runtime.GOOS == "windows" {
		want = "forge.exe"
	}
	if got != want {
		t.Errorf("mainExeName() = %q, 在 %s 上期望 %q", got, runtime.GOOS, want)
	}
}

func TestNewExeNamePlatformSuffix(t *testing.T) {
	got := newExeName()
	want := "forge_new"
	if runtime.GOOS == "windows" {
		want = "forge_new.exe"
	}
	if got != want {
		t.Errorf("newExeName() = %q, 在 %s 上期望 %q", got, runtime.GOOS, want)
	}
}

// TestExeNamesDifferAndShareBase: 两个名字必须同基名且互不相同 ——
// 若相等, 自升级会直接覆盖生产二进制(「没冒烟就先替换」事故的形态)。
func TestExeNamesDifferAndShareBase(t *testing.T) {
	m, n := mainExeName(), newExeName()
	if m == n {
		t.Fatalf("mainExeName 与 newExeName 相同 (%q) —— 自升级会覆盖生产二进制", m)
	}
	if !strings.HasPrefix(n, exeBaseName) || !strings.HasPrefix(m, exeBaseName) {
		t.Errorf("两个名字都应以 %q 开头: %q / %q", exeBaseName, m, n)
	}
}

// TestExeNameCallSitesAvoidLiterals 结构哨兵: 升级/自替换路径不得再出现写死的 exe 名。
// 这类写死在 Windows 上完全无害, 只有在 Linux 上才暴露 —— 正因如此需要判据而非自觉。
func TestExeNameCallSitesAvoidLiterals(t *testing.T) {
	// 只查调用点, 不查 exe_names.go 本身 —— 那里是字面量的定义与说明处,
	// 注释里提及 "forge.exe" 是解释「为何要抽出来」, 不是写死使用。
	for _, f := range []string{"upgrade.go", "forge_self.go", "forge_self_deploy.go", "main_startup.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("读 %s: %v", f, err)
			continue
		}
		s := string(b)
		for _, lit := range []string{`"forge.exe"`, `"forge_new.exe"`} {
			if strings.Contains(s, lit) {
				t.Errorf("%s 仍写死 %s —— 应改用 mainExeName()/newExeName()", f, lit)
			}
		}
	}
}
