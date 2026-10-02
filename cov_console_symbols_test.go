//go:build windows

package main

// cov_console_symbols_test.go — 控制台 API 符号名形态哨兵 (20260920)
//
// 背景: kernel32.dll 对含字符参数的 API 只导出 A/W 变体, 无后缀名根本不存在。
// 一旦写错, resolveConsoleProcs/probeConsoleProcs 必然失败 → consoleProcsOK 恒 false
// → readLine 永远走 readLineFallback、getTermWidth 恒返回 80、状态栏静默失效。
// 该缺陷不报错、不 panic, 609 个用例全绿也无法发现 (旧用例用 t.Skipf 降级掩盖)。
// 本哨兵用死程序锁定两类命名约定, 使同类退化在测试第一步即暴露。

import (
	"strings"
	"syscall"
	"testing"
)

// 含字符参数的 API: 必须带 A/W 后缀, 且必须真能解析。
func TestConsoleInputProcNamesMustHaveAorWSuffix(t *testing.T) {
	for _, c := range []struct {
		why string
		p   *syscall.LazyProc
	}{
		{"PeekConsoleInput 只有 A/W 变体", procPeekConsoleInput},
		{"ReadConsoleInput 只有 A/W 变体", procReadConsoleInput},
	} {
		name := c.p.Name
		if !strings.HasSuffix(name, "A") && !strings.HasSuffix(name, "W") {
			t.Errorf("%s: 符号名 %q 缺 A/W 后缀 → 解析必然失败 → 行编辑器静默降级", c.why, name)
			continue
		}
		if err := c.p.Find(); err != nil {
			t.Errorf("%s: 符号 %q 无法解析: %v", c.why, name, err)
		}
	}
}

// 对照面: 不含字符参数的 API 只有无后缀版本 (加 A/W 反而失败)。
// 固化两类约定, 防止"一律加 W"式的反向误修。
func TestNonStringConsoleProcsHaveNoSuffix(t *testing.T) {
	for _, c := range []struct {
		why string
		p   *syscall.LazyProc
	}{
		{"GetConsoleMode 无 A/W 变体", procGetConsoleMode},
		{"SetConsoleMode 无 A/W 变体", procSetConsoleMode},
		{"GetConsoleScreenBufferInfo 无 A/W 变体", procGetConsoleScreenBufferInfo},
	} {
		name := c.p.Name
		if strings.HasSuffix(name, "A") || strings.HasSuffix(name, "W") {
			t.Errorf("%s: 符号名 %q 不应带 A/W 后缀", c.why, name)
			continue
		}
		if err := c.p.Find(); err != nil {
			t.Errorf("%s: 符号 %q 无法解析: %v", c.why, name, err)
		}
	}
}

// 端到端: 全部符号解析后 consoleProcsOK 必须为 true (修复前恒 false)。
func TestConsoleProcsAllResolvable(t *testing.T) {
	resolveConsoleProcs()
	if !consoleProcsOK {
		t.Fatalf("consoleProcsOK 必须为 true: 符号解析失败 → 行编辑器/状态栏/终端宽度整体降级")
	}
}
