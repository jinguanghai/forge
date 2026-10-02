//go:build windows

package main

import (
	"os"
	"syscall"
)

// ensureCookedForApproval 审批读取前自检控制台输入模式 (20261002)。
//
// 背景: confirmDangerous 用 bufio.ReadString('\n') 等主人按 y, 该 API 依赖
// LINE_INPUT (回车产生 \n) 与 ECHO_INPUT (敲键可见) 模式位。前次生产事故:
// readLine 设 raw 退出若 defer 未跑 (强杀/超时退), 控制台永久停留 0x03F1
// (LINE|ECHO|WINDOW 三位被清), 主人在 (y/N) 提示处敲键完全无反应, 三次
// approval 索引缺位 (events.jsonl 可验)。
//
// 处置: 每次审批读取前自检, 若发现 LINE_INPUT 或 ECHO_INPUT 被关, 补回并
// 留痕 (事件类型 console_cookguard)。这是「死程序兜底」一则 — 不依赖
// readLine 的 defer 是否正确执行, 即便整条 readLine 链路全坏, 审批门仍可用。
func ensureCookedForApproval() {
	h := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	if err := getConsoleMode(h, &mode); err != nil {
		return
	}
	fixed := mode | enableLineInput | enableEchoInput
	if fixed == mode {
		return
	}
	if err := setConsoleMode(h, fixed); err != nil {
		return
	}
	logEvent("console_cookguard", "审批读取前补回 cooked (LINE_INPUT|ECHO_INPUT)",
		map[string]uint32{"before": mode, "after": fixed})
}
