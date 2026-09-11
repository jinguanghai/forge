//go:build !windows

// tts_other.go — 非 Windows 平台语音 stub: 无 winmm MCI, 语音不支持, 静默跳过。

package main

import "fmt"

var voiceEnabled = false
var voiceName = "zh-CN-XiaoxiaoNeural"
var knownVoices = []string{}

func voiceDefaultEnabled() bool { return false }
func voiceDefaultName() string  { return "zh-CN-XiaoxiaoNeural" }

func sanitizeForSpeech(s string) string { return s }

func voiceSpeak(text string) error  { return nil }
func speakLastReply(a *AgentRunner) {}

func cmdVoice(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println("  此平台不支持语音播报 (需 Windows + edge-tts)。")
	fmt.Println()
}
func isValidVoice(v string) bool { return false }
