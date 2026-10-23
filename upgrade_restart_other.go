//go:build !windows && !linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// restartScriptPath 返回重启脚本落点 (非 Windows 非 Linux: .forge/restart_self.sh)。
func restartScriptPath(workDir string) string {
	return filepath.Join(workDir, ".forge", "restart_self.sh")
}

// buildRestartScript 非 Windows 非 Linux 侧 = sh 版 (纯函数在 upgrade_restart.go)。
func buildRestartScript(workDir string, pid int, ts string) string {
	return buildRestartScriptSH(workDir, pid, ts)
}

// writeRestartScript 落盘重启脚本 (sh: 无 BOM, LF, 置可执行位)。
func writeRestartScript(scriptPath, workDir string, pid int, ts string) error {
	if err := atomicWrite(scriptPath, []byte(buildRestartScript(workDir, pid, ts))); err != nil {
		return err
	}
	return os.Chmod(scriptPath, 0o755)
}

// launchRestartScript 分离启动重启脚本。
func launchRestartScript(scriptPath, workDir string) error {
	cmd := exec.Command("sh", scriptPath)
	cmd.Dir = workDir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动重启脚本失败: %v", err)
	}
	return nil
}
