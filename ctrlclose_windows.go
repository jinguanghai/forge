//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// Windows 控制台控制事件常量（kernel32 SetConsoleCtrlHandler）。
const (
	ctrlCloseEvent    = 0x2 // CTRL_CLOSE_EVENT    点击窗口 X / 关闭终端标签页
	ctrlLogoffEvent   = 0x5 // CTRL_LOGOFF_EVENT   注销
	ctrlShutdownEvent = 0x6 // CTRL_SHUTDOWN_EVENT 关机
)

// ctrlCloseCallback 必须包级持有引用，防止被 GC 回收导致回调失效。
var ctrlCloseCallback uintptr

var procSetConsoleCtrlHandler = kernel32DLL.NewProc("SetConsoleCtrlHandler")

// installCtrlCloseHandler 注册 Windows 控制台关闭事件处理。
//
// 问题：Go runtime 自带的 ctrlHandler 不拦截 CTRL_CLOSE_EVENT，因此点击
// 窗口 X / 关闭终端标签页时进程会被系统直接强杀（不执行任何清理，defer
// 不运行）。这里注册自己的 handler：对 CLOSE/LOGOFF/SHUTDOWN 返回 TRUE
// 表示"已处理"，系统便进入"等待进程退出"阶段（约 5 秒宽限）而非立即
// 强杀，我们借机保存缓存、打印再见后退出。
//
// 注意：回调运行在系统投递控制事件的线程上，且主 goroutine 可能阻塞在
// raw-mode stdin 读取（无法唤醒），因此必须在回调内同步完成清理并
// os.Exit(0)，不能走 channel/goroutine 协调。
func installCtrlCloseHandler(agent *AgentRunner) {
	ctrlCloseCallback = syscall.NewCallback(func(ctrlType uint32) uintptr {
		switch ctrlType {
		case ctrlCloseEvent, ctrlLogoffEvent, ctrlShutdownEvent:
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr, "%s 窗口关闭 — 保存缓存并退出...\n", color(ansi.yellow, "⚡"))
			agent.Shutdown()
			os.Exit(0)
		}
		return 0 // 其它事件（含 Ctrl+C）不处理，交给 runtime 的 os.Interrupt 路径
	})
	r, _, err := procSetConsoleCtrlHandler.Call(ctrlCloseCallback, 1)
	if r == 0 {
		fmt.Fprintf(os.Stderr, "%s 注册控制台关闭事件处理失败: %v\n", color(ansi.yellow, "⚠"), err)
	}
}
