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

	// 顺序至关重要：必须先 taskkill /T，再杀直接子进程。
	// /T 按父 PID 枚举整棵子树，父进程一旦死亡就枚举不到孙进程 → 孙进程成孤儿。
	// 2026-09-23 受控 2x2 实验（每格 3 次，探针经自检有效）：
	//   Kill→taskkill + exec.Command        → 3/3 漏网
	//   Kill→taskkill + CommandContext      → 3/3 漏网（修复前的产品路径）
	//   taskkill→Kill + CommandContext      → 3/3 漏网（只换顺序无效）
	//   taskkill→Kill + exec.Command        → 0/3 漏网（唯一通过）
	// 结论：顺序 与「不用 CommandContext」两个条件缺一不可。
	killCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tk := exec.CommandContext(killCtx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	tk.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = tk.Run()

	// 兜底：taskkill 失败或进程已自行退出时仍然生效（Kill 幂等，重复杀无副作用）。
	_ = cmd.Process.Kill()
}
