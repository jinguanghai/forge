//go:build !windows

package main

import "time"

// approvalKeyProbe 非 Windows 平台: 无控制台输入窥视, 退化为纯等待 —— 仍满足
// 「默认放行」语义, 只是失去「按任意键撤销」的后悔窗口 (VPS 侧无人值守, 该差异
// 可接受)。保留同签名是为了让 approval_delegate.go 跨平台编译无缺口。
func approvalKeyProbe(d time.Duration) bool {
	time.Sleep(d)
	return false
}
