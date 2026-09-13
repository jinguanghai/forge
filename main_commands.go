// main_commands.go: 交互命令分发: "/xx" 子命令的注册与路由

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

func cmdClear(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Print("\033[2J\033[H")
	printWelcome(cfg)
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

// gateDisplayNames 是 /tools 的展示名 (与 铸剑炉_GATES 同集合, 仅显示层带别名,
// 如 sh/bash、node/js)。提到包级是为了让一致性哨兵可比对 —— 展示层与清单脱节
// 会误导使用者 ("有这个 gate 吗")。
var gateDisplayNames = []string{
	"python", "go", "sh/bash", "node/js",
	"math", "logic", "regex", "knowledge",
	"chain", "self", "tcm", "browser",
}

func cmdTools(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("可用语言编译器 (12 gates):"))
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

func cmdMemhealth(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(memHealthReport(cfg.WorkDir))
	fmt.Println()
}

func cmdGatesync(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(gateSyncCheck(cfg.WorkDir, cfg.PluginReleaseDir))
	fmt.Println()
}

func cmdAnchor(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("锚点写入审计 (七期护栏):"))
	fmt.Println(anchorAuditSummary(cfg.WorkDir))
	fmt.Println()
}

func cmdMemdiag(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("☯ 记忆节律诊断 (π-φ):"))
	md := MemDiagMetrics(cfg.WorkDir)
	if md == nil {
		fmt.Println("  ✗ 诊断失败")
	} else {
		fmt.Printf("  %s\n", md.Summary)
		if len(md.Vector) == 8 {
			req, _ := json.Marshal(map[string]interface{}{"type": "diagnose", "vector": md.Vector, "domain": "memory"})
			_, res, errB := agent.forge.Build(string(req), "tcm", "")
			if errB != nil || res == nil || !res.OK {
				emsg := "诊断失败"
				if errB != nil {
					emsg = errB.Error()
				} else if res != nil && res.Error != "" {
					emsg = res.Error
				}
				fmt.Println("  " + color(ansi.red, "✗") + " " + emsg)
			} else {
				var dr map[string]interface{}
				if err := json.Unmarshal([]byte(res.Stdout), &dr); err != nil {
					fmt.Println("  " + color(ansi.red, "✗") + " 解析失败: " + err.Error())
				} else {
					fmt.Printf("  %s %s\n", bold("卦象:"), dr["dominant"])
					fmt.Printf("  %s %s (%s)\n", bold("六势态:"), dr["stage"], dr["stage_desc"])
					fmt.Printf("  %s %s\n", bold("优先战略:"), dr["recommendation"])
					if sc, ok := dr["strategies"].([]interface{}); ok && len(sc) > 0 {
						fmt.Print("  战略排序: ")
						for i, s := range sc {
							if i >= 3 {
								break
							}
							if m, ok := s.(map[string]interface{}); ok {
								fmt.Printf("%s(%.2f) ", m["name"], m["score"].(float64))
							}
						}
						fmt.Println()
					}
				}
			}
		}
		for _, a := range md.Advice {
			fmt.Println("  " + color(ansi.yellow, "•") + " " + a)
		}
	}
	fmt.Println()
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

func cmdCache(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("DeepSeek 前缀缓存命中:"))
	fmt.Println(cacheStatsSummary())
	fmt.Println(dim("  统计文件: cache_stats.jsonl (工作目录) · 数据由 stream_options.include_usage 采集"))
	fmt.Println()
}

func cmdDiagnose(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("☯ 铸剑炉自诊断 (八极八势·六势态):"))
	fs := agent.forge.Stats()
	builds, _ := fs["builds"].(int64)
	errs, _ := fs["errors"].(int64)
	hits, _ := fs["cache_hits"].(int64)
	size, _ := fs["cache_size"].(int64)

	// 运行指标 → 八极向量 [阳,阴,表,里,寒,热,虚,实]
	// 阳: 工具执行活跃度(近builds) 阴: 缓存稳定度 表: 外部接口 里: 内部协作
	// 寒: 错误抑制(反向) 热: 负载热度 虚: 资源空缺(缓存薄) 实: 积压占用
	yang := clampF(2+float64(builds%10), 0, 10)            // 构建活跃
	yin := clampF(4+float64(size%8), 0, 10)                // 缓存积累=承载稳定
	biao := clampF(3+float64(len(agent.history)%8), 0, 10) // 外部交互面
	li := clampF(5+float64(len(agent.history)%6), 0, 10)   // 内部上下文
	cold := clampF(10-float64(errs), 0, 10)                // 错误低=寒少
	heat := clampF(2+float64(errs%5), 0, 10)               // 错误高=热亢
	xu := clampF(10-float64(hits%10), 0, 10)               // 缓存命中低=虚
	shi := clampF(2+float64(size%6), 0, 10)                // 缓存占用=实
	vec := []float64{yang, yin, biao, li, cold, heat, xu, shi}

	fmt.Printf("  指标: builds=%d errors=%d cache_hits=%d cache_size=%d\n", builds, errs, hits, size)
	req, _ := json.Marshal(map[string]interface{}{"type": "diagnose", "vector": vec, "domain": "system"})
	_, res, errB := agent.forge.Build(string(req), "tcm", "")
	if errB != nil || res == nil || !res.OK {
		emsg := "诊断失败"
		if errB != nil {
			emsg = errB.Error()
		} else if res != nil && res.Error != "" {
			emsg = res.Error
		}
		fmt.Println("  " + color(ansi.red, "✗") + " " + emsg)
	} else {
		var dr map[string]interface{}
		if err := json.Unmarshal([]byte(res.Stdout), &dr); err != nil {
			fmt.Println("  " + color(ansi.red, "✗") + " 解析失败: " + err.Error())
		} else {
			fmt.Printf("  %s %s\n", bold("卦象:"), dr["dominant"])
			fmt.Printf("  %s %s (%s)\n", bold("六势态:"), dr["stage"], dr["stage_desc"])
			fmt.Printf("  %s %s\n", bold("优先战略:"), dr["recommendation"])
			if w, _ := dr["warning"].(string); w != "" {
				fmt.Println("  " + color(ansi.yellow, "⚠") + " " + w)
			}
			if sc, ok := dr["strategies"].([]interface{}); ok {
				fmt.Print("  战略排序: ")
				for i, s := range sc {
					if i >= 4 {
						break
					}
					if m, ok := s.(map[string]interface{}); ok {
						fmt.Printf("%s(%.2f) ", m["name"], m["score"].(float64))
					}
				}
				fmt.Println()
			}
		}
	}
	fmt.Println()
}

