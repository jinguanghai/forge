//go:build !windows

package main

// getTermWidth returns 0 on platforms without console width detection.
func getTermWidth() int {
	return 0
}
