//go:build !windows

package main

// enableWindowsUTF8 在非 Windows 平台为空实现：
// 类 Unix 终端默认即为 UTF-8，无需设置控制台代码页。
func enableWindowsUTF8() {}
