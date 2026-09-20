//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// enableWindowsUTF8 将 Windows 控制台代码页设为 UTF-8 (CP_UTF8 = 65001)，
// 保证中文输出不乱码。
func enableWindowsUTF8() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	// 输出代码页与输入代码页都设为 UTF-8 (CP_UTF8 = 65001)，否则交互
	// 模式下粘贴/键入的中文可能被控制台按 GBK 解释成乱码。
	k32.NewProc("SetConsoleOutputCP").Call(65001)
	k32.NewProc("SetConsoleCP").Call(65001)

	// 启用虚拟终端处理，让 \033[K 等 ANSI 光标控制在旧版控制台配置下也生效。
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	const enableVirtualTerminalProcessing = 0x0004
	if r1, _, _ := k32.NewProc("GetConsoleMode").Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r1 != 0 {
		k32.NewProc("SetConsoleMode").Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	}
}
