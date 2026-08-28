//go:build windows

package main

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// killProcessTree 杀直接子进程 + taskkill /T /F 递归杀整棵进程树。
// /T = tree（含孙进程），/F = force（强杀，不等待正常退出）。
// HideWindow 避免强杀时闪现黑色 cmd 窗口。
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Kill() // 先杀直接子进程（幂等，重复杀无副作用）

	killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tk := exec.CommandContext(killCtx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = tk.Run() // 失败无妨：外层 runWithTimeout 已强制返回
}
