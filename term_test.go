// term_test.go: 终端尺寸探测的跨平台契约。
//
// 本文件必须能在 Windows 与 Linux 双平台编译 —— 只允许引用两平台【共有】的符号:
// getTermWidth / consoleWindowRect / ensureConsoleProbe。
// 严禁引用 resolveConsoleProcs / coord / smallRect / consoleProcsOK(仅 Windows),
// 否则 Windows 能过、Linux 崩 —— 而铸剑炉部署在 Linux VPS。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerm_GetTermWidth(t *testing.T) {
	w := getTermWidth()
	if w < 0 {
		t.Errorf("宽度不得为负: %d", w)
	}
	// 幂等: 同环境两次调用结果一致
	if again := getTermWidth(); again != w {
		t.Errorf("宽度探测不稳定: %d vs %d", w, again)
	}
}

func TestTerm_ConsoleWindowRectConsistency(t *testing.T) {
	top, bottom, ok := consoleWindowRect()
	if !ok {
		// 非 Windows 或非控制台环境: 必须返回零值, 不得给出"看似有效"的数字
		if top != 0 || bottom != 0 {
			t.Errorf("ok=false 时应返回零值, 实际 %d/%d", top, bottom)
		}
		return
	}
	if bottom < top {
		t.Errorf("窗口矩形倒置: top=%d bottom=%d", top, bottom)
	}
	top2, bottom2, ok2 := consoleWindowRect()
	if ok2 != ok || top2 != top || bottom2 != bottom {
		t.Errorf("矩形探测不稳定: (%d,%d,%v) vs (%d,%d,%v)", top, bottom, ok, top2, bottom2, ok2)
	}
}

func TestTerm_EnsureConsoleProbeIdempotent(t *testing.T) {
	// 幂等且无副作用(非 Windows 为空实现); 连续调用不得 panic
	ensureConsoleProbe()
	ensureConsoleProbe()
}

func TestTerm_PlatformFilesDefineSameAPI(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	shared := []string{
		"func getTermWidth() int",
		"func consoleWindowRect() (top, bottom int, ok bool)",
		"func ensureConsoleProbe()",
	}
	for _, name := range []string{"term_windows.go", "term_other.go"} {
		src, err := os.ReadFile(filepath.Join(wd, name))
		if err != nil {
			t.Errorf("读 %s 失败: %v", name, err)
			continue
		}
		s := string(src)
		for _, want := range shared {
			if !strings.Contains(s, want) {
				t.Errorf("%s 缺 %q —— 跨平台缺口(Linux 构建会失败)", name, want)
			}
		}
	}
}

func TestTerm_WindowsUsesWindowRectNotBufferSize(t *testing.T) {
	// 教训固化: getTermWidth 必须取【窗口矩形宽度】(用户可见列数),
	// 而非缓冲区宽度 Size.X —— 缓冲区可远大于窗口(含滚动区),
	// 用它做折行/光标定位会把光标定到屏幕外, 表现为"输入看不见"。
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(wd, "term_windows.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "info.Window.Right") || !strings.Contains(s, "info.Window.Left") {
		t.Error("getTermWidth 必须用 info.Window 矩形宽度, 不得直接用缓冲区 Size.X")
	}
	if !strings.Contains(s, "info.Window.Top") || !strings.Contains(s, "info.Window.Bottom") {
		t.Error("consoleWindowRect 必须用 info.Window.Top/Bottom")
	}
}

func TestTerm_OtherPlatformReturnsZero(t *testing.T) {
	// 非 Windows 实现的契约: 无控制台 API → 返回零值, 由调用方兜底
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(wd, "term_other.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "//go:build !windows") {
		t.Error("term_other.go 缺 build 约束 —— 会与 Windows 版重复定义")
	}
	if !strings.Contains(s, "return 0") {
		t.Error("term_other.go 的 getTermWidth 应返回 0")
	}
}
