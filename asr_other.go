//go:build !windows

// asr_other.go — 非 Windows 平台语音输入 stub: 无 sounddevice/模型, 不支持, 返回不可用。

package main

import "fmt"

func asrToolDir() string { return ".forge/asr_tool" }
func asrEnv() []string   { return nil }

func asrFile(path string) (string, error) {
	return "", fmt.Errorf("此平台不支持语音转写 (asr.go 仅 Windows)")
}
func asrListen() (string, error) {
	return "", fmt.Errorf("此平台不支持语音输入 (asr.go 仅 Windows)")
}

func isListenCmd(input string) (bool, string) { return false, "" }
