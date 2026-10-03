// main_commands.go: 交互命令分发入口 + 核心命令 (help/tools/folded/cache/clear)

package main

import (
	"fmt"
	"strings"
)

func handleCommand(input string, agent *AgentRunner, cfg *Config, historyFile string, showReasoning *bool) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help", "/h", "/?":
		cmdHelp(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/stats":
		cmdStats(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/goal":
		cmdGoal(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/history":
		cmdHistory(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/clear":
		cmdClear(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/reasoning":
		cmdReasoning(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/theme":
		cmdTheme(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/model":
		cmdModel(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/router":
		cmdRouter(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/upgrade":
		cmdUpgrade(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/tools", "/gates":
		cmdTools(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/folded", "/folds":
		cmdFolded(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/memhealth":
		cmdMemhealth(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/gatesync":
		cmdGatesync(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/scorecard":
		cmdScorecard(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/anchor":
		cmdAnchor(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/memdiag":
		cmdMemdiag(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/unfold":
		cmdUnfold(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/last":
		cmdLast(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/cache":
		cmdCache(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/diagnose":
		cmdDiagnose(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/health":
		cmdHealth(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/voice":
		cmdVoice(agent, cfg, parts, historyFile, showReasoning, cmd)
	case "/看图", "/see", "/vision":
		cmdVision(agent, cfg, parts, historyFile, showReasoning, cmd)
	default:
		cmdDefault(agent, cfg, parts, historyFile, showReasoning, cmd)
	}
}

func cmdHelp(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("铸剑炉 命令:"))
	fmt.Println("  " + color(ansi.cyan, "/help") + "      — 显示本帮助")
	fmt.Println("  " + color(ansi.cyan, "/stats") + "     — 显示会话统计")
	fmt.Println("  " + color(ansi.cyan, "/goal") + "      — 目标管理: /goal <标题> 创建, /goal list/pause/resume/complete/blocked <原因>/clear")
	fmt.Println("  " + color(ansi.cyan, "/sessions") + "  — 会话列表 (三期 I1)")
	fmt.Println("  " + color(ansi.cyan, "/new") + "       — 新会话")
	fmt.Println("  " + color(ansi.cyan, "/use") + "       — 恢复会话: /use <会话id>")
	fmt.Println("  " + color(ansi.cyan, "/history") + "   — 显示输入历史（最近20条）")
	fmt.Println("  " + color(ansi.cyan, "/clear") + "     — 清屏")
	fmt.Println("  " + color(ansi.cyan, "/reasoning") + " — 切换推理链显示")
	fmt.Println("  " + color(ansi.cyan, "/model") + "     — 显示当前模型信息")
	fmt.Println("  " + color(ansi.cyan, "/theme") + "     — 切换主题: /theme <neon|cold|warm>")
	fmt.Println("  " + color(ansi.cyan, "/health") + "    — 健康检查")
	fmt.Println("  " + color(ansi.cyan, "/tools") + "     — 列出可用语言编译器 (gates)")
	fmt.Println("  " + color(ansi.cyan, "/scorecard") + "  — gate Scorecard 排行榜: /scorecard [最近N条] (按 lang/gate 聚合 gate_audit)")
	fmt.Println("  " + color(ansi.cyan, "/voice") + "     — 语音播报开关/声线: /voice on|off|test|voices|声线 <名称>")
	fmt.Println("  " + color(ansi.cyan, "/听") + "        — 语音输入(听): 输入 /听 回车后开始说话, 本地离线转文字; 打字输入与语音输入分离, 互不干扰")
	fmt.Println("  " + color(ansi.cyan, "/看图") + "      — 识别目录下图片: /看图 <目录> [-d](诊断模式)")
	fmt.Println("  " + color(ansi.cyan, "/last") + "      — 显示最近一次工具完整输出")
	fmt.Println("  " + color(ansi.cyan, "/cache") + "     — DeepSeek 前缀缓存命中统计")
	fmt.Println("  " + color(ansi.cyan, "/folded") + "    — 折叠展开记忆系统总账")
	fmt.Println("  " + color(ansi.cyan, "/unfold") + "    — 展开折叠任务: /unfold <任务名>")
	fmt.Println("  " + color(ansi.cyan, "/memdiag") + "   — 记忆节律π-φ健康卦象")
	fmt.Println("  " + color(ansi.cyan, "/upgrade") + "   — 自检后自动切换到新版本窗口")
	fmt.Println()
	fmt.Println(dim("  快捷键: ↑↓ 历史 · Tab 命令补全 · Ctrl+C 取消任务(再按退出)"))
	fmt.Println()
}

func cmdClear(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Print("\033[2J\033[H")
	printWelcome(cfg)
}

// gateDisplayNames 是 /tools 的展示名 (与 铸剑炉_GATES 同集合, 仅显示层带别名,
// 如 sh/bash、node/js)。提到包级是为了让一致性哨兵可比对 —— 展示层与清单脱节
// 会误导使用者 ("有这个 gate 吗")。
var gateDisplayNames = []string{
	"python", "go", "node/js",
	"math", "logic", "regex", "knowledge",
	"chain", "self", "tcm", "browser", "relation", "media", "task",
}

func cmdTools(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	// 面数动态生成: 硬编码数字会随 gate 增减静默过期 (旧版写死 "12 gates" 而实际 13 面)。
	fmt.Println(bold(fmt.Sprintf("可用语言编译器 (%d gates):", len(gateDisplayNames))))
	for i, g := range gateDisplayNames {
		fmt.Printf("  %s %s", dim(fmt.Sprintf("%2d.", i+1)), color(ansi.cyan, g))
		if (i+1)%4 == 0 {
			fmt.Println()
		} else {
			pad := 18 - displayWidth(g)
			if pad < 1 {
				pad = 1
			}
			fmt.Print(strings.Repeat(" ", pad))
		}
	}
	fmt.Println()
	fs := agent.forge.Stats()
	fmt.Printf("  builds=%v cache_hits=%v errors=%v cache_size=%v\n",
		fs["builds"], fs["cache_hits"], fs["errors"], fs["cache_size"])
	fmt.Println()
}

func cmdFolded(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(ListFoldedTasks(cfg.WorkDir))
	fmt.Println()
}

func cmdCache(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("DeepSeek 前缀缓存命中:"))
	fmt.Println(cacheStatsSummary())
	fmt.Println(dim("  统计文件: cache_stats.jsonl (工作目录) · 数据由 stream_options.include_usage 采集"))
	fmt.Println()
}

func cmdDefault(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Printf("  %s 未知命令: %s (输入 /help 查看帮助)\n", color(ansi.red, "✗"), cmd)
}
