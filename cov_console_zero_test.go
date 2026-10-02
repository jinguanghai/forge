//go:build windows

package main

// cov_console_zero_test.go — Windows 控制台 API 包装层补测 (20260920)
//
// readline_windows.go / term_windows.go 里的包装函数此前零覆盖。这些是"判定"层:
// 句柄无效时必须如实报错 (而非假装成功), 符号解析必须幂等 ——
// 状态栏/行编辑器的可用性全部建立在它们之上。
//
// ✅ 已修缺陷 (20260920 发现 / 同日修复, 主人批准): kernel32.dll 不导出无后缀名的
//   PeekConsoleInput / ReadConsoleInput (只有 A/W 版本; 反之 GetConsoleMode 只有
//   无后缀版本)。定义处曾用无后缀名 → resolveConsoleProcs/probeConsoleProcs 必然
//   失败 → consoleProcsOK 恒为 false → 状态栏 (drawStatusBar→consoleWindowRect)
//   与折行感知行编辑器 (readLine) 的控制台探测链路整体走降级分支。
//   产品代码已改为 PeekConsoleInputW / ReadConsoleInputW; 本文件的 Skip 同步升级为
//   硬断言 (SKIP 不计失败, 缺陷曾被它掩盖), 符号名形态另由 cov_console_symbols_test.go 哨兵锁定。

import (
	"os"
	"syscall"
	"testing"
)

// 无效句柄: GetConsoleMode 必须失败, 不能假装成功。
func TestGetConsoleMode_InvalidHandleFails(t *testing.T) {
	var mode uint32
	if err := getConsoleMode(syscall.InvalidHandle, &mode); err == nil {
		t.Fatalf("无效句柄必须返回错误")
	}
}

// 无效句柄: SetConsoleMode 必须失败。
func TestSetConsoleMode_InvalidHandleFails(t *testing.T) {
	if err := setConsoleMode(syscall.InvalidHandle, 0); err == nil {
		t.Fatalf("无效句柄必须返回错误")
	}
}

// 窗口矩形: 形状自洽 (ok 时 bottom>=top), 不可用必须 ok=false 而非瞎报数。
func TestConsoleWindowRect_ShapeIsSane(t *testing.T) {
	top, bottom, ok := consoleWindowRect()
	if ok && bottom < top {
		t.Fatalf("ok=true 时必须 bottom>=top, 实际 top=%d bottom=%d", top, bottom)
	}
	if !ok && (top != 0 || bottom != 0) {
		t.Fatalf("ok=false 时应返回零值, 实际 top=%d bottom=%d", top, bottom)
	}
}

// 符号解析: 全部符号必须可解析 (否则状态栏/行编辑器静默降级)。
// 20260920 修复后由 Skip 升级为硬断言: 必须真失败, 不再允许被 Skip 掩盖。
func TestResolveConsoleProcs_SymbolsResolvable(t *testing.T) {
	resolveConsoleProcs()
	if consoleProcsOK {
		return
	}
	var missing []string
	for _, c := range []struct {
		name string
		p    *syscall.LazyProc
	}{
		{"GetConsoleMode", procGetConsoleMode},
		{"SetConsoleMode", procSetConsoleMode},
		{"MultiByteToWideChar", procMBToWideChar},
		{"SetConsoleCursorPosition", procSetConsoleCursorPosition},
		{"PeekConsoleInput", procPeekConsoleInput},
		{"ReadConsoleInput", procReadConsoleInput},
		{"GetConsoleScreenBufferInfo", procGetConsoleScreenBufferInfo},
	} {
		if err := c.p.Find(); err != nil {
			missing = append(missing, c.name)
		}
	}
	t.Fatalf("控制台符号解析失败 → consoleProcsOK 恒 false, 状态栏/行编辑器降级; 缺失: %v", missing)
}

// 幂等: ensureConsoleProbe 复调不得改变解析结果。
func TestEnsureConsoleProbe_Idempotent(t *testing.T) {
	ensureConsoleProbe()
	first := consoleProcsOK
	ensureConsoleProbe()
	if consoleProcsOK != first {
		t.Fatalf("复调改变了 consoleProcsOK: %v -> %v", first, consoleProcsOK)
	}
}

// probeConsoleProcs 在符号已解析时立即早退 (不得重复探测)。
func TestProbeConsoleProcs_EarlyReturnWhenResolved(t *testing.T) {
	ensureConsoleProbe()
	if !consoleProcsOK {
		t.Skip("符号未解析 (见 TestResolveConsoleProcs_SymbolsResolvable), 早退路径不适用")
	}
	probeConsoleProcs()
	if !consoleProcsOK {
		t.Fatalf("已解析状态下复调不得把 consoleProcsOK 置回 false")
	}
}

// setCursorPos: 不可用句柄下必须静默失败 (不 panic)。会移动真实光标, 但测试进程
// stdout 为管道时 SetConsoleCursorPosition 直接失败, 无副作用。
func TestSetCursorPos_NoPanic(t *testing.T) {
	setCursorPos(0, 0)
	setCursorPos(3, 7)
}

// probeConsoleProcs: 符号缺失时必须安全降级 (内部 mustFind panic 被 recover 捕获),
// 且必须幂等 —— 探测失败不得把状态在两次调用间来回翻转。
func TestProbeConsoleProcs_FailsSafeAndIdempotent(t *testing.T) {
	probeConsoleProcs()
	first := consoleProcsOK
	probeConsoleProcs()
	if consoleProcsOK != first {
		t.Fatalf("探测非幂等: %v -> %v", first, consoleProcsOK)
	}
}

// drawStatusBar: 控制台不可用 (consoleProcsOK=false) 时必须直接返回, 不 panic 不刷屏。
// 已知缺陷下 consoleWindowRect 恒 ok=false, 本用例固化"缺控制台即静默放弃"的降级语义。
func TestDrawStatusBar_DegradesWhenConsoleUnavailable(t *testing.T) {
	f := sinkStderr(t)
	drawStatusBar(nil, t.TempDir())
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("读 stderr 失败: %v", err)
	}
	if !consoleProcsOK && len(b) != 0 {
		t.Fatalf("控制台不可用时不应输出状态栏, 实际 %q", string(b))
	}
}
