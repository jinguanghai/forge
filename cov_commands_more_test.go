package main

// cov_commands_more_test.go — 命令分发/各 cmd* 分支补测 (临时 WorkDir, 全部只读或写临时目录)
// 排除 /upgrade (会自替换 exe) 与 /diagnose /vision (外部耗时)。

import (
	"strings"
	"testing"
)

func TestCovCommands_SafeCommands(t *testing.T) {
	cmds := []string{
		"/help", "/h", "/?", "/stats",
		"/goal", "/goal list", "/goal ?", "/goal 测试目标", "/goal pause", "/goal resume",
		"/goal complete", "/goal blocked", "/goal clear", "/goal 未知子命令",
		"/theme", "/theme 未知主题名",
		"/gatesync", "/anchor", "/folded", "/folded 1", "/unfold x",
		"/tools", "/history", "/last", "/cache", "/health", "/memhealth", "/memdiag",
		"/model", "/router", "/router auto", "/router 非法模式", "/reasoning", "/reasoning on",
		"/reasoning off", "/scorecard", "/clear", "/不存在的命令", "/", "   ",
	}
	for _, c := range cmds {
		name := strings.ReplaceAll(strings.TrimSpace(c), " ", "_")
		if name == "" {
			name = "blank"
		}
		t.Run(name, func(t *testing.T) {
			agent, cfg, hist := newHandleCmdAgent(t)
			sr := false
			out := captureStdout(t, func() { handleCommand(c, agent, cfg, hist, &sr) })
			_ = out
		})
	}
}

func TestCovCommands_HelpAndWelcome(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := true
	out := captureStdout(t, func() {
		handleCommand("/help", agent, cfg, hist, &sr)
		printHelp()
	})
	if strings.TrimSpace(out) == "" {
		t.Error("help/welcome 输出为空")
	}
}