func cmdHealth(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	pong := agent.forge.Ping()
	fmt.Printf("  %s %s\n", bold("Forge:"), color(ansi.green, pong))
	if agent.llm.IsHealthy() {
		fmt.Printf("  %s %s\n", bold("LLM:"), color(ansi.green, "healthy"))
	} else {
		fmt.Printf("  %s %s\n", bold("LLM:"), color(ansi.red, "degraded"))
	}
	fmt.Println()
}

func cmdVision(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	arg := ""
	diag := false
	if len(parts) > 1 {
		arg = strings.TrimSpace(strings.Join(parts[1:], " "))
		for _, flag := range []string{"-d", "--diag", "诊断", "diag", "体检", "分析"} {
			if strings.Contains(arg, flag) {
				diag = true
				arg = strings.ReplaceAll(arg, flag, "")
			}
		}
		arg = strings.TrimSpace(arg)
	}
	if arg == "" {
		fmt.Println()
		fmt.Printf("  %s 用法: /看图 <目录> [-d]  — 识别目录下全部图片; 追加 -d 为诊断模式(识别+分析+建议)\n", color(ansi.yellow, "⚠"))
		fmt.Println()
		return
	}
	dir := arg
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cfg.WorkDir, dir)
	}
	imgs, err := discoverImagesInDir(dir)
	if err != nil {
		fmt.Printf("  %s 识图失败: %v\n", color(ansi.red, "✗"), err)
		fmt.Println()
		return
	}
	if len(imgs) == 0 {
		fmt.Printf("  %s 目录 %s 未发现图片 (JPEG/PNG/GIF/WebP)\n", color(ansi.yellow, "⚠"), dir)
		fmt.Println()
		return
	}
	mode := "描述"
	if diag {
		mode = "诊断"
	}
	fmt.Printf("  %s %s\n", color(ansi.blue, "🖼"), dim(fmt.Sprintf("识图 %d 张 (%s) → %s [%s模式]", len(imgs), dir, cfg.ModelVision, mode)))
	fmt.Println()
	// 构造带图的多模态 user 消息 (无工具), 强制视觉模型, 流式输出
	sys := ChatMessage{Role: "system", Content: buildSystemPromptStable(cfg.WorkDir, cfg.GatesEnabled)}
	prompt := "请识别以下图片，并逐张描述主要内容。"
	if diag {
		prompt = "你是视觉诊断专家。请对以下图片做三件事：\n" +
			"① 识别 — 完整转述看到的内容(文字/布局/对象/颜色/层级);\n" +
			"② 诊断 — 系统性列出问题点并分级 🔴高/🟡中/🟢低, 涵盖可读性/对齐/色彩对比/信息密度/层级/操作路径; 若是 CLI 界面再额外审视提示符/日志/状态栏/命令列表/配色;\n" +
			"③ 建议 — 每个问题给一句可立即执行的改法(调整对齐/加间距/改颜色/删冗余/移位置/补提示); \n" +
			"只依据图中真实存在的元素判断, 不编造; 若图片正常无明显问题, 明确说'未发现明显问题'。"
	}
	user := ChatMessage{Role: "user", Content: prompt, Images: imgs}
	msgs := []ChatMessage{sys, user}
	spin := startSpinner("铸剑炉识图中…")
	ch := agent.llm.ChatCompletionStream(agent.Context(), msgs, nil, cfg.ModelVision)
	var contentBuf, reasoningBuf strings.Builder
	var toolCalls []ToolCall
	streamErr := agent.processStream(ch, &contentBuf, &reasoningBuf, &toolCalls, cfg.ShowReasoning, spin.stop)
	spin.stop()
	if streamErr != nil {
		fmt.Printf("  %s %v\n", color(ansi.red, "✗"), streamErr)
	}
	fmt.Println()
}

func cmdDefault(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Printf("  %s 未知命令: %s (输入 /help 查看帮助)\n", color(ansi.red, "✗"), cmd)
}
