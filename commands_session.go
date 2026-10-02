// commands_session.go: 会话与配置类子命令 (目标/统计/路由/折叠/主题/历史/模型/推理/升级)

package main

import (
	"fmt"
	"os"
	"strings"
)

func cmdStats(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("会话统计:"))
	fmt.Println("  " + agent.stats.StringZh())
	fs := agent.forge.Stats()
	fmt.Printf("  forge: builds=%v cache_hits=%v errors=%v cache_size=%v\n",
		fs["builds"], fs["cache_hits"], fs["errors"], fs["cache_size"])
	// 按会话的 gate 成功率
	ss := collectSessionStats(cfg.WorkDir)
	if ss.Total > 0 {
		rate := float64(ss.OK) / float64(ss.Total) * 100
		sessTag := ss.SessionID
		if sessTag == "" {
			sessTag = "legacy"
		}
		fmt.Printf("  gates[%s]: %d 调用, 成功 %d, 失败 %d (成功率 %.0f%%)\n",
			sessTag, ss.Total, ss.OK, ss.Fail, rate)
		for _, g := range ss.Gates {
			gr := 0.0
			if g.Calls > 0 {
				gr = float64(g.OK) / float64(g.Calls) * 100
			}
			fmt.Printf("    %-10s %3d 调用 %3d 成功 %3d 失败 (%.0f%%)\n", g.Lang, g.Calls, g.OK, g.Fail, gr)
		}
	} else {
		fmt.Println("  gates: 本会话暂无工具调用记录")
	}
	fmt.Println()
}

func cmdGoal(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(strings.Join(parts[1:], " "))
	}
	lowArg := strings.ToLower(arg)
	fmt.Println()
	switch {
	case arg == "" || lowArg == "list" || lowArg == "?":
		g, err := loadGoal(cfg.WorkDir)
		if err != nil {
			fmt.Printf("  %s %v\n", color(ansi.red, "✗"), err)
		} else if g == nil {
			fmt.Println("  当前无目标。用法: /goal <标题> 创建新目标")
		} else {
			fmt.Printf("  %s %s\n", bold("目标:"), g.Title)
			fmt.Printf("  %s %s\n", bold("状态:"), goalStatusLabel(g.Status))
			fmt.Printf("  %s %s\n", bold("创建:"), g.CreatedAt)
			if g.BlockedReason != "" {
				fmt.Printf("  %s %s\n", bold("阻塞原因:"), g.BlockedReason)
			}
			fmt.Println(dim("  命令: /goal pause|resume|complete|blocked <原因>|clear"))
		}
	case lowArg == "pause" || lowArg == "resume" || lowArg == "complete" || lowArg == "clear":
		var err error
		if lowArg == "clear" {
			err = clearGoal(cfg.WorkDir)
			if err == nil {
				fmt.Printf("  %s 目标已清除\n", color(ansi.green, "🗑"))
			}
		} else {
			_, err = setGoalStatus(cfg.WorkDir, map[string]string{
				"pause": GoalPaused, "resume": GoalActive, "complete": GoalCompleted,
			}[lowArg], "")
			if err == nil {
				fmt.Printf("  %s 目标已 %s\n", color(ansi.green, "✔"), lowArg)
			}
		}
		if err != nil {
			fmt.Printf("  %s %v\n", color(ansi.yellow, "⚠"), err)
		}
	case strings.HasPrefix(lowArg, "blocked"):
		reason := strings.TrimSpace(arg[len("blocked"):])
		if _, err := setGoalStatus(cfg.WorkDir, GoalBlocked, reason); err != nil {
			fmt.Printf("  %s %v\n", color(ansi.yellow, "⚠"), err)
		} else {
			fmt.Printf("  %s 目标已标记阻塞: %s\n", color(ansi.red, "⛔"), reason)
		}
	default:
		if g, err := createGoal(cfg.WorkDir, arg); err != nil {
			fmt.Printf("  %s %v\n", color(ansi.yellow, "⚠"), err)
		} else {
			fmt.Printf("  %s 目标已创建: %s (%s)\n", color(ansi.green, "🎯"), g.Title, g.Status)
		}
	}
	fmt.Println()
}

func cmdHistory(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	history := readHistory(historyFile, 20)
	fmt.Println()
	if len(history) == 0 {
		fmt.Println(dim("  暂无历史记录。"))
	} else {
		fmt.Println(bold("最近输入:"))
		for i, h := range history {
			display := h
			if len(display) > 80 {
				display = display[:80] + "..."
			}
			fmt.Printf("  %s %s\n", dim(fmt.Sprintf("%2d.", i+1)), display)
		}
	}
	fmt.Println()
}

func cmdReasoning(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	*showReasoning = !*showReasoning
	if *showReasoning {
		os.Setenv("FORGE_SHOW_REASONING", "true")
		cfg.ShowReasoning = true
		fmt.Println(color(ansi.yellow, "  🧠 推理链显示: 开"))
	} else {
		os.Setenv("FORGE_SHOW_REASONING", "false")
		cfg.ShowReasoning = false
		fmt.Println(dim("  推理链显示: 关"))
	}
	fmt.Println()
}

