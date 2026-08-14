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
