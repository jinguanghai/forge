package main

import (
	"context"
	"os/exec"
	"time"
)

// runWithTimeout 运行命令并强制超时保护：
//   - 正常路径：cmd.Run() 完成即返回
//   - 超时路径：杀进程树（含孙进程）后等待至多 2 秒让管道 EOF，然后
//     无论是否清理干净都强制返回 ctx.Err() —— 绝不让 Wait 永久阻塞挂死会话。
//
// 背景（2026-08-18 事故）：Windows 下 os.popen("setx ...") → cmd.exe → setx
// 孙进程继承 stdout 管道句柄，Go 的 CommandContext 超时只 Kill 直接子进程，
// 孙进程持管道 → cmd.Run() 的 Wait 永远等 EOF → 整个回合挂死 20 分钟。
// 此函数保证任何情况下超时最多 2 秒后强制返回错误给 LLM。
func runWithTimeout(ctx context.Context, cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killProcessTree(cmd)
		// 给进程树清理一点时间让管道 EOF；2 秒后无论结果强制返回。
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return ctx.Err()
	}
}