func cmdTheme(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	if len(parts) > 1 {
		name := strings.ToLower(parts[1])
		switch name {
		case "neon", "cold", "warm":
			setTheme(name)
			fmt.Println("  " + color(ansi.green, "\u2713") + " 已切换主题 \u2192 " + tuiThemeInfo())
			fmt.Println("  " + dim("提示: 主题立即作用于边框/渐变/状态栏, 并持久化到 .forge/theme"))
		default:
			fmt.Println("  " + dim("未知主题: ") + name)
			fmt.Println("  可用: neon | cold | warm")
		}
	} else {
		fmt.Println("  " + tuiThemeInfo())
		fmt.Println("  " + dim("用法: /theme <neon|cold|warm>"))
	}
	fmt.Println()
}

func cmdModel(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Printf("  %s %s\n", bold("模型:"), cfg.Model)
	fmt.Printf("  %s %s\n", bold("路由:"), routerModeLabel(cfg.RouterMode))
	fmt.Printf("  %s %s\n", bold("思考强度:"), effortLabel(cfg))
	fmt.Printf("  %s %s\n", bold("Flash:"), cfg.ModelFlash)
	fmt.Printf("  %s %s\n", bold("Pro:  "), cfg.ModelPro)
	fmt.Printf("  %s %s\n", bold("接口:"), cfg.BaseURL)
	if agent.llm.IsHealthy() {
		fmt.Printf("  %s %s\n", bold("健康:"), color(ansi.green, "OK"))
	} else {
		fmt.Printf("  %s %s\n", bold("健康:"), color(ansi.red, "FAIL"))
	}
	fmt.Println()
	fmt.Println(dim("  命令: /router [auto|flash|pro|fixed] 切换路由模式"))
	fmt.Println()
}

func cmdRouter(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	arg := ""
	if len(parts) > 1 {
		arg = strings.ToLower(parts[1])
	}
	switch arg {
	case "", "?":
		fmt.Printf("  %s %s\n", bold("当前路由:"), routerModeLabel(cfg.RouterMode))
		fmt.Printf("  %s %s\n", bold("Flash:"), cfg.ModelFlash)
		fmt.Printf("  %s %s\n", bold("Pro:  "), cfg.ModelPro)
		fmt.Println(dim("  用法: /router auto|flash|pro|fixed"))
		fmt.Println(dim("  auto  = 简单任务→flash, 复杂任务→pro (按任务关键词自动判断)"))
		fmt.Println(dim("  flash = 全部用 flash (最省)   pro = 全部用 pro (最强)"))
		fmt.Println(dim("  fixed = 固定用 DEEPSEEK_MODEL 指定的模型 (兼容旧行为)"))
	case "auto", "flash", "pro", "fixed":
		cfg.RouterMode = arg
		fmt.Printf("  %s %s\n", bold("路由模式已切换:"), routerModeLabel(cfg.RouterMode))
		fmt.Println(dim("  (本次会话即时生效)"))
	default:
		fmt.Printf("  %s %s\n", color(ansi.red, "无效模式:"), arg)
		fmt.Println(dim("  可选: auto | flash | pro | fixed"))
	}
	fmt.Println()
}

func cmdUpgrade(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	if err := SelfUpgrade(cfg); err != nil {
		fmt.Printf("  %s %v\n", color(ansi.red, "✗"), err)
		fmt.Println(dim("  已中止，当前窗口保持不变。"))
		fmt.Println()
		return
	}
	fmt.Println()
	fmt.Println(bold("  ✅ 验证全部通过，正在切换到新窗口..."))
	fmt.Println(dim("  旧窗口将自动关闭，新窗口几秒后打开。"))
	fmt.Println()
	exitRequested = true
}

func cmdUnfold(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	if len(parts) < 2 {
		fmt.Println(dim("  用法: /unfold <任务名>   例: /unfold 输入端修复"))
		fmt.Println(dim("  深入全文: /unfold 深入<任务名>   总账: /folded"))
	} else {
		name := strings.Join(parts[1:], " ")
		var out string
		var err error
		if mode, nm := matchFoldUnfoldCmd(name); mode == 2 {
			out, err = UnfoldDeep(cfg.WorkDir, nm)
		} else {
			out, err = UnfoldPreview(cfg.WorkDir, name)
		}
		if err != nil {
			fmt.Println(color(ansi.red, "  ✗") + " " + err.Error())
		} else {
			fmt.Println(out)
		}
	}
	fmt.Println()
}

func cmdLast(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	out, ok := agent.LastOutput()
	if !ok {
		fmt.Println(dim("  暂无工具输出。"))
	} else {
		fmt.Println(bold("最近一次工具输出:"))
		for _, ol := range strings.Split(out, "\n") {
			if len(ol) > 160 {
				ol = ol[:160] + "..."
			}
			fmt.Printf("  %s %s\n", dim("│"), ol)
		}
	}
	fmt.Println()
}
