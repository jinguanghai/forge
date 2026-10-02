//go:build windows

package main

// cov_commands2_more_test.go — 自诊断/看图/语音 命令分支补测 (临时 WorkDir, 不触网)

import (
	"testing"
)

func TestCovCommands2_DiagnoseVisionVoice(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	wd := t.TempDir()
	voiceMu.Lock()
	origEnabled, origName := voiceEnabled, voiceName
	voiceMu.Unlock()
	t.Cleanup(func() {
		voiceMu.Lock()
		voiceEnabled, voiceName = origEnabled, origName
		voiceMu.Unlock()
	})

	cmds := []string{
		"/diagnose",
		"/vision",
		"/看图",
		"/看图 " + wd,
		"/voice",
		"/voice on",
		"/voice off",
		"/voice 未知参数xyz",
	}
	for _, c := range cmds {
		c := c
		out := captureStdout(t, func() { handleCommand(c, agent, cfg, hist, &sr) })
		t.Logf("%-24s -> %d 字节输出", c, len(out))
	}
}

func TestCovCommands2_DiagnoseTwice(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	// 制造非零统计: 跑一次 gate 后诊断
	_, _, _ = agent.forge.Build("print(1)", "python", "")
	_ = captureStdout(t, func() { handleCommand("/diagnose", agent, cfg, hist, &sr) })
	_ = captureStdout(t, func() { handleCommand("/stats", agent, cfg, hist, &sr) })
}
