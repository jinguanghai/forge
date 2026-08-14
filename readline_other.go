//go:build !windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// errInterrupt is defined on all platforms so main.go can reference it.
var errInterrupt = errors.New("interrupt")

// readLine on non-Windows platforms: plain line mode (no history navigation).
func readLine(prompt string, history []string) (string, error) {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
