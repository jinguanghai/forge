//go:build windows

package main

import "time"

// approvalKeyProbe 委托模式倒计时期间的按键探针 (Windows)。
//
// 复用行编辑器的非阻塞控制台窥视 waitInput (readline_windows.go): 只窥视不消费,
// 不用 goroutine+channel 读超时 —— 后者超时后读协程仍挂在 stdin 上, 会吞掉主人
// 下一条输入。VPS 侧无此函数, 由 approval_delegate_other.go 提供同签名实现。
func approvalKeyProbe(d time.Duration) bool { return waitInput(d) }
