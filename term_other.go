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

// getTermHeight returns 0 on platforms without console height detection.
func getTermHeight() int {
	return 0
}

// ensureConsoleProbe 非 Windows 平台为空实现(无控制台 API 需要探针)。
func ensureConsoleProbe() {}
