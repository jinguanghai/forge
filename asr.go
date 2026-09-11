//go:build windows

// asr.go — 铸剑炉语音输入: 麦克风录音(听) → 本地离线ASR(sherpa-onnx) → 文字。
// 与 tts.go(说) 对应, 形成「听 → 模型 → 说」完整语音闭环。0 token / 0 元 / 本地离线下。

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// asrToolDir 定位持久 ASR 工具目录 (exe 同级 .forge/asr_tool)。
// 可用环境变量 FORGE_ASR_DIR 覆盖 (测试/部署场景)。
func asrToolDir() string {
	if d := strings.TrimSpace(os.Getenv("FORGE_ASR_DIR")); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return filepath.Join(".", ".forge", "asr_tool")
	}
	return filepath.Join(filepath.Dir(exe), ".forge", "asr_tool")
}

// asrEnv 子进程环境: 清代理(本地推理无需)+UTF-8。
func asrEnv() []string {
	env := []string{}
	for _, e := range os.Environ() {
		if strings.Contains(strings.ToUpper(e), "PROXY") {
			continue
		}
		env = append(env, e)
	}
	env = append(env, "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	return env
}

// asrFile 转写一个音频文件为文字 (asr.py, 16k 单声道 wav/flac/mp3 等)。
func asrFile(path string) (string, error) {
	script := filepath.Join(asrToolDir(), "asr.py")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("ASR 脚本不存在: %s", script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python", script, path)
	cmd.Env = asrEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ASR 转写: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// asrListen 麦克风录音→本地离线识别→文字 (listen.py, --seconds 0 = 静音检测, 最长30s)。
func asrListen() (string, error) {
	script := filepath.Join(asrToolDir(), "listen.py")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("听写脚本不存在: %s", script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python", script, "--seconds", "0")
	cmd.Env = asrEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("语音识别: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// isListenCmd 识别「语音输入」触发命令: /听、听、/语音、/listen、/voice 听 等。
// 返回 (是否命中, 文件路径或空=麦克风)。
func isListenCmd(input string) (bool, string) {
	s := strings.TrimSpace(input)
	low := strings.ToLower(s)
	switch low {
	case "/听", "听", "/语音", "/listen", "/voice 听", "/voice in", "/voice listen":
		return true, ""
	}
	for _, pfx := range []string{"/听", "/listen", "/语音"} {
		if strings.HasPrefix(low, pfx) {
			rest := strings.TrimSpace(s[len(pfx):])
			if rest != "" {
				rest = strings.Trim(rest, `"'`)
				if rest != "" {
					return true, rest
				}
			}
		}
	}
	return false, ""
}
