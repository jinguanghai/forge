package main

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// agentBusy is true while a task is running (used by the signal handler to
// decide whether Ctrl+C cancels the task or exits the program).
var agentBusy atomic.Bool

// exitRequested 由 /upgrade 命令置位; 主循环检测到后 break 退出,
// 走正常 return 路径触发 defer agent.Shutdown() 保存缓存。
var exitRequested bool

// sigCount: 从 main() 提升为包级 —— 信号处理与交互循环共用
// (首次 Ctrl+C 取消任务计数, 任务结束重置)。
var sigCount atomic.Int32

const AppVersion = "3.0.0"

func main() {
	// 拆分: 启动序 → main_startup.go, 运行模式 → main_interactive.go,
	// 命令处理 → main_commands.go, 工具函数 → main_utils.go
	runSelfReplace()
	enableWindowsUTF8()
	showReasoning, nonFlagArgs := parseFlags()
	cfg, err := runStartupConfig(showReasoning)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s Config error: %v\n", color(ansi.red, "✗"), err)
		os.Exit(1)
	}
	agent, err := NewAgentRunner(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s Agent init error: %v\n", color(ansi.red, "✗"), err)
		os.Exit(1)
	}
	installSignals(agent)
	// 进入交互前确保控制台 API 探针已初始化, 保证首轮 drawStatusBar 即可绘制
	ensureConsoleProbe()
	defer agent.Shutdown()
	installCtrlCloseHandler(agent)
	checkPeakHour(cfg)
	historyFile := setupHistoryFile(cfg)
	printHealthReport(cfg.WorkDir)
	printWelcome(cfg)
	if len(nonFlagArgs) > 0 {
		runNonInteractive(cfg, agent, strings.Join(nonFlagArgs, " "))
		return
	}
	runInteractive(cfg, agent, showReasoning, historyFile)
}
