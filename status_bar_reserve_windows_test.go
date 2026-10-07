//go:build windows

package main

import "testing"

// TestReserveInputRow_NoConsole 控制台不可用(测试进程 stdout 为管道)时必须
// 原样返回、不 panic、不写任何换行 —— 非 TTY 下不得污染输出流。
//
// 为何单独成文件: 本用例依赖 consoleProcsOK (readline_windows.go, 仅 Windows 编译),
// 与它同源的跨平台用例 (TestInputReserveNewlines*) 留在 status_bar_reserve_test.go。
// 二者合在一个无 tag 文件里会让 Linux 侧 go test -c 直接编译失败 (T22 第 1 层)。
func TestReserveInputRow_NoConsole(t *testing.T) {
	old := consoleProcsOK
	consoleProcsOK = false
	defer func() { consoleProcsOK = old }()
	if got := reserveInputRow(42); got != 42 {
		t.Errorf("控制台不可用时 reserveInputRow(42)=%d, want 42", got)
	}
}
