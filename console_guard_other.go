//go:build !windows

package main

// ensureCookedForApproval 非 Windows 平台: tty 天然 cooked, 无 raw 残留风险,
// 此函数为空占位以满足 forge.go 跨平台编译。非 Windows 上 readLine 用
// bufio.ReadString 直接读 stdin, 不调 SetConsoleMode。
func ensureCookedForApproval() {}
