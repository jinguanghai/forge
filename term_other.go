//go:build !windows

package main

// getTermWidth returns 0 on platforms without console width detection.
func getTermWidth() int {
	return 0
}

// consoleWindowRect returns 0/0/false on non-windows platforms.
func consoleWindowRect() (top, bottom int, ok bool) {
	return 0, 0, false
}

// ensureConsoleProbe 非 Windows 平台为空实现(无控制台 API 需要探针)。
func ensureConsoleProbe() {}

// coord 非 Windows 下的占位类型: 与 term_windows.go 的 coord 同构,
// 仅为让 status_bar.go 的跨平台调用(getCursorPos)通过编译。
type coord struct{ X, Y int16 }

// getCursorPos 非 Windows 平台恒返回不可用 —— 状态栏/输入行腾挪逻辑不启用。
func getCursorPos() (coord, bool) {
	return coord{}, false
}
