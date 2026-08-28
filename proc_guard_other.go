//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// killProcessTree Unix 实现：杀直接子进程；若调用方设置了 Setpgid 则杀整个进程组。
func killProcessTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
