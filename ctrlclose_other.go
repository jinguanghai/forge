//go:build !windows

package main

// installCtrlCloseHandler 非 Windows 平台无"控制台关闭事件"概念，
// 终端关闭走 SIGTERM/SIGHUP 路径，无需额外处理。
func installCtrlCloseHandler(agent *AgentRunner) {}
