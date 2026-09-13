//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

type coord struct {
	X int16
	Y int16
}

type smallRect struct {
	Left, Top, Right, Bottom int16
}

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

var procGetConsoleScreenBufferInfo = kernel32DLL.NewProc("GetConsoleScreenBufferInfo")

// getTermWidth returns the console width in columns (0 if unavailable).
func getTermWidth() int {
	if !consoleProcsOK {
		return 80
	}
	h := syscall.Handle(os.Stdout.Fd())
	var info consoleScreenBufferInfo
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return 0
	}
	// 必须返回“窗口矩形宽度”（用户可见列数），而非缓冲区宽度 Size.X。
	// 缓冲区宽度可远大于窗口（含滚动区）；用它做折行/光标定位，
	// 超宽输入会把光标定位到屏幕外，表现为“输入看不见”。
	winW := int(info.Window.Right) - int(info.Window.Left) + 1
	if winW > 0 {
		return winW
	}
	return int(info.Size.X)
}

// consoleWindowRect returns visible window top/bottom in buffer Y coords.
func consoleWindowRect() (top, bottom int, ok bool) {
	if !consoleProcsOK {
		return 0, 0, false
	}
	h := syscall.Handle(os.Stdout.Fd())
	var info consoleScreenBufferInfo
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return 0, 0, false
	}
	top = int(info.Window.Top)
	bottom = int(info.Window.Bottom)
	return top, bottom, bottom >= top
}

// resolveConsoleProcs 无副作用地解析所有控制台 API 符号(仅 Find, 不调用/不改模式/不移动光标)。
// 供启动期提前初始化 consoleProcsOK, 使 drawStatusBar 首轮即可获取窗口矩形与宽度,
// 同时避免 probeConsoleProcs 在启动期移动光标/改动控制台模式的副作用。
func resolveConsoleProcs() {
	if consoleProcsOK {
		return
	}
	procs := []*syscall.LazyProc{
		procGetConsoleMode, procSetConsoleMode, procSetConsoleCursorPosition,
		procPeekConsoleInput, procReadConsoleInput, procMBToWideChar,
		procGetConsoleScreenBufferInfo,
	}
	ok := true
	for _, p := range procs {
		if err := p.Find(); err != nil {
			ok = false
			break
		}
	}
	if ok {
		consoleProcsOK = true
	}
}

// ensureConsoleProbe 确保控制台 API 符号已解析(幂等)。在进入交互循环前调用,
// 以保证 drawStatusBar 首轮即可获取窗口矩形与宽度(修复首轮状态栏缺失)。
func ensureConsoleProbe() {
	resolveConsoleProcs()
}
