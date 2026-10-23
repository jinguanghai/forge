//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// restartScriptPath 返回重启脚本落点 (Linux: .forge/restart_self.sh)。
//
// 归属 .forge/ 的理由与 Windows 版同源: 根目录是工作区, 自动生成物须有专属容器;
// 且 .forge-temp/ 启动即清空, 脚本执行中途被删 = 升级失败且程序不回来。
func restartScriptPath(workDir string) string {
	return filepath.Join(workDir, ".forge", "restart_self.sh")
}

// buildRestartScript Linux 侧 = sh 版 (纯函数在 upgrade_restart.go, 本机可测)。
func buildRestartScript(workDir string, pid int, ts string) string {
	return buildRestartScriptSH(workDir, pid, ts)
}

// writeRestartScript 落盘重启脚本 (Linux: 无 BOM, LF, 置可执行位)。
func writeRestartScript(scriptPath, workDir string, pid int, ts string) error {
	if err := atomicWrite(scriptPath, []byte(buildRestartScript(workDir, pid, ts))); err != nil {
		return err
	}
	return os.Chmod(scriptPath, 0o755)
}

// launchRestartScript 分离启动重启脚本。
//
// Setsid 是必需项而非优化: 脚本要活过主进程退出 —— 留在同一会话里会随终端
// 关闭/进程组信号一起死, 表现为「升级后程序不回来」。
func launchRestartScript(scriptPath, workDir string) error {
	cmd := exec.Command("sh", scriptPath)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动重启脚本失败: %v", err)
	}
	return nil
}
