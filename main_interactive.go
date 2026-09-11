package main

// main_interactive.go — 运行模式拆分: 非交互单次查询 + 交互式 REPL。

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// runNonInteractive 非交互模式: 单次查询, 不保存检查点 (避免污染主会话)。
func runNonInteractive(cfg *Config, agent *AgentRunner, query string) {
	fmt.Printf("%s %s\n\n", color(ansi.cyan, "▶"), query)
	if blocked, kind, hit, level := checkInputGuard(query); blocked {
		if level == "critical" {
			allowed, reason, rerr := agent.ReviewCritical(query, kind, hit)
			logGuardEvent(cfg.WorkDir, level, kind, hit, guardVerdictStr(allowed, rerr), reason, query)
			if rerr != nil {
				fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」(独立复核不可用, 安全默认拒绝)\n", color(ansi.yellow, "🛡"), kind, hit)
				fmt.Println(dim("此指令涉及破坏性/攻击性操作, 未交给模型执行。"))
				os.Exit(2)
			}
			if allowed {
				fmt.Printf("\n%s 输入护栏 [%s] 命中「%s」但已通过独立复核, 继续执行: %s\n", color(ansi.green, "🛡"), kind, hit, reason)
			} else {
				fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」独立复核拒绝: %s\n", color(ansi.yellow, "🛡"), kind, hit, reason)
				fmt.Println(dim("此指令涉及破坏性/攻击性操作, 未交给模型执行。"))
				os.Exit(2)
			}
		} else {
			logGuardEvent(cfg.WorkDir, level, kind, hit, "blocked", "词库直接阻断", query)
			fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」\n", color(ansi.yellow, "🛡"), kind, hit)
			fmt.Println(dim("此指令涉及越权或注入意图, 未交给模型执行。"))
			os.Exit(2)
		}
	}
	agentBusy.Store(true)
	agent.SaveCheckpoint = false
	if err := agent.RunStream(query); err != nil {
		if err == context.Canceled {
			fmt.Fprintf(os.Stderr, "\n%s Interrupted\n", color(ansi.yellow, "⚡"))
		} else {
			fmt.Fprintf(os.Stderr, "\n%s %v\n", color(ansi.red, "✗"), err)
			os.Exit(1)
		}
	}
	agentBusy.Store(false)
	speakLastReply(agent)
	fmt.Printf("\n%s  %s\n", statusBarCacheText(agent, 20), dim(agent.stats.StringZh()))
	cs := cacheStatsSummary()
	if strings.Contains(cs, "请求数") {
		fmt.Printf("%s\n", dim(strings.SplitN(cs, "\n", 2)[0]))
	}
}

// runInteractive 交互模式: REPL 主循环。
func runInteractive(cfg *Config, agent *AgentRunner, showReasoning bool, historyFile string) {
	// 会话检查点: 检测上次会话, 提示可恢复
	var lastCkpH []ChatMessage
	if sessions, err := listSessions(cfg.WorkDir); err == nil && len(sessions) > 0 {
		top := sessions[0]
		topTitle := top.Title
		if topTitle == "" {
			topTitle = "(无标题)"
		}
		fmt.Printf("%s 历史会话 %d 个, 最近: %s %s\n",
			color(ansi.cyan, "🗂"), len(sessions), dim(top.ID), dim(topTitle))
		fmt.Println(dim("输入 /sessions 查看全部, /use <id> 恢复指定会话；直接输入新指令则开始新会话。"))
		fmt.Println()
	}
	if h, savedAt, err := loadCheckpoint(cfg.WorkDir); err == nil && len(h) > 0 {
		lastCkpH = h
		fmt.Printf("%s 检测到上次会话检查点（保存于 %s, 共 %d 轮对话）\n",
			color(ansi.cyan, "♻"), savedAt, len(h)/2)
		fmt.Println(dim("输入 恢复  可续接上次会话上下文；直接输入新指令则开始新会话。"))
		fmt.Println()
	}

	lineHistory := readHistory(historyFile, 50)
	multiLineBuf := strings.Builder{}
	inMultiLine := false

	for {
		drawStatusBar(agent, cfg.WorkDir) // 底部状态栏: 每轮刷新
		prompt := tuiGradient("铸剑炉", tuiPrimePalette())
		if showReasoning {
			prompt += color(ansi.yellow, "·R")
		}
		if inMultiLine {
			prompt = color(ansi.dim, " ... ")
		} else {
			prompt += " » "
		}

		line, err := readLine(prompt, lineHistory)
		pastedMultiLine := strings.Contains(line, "\n")
		if err != nil {
			if errors.Is(err, errInterrupt) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				fmt.Println()
				break
			}
			fmt.Fprintf(os.Stderr, "%s 输入错误: %v\n", color(ansi.red, "✗"), err)
			continue
		}

		trimmedLine := strings.TrimSpace(line)
		if !pastedMultiLine && strings.HasSuffix(trimmedLine, "\\") {
			core := strings.TrimSuffix(trimmedLine, "\\")
			if !isPathLike(core) {
				inMultiLine = true
				multiLineBuf.WriteString(core)
				multiLineBuf.WriteString("\n")
				continue
			}
		}
		if inMultiLine {
			multiLineBuf.WriteString(line)
			line = multiLineBuf.String()
			multiLineBuf.Reset()
			inMultiLine = false
		}

		if !pastedMultiLine && !inMultiLine && unbalancedDelimiters(line) {
			inMultiLine = true
			multiLineBuf.WriteString(line)
			multiLineBuf.WriteString("\n")
			continue
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// 折叠展开记忆系统: "展开<任务名>"→半显化摘要; "深入<任务名>"→读全文
		if mode, name := matchFoldUnfoldCmd(input); mode > 0 {
			var out string
			var err error
			if mode == 2 {
				out, err = UnfoldDeep(cfg.WorkDir, name)
			} else {
				out, err = UnfoldPreview(cfg.WorkDir, name)
			}
			if err == nil {
				fmt.Println()
				fmt.Println(out)
				fmt.Println()
				continue
			}
		}

		// 会话恢复命令: "恢复" 续接上次检查点
		if (input == "恢复" || strings.EqualFold(input, "resume")) && lastCkpH != nil {
			agent.RestoreHistory(lastCkpH)
			fmt.Printf("%s 已恢复上次会话（%d 轮对话上下文）\n", color(ansi.green, "♻"), len(lastCkpH)/2)
			lastCkpH = nil
			continue
		}

		// 会话命令: /sessions /new /use <id>
		lowCmd := strings.ToLower(input)
		switch {
		case input == "/sessions" || lowCmd == "会话列表" || lowCmd == "sessions":
			sess, serr := listSessions(cfg.WorkDir)
			if serr != nil || len(sess) == 0 {
				fmt.Printf("%s 暂无历史会话。输入 /new 开始一个新会话。\n", color(ansi.cyan, "🗂"))
				fmt.Println()
				continue
			}
			fmt.Printf("%s 会话列表 (共 %d 个):\n", color(ansi.cyan, "🗂"), len(sess))
			cur := currentSession()
			for _, s := range sess {
				title := s.Title
				if title == "" {
					title = "(无标题)"
				}
				mark := "  "
				if s.ID == cur {
					mark = "▶"
				}
				fmt.Printf("  %s %s  %s  更新于 %s\n", mark, dim(s.ID), dim(title), dim(s.UpdatedAt))
			}
			fmt.Println(dim("输入 /use <id> 恢复指定会话；/new 开始新会话。"))
			fmt.Println()
			continue

		case input == "/new" || lowCmd == "新会话" || lowCmd == "newsession":
			if err := agent.saveCheckpoint(cfg.WorkDir); err != nil {
				fmt.Fprintf(os.Stderr, "%s 旧会话检查点保存失败: %v\n", color(ansi.yellow, "⚡"), err)
			}
			s, cerr := createSession(cfg.WorkDir)
			if cerr != nil {
				fmt.Fprintf(os.Stderr, "%s 新会话创建失败: %v\n", color(ansi.red, "✗"), cerr)
				fmt.Println()
				continue
			}
			setEventSession(s.ID)
			agent.RestoreHistory(nil)
			lastCkpH = nil
			fmt.Printf("%s 已创建新会话 %s\n", color(ansi.green, "➕"), s.ID)
			fmt.Println(dim("对话历史已清空；输入 /sessions 可随时查看并切换。"))
			fmt.Println()
			continue

		case strings.HasPrefix(input, "/use ") || strings.HasPrefix(lowCmd, "使用会话 "):
			parts := strings.Fields(strings.TrimPrefix(input, "/use "))
			if len(parts) == 0 {
				fmt.Println(dim("用法: /use <会话id> (用 /sessions 查看) 或 使用会话 <id>"))
				fmt.Println()
				continue
			}
			id := parts[0]
			if _, lerr := loadSession(cfg.WorkDir, id); lerr != nil {
				fmt.Printf("%s 会话 %s 不存在 (用 /sessions 查看全部)\n", color(ansi.yellow, "🛡"), dim(id))
				fmt.Println()
				continue
			}
			setCurrentSession(id)
			setEventSession(id)
			h, savedAt, herr := loadCheckpoint(cfg.WorkDir)
			agent.RestoreHistory(h)
			lastCkpH = nil
			if herr == nil && len(h) > 0 {
				fmt.Printf("%s 已恢复会话 %s（保存于 %s, 共 %d 轮对话）\n",
					color(ansi.green, "♻"), id, savedAt, len(h)/2)
			} else {
				fmt.Printf("%s 已切换到会话 %s（空历史）\n", color(ansi.green, "♻"), id)
			}
			fmt.Println()
			continue
		}

		// Commands
		// 语音输入: /听 或 /听 <音频文件> → 本地离线ASR → 识别文字作为本轮输入。
		// 打字输入与语音输入分离: 键盘=打字通道, /听=语音通道, 两种输入方式互不干扰。
		var isVoice bool
		if hit, apath := isListenCmd(input); hit {
			var voiceText string
			if apath != "" {
				voiceText, _ = asrFile(apath)
			} else {
				voiceText, _ = asrListen()
			}
			voiceText = strings.TrimSpace(voiceText)
			if voiceText == "" {
				fmt.Printf("\n%s 未识别到语音，请重试\n", color(ansi.yellow, "⚠"))
				fmt.Println()
				continue
			}
			isVoice = true
			fmt.Printf("\n%s 语音输入: %s\n", color(ansi.cyan, "🎤"), voiceText)
			fmt.Println()
			input = voiceText
		}

		if !isVoice && (strings.HasPrefix(input, "/") || lowCmd == "升级" || lowCmd == "upgrade") {
			handleCommand(input, agent, cfg, historyFile, &showReasoning)
			if exitRequested {
				break
			}
			continue
		}

		if strings.ToLower(input) == "exit" || strings.ToLower(input) == "quit" {
			fmt.Println(dim("再见。"))
			break
		}

		// 折叠记忆相关性触发: 对话命中折叠项关键词→静默提示一次(24h去重)
		if rel := FindRelevantFold(cfg.WorkDir, input); rel != nil {
			fmt.Printf("%s 相关折叠记忆【%s】— %s\n",
				color(ansi.cyan, "📎"), rel.Name, truncateCN(rel.Summary, 40))
			fmt.Printf("   输入「展开%s」查看摘要, 「深入%s」查看全文\n", rel.Name, rel.Name)
			MarkHinted(cfg.WorkDir, rel.ID)
			fmt.Println()
		}

		// 输入护栏: 越权/注入指令在交给 LLM 前阻断
		if blocked, kind, hit, level := checkInputGuard(input); blocked {
			if level == "critical" {
				allowed, reason, rerr := agent.ReviewCritical(input, kind, hit)
				logGuardEvent(cfg.WorkDir, level, kind, hit, guardVerdictStr(allowed, rerr), reason, input)
				logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "level": level, "verdict": guardVerdictStr(allowed, rerr)})
				if rerr != nil {
					fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」(独立复核不可用, 安全默认拒绝)\n", color(ansi.yellow, "🛡"), kind, hit)
					fmt.Println(dim("此指令涉及破坏性/攻击性操作, 未交给模型执行。"))
					fmt.Println()
					continue
				}
				if !allowed {
					fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」独立复核拒绝: %s\n", color(ansi.red, "🚨 L3 守门人报警"), kind, hit, reason)
					fmt.Println(dim("此指令涉及破坏性/攻击性操作, 未交给模型执行。"))
					fmt.Println(dim("如确属正当需求(如防御演练), 请重新表述并说明用途后再次输入。"))
					fmt.Println()
					continue
				}
				fmt.Printf("\n%s 输入护栏 [%s] 命中「%s」但已通过独立复核, 继续执行: %s\n", color(ansi.green, "🛡"), kind, hit, reason)
				fmt.Println()
			} else {
				logGuardEvent(cfg.WorkDir, level, kind, hit, "blocked", "词库直接阻断", input)
				logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "level": level})
				fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」\n", color(ansi.yellow, "🛡"), kind, hit)
				fmt.Println(dim("此指令涉及越权或注入意图, 未交给模型执行。"))
				fmt.Println(dim("如确属正当需求, 请重新表述(例如说明用途)后再次输入。"))
				fmt.Println()
				continue
			}
		}

		// Save to history
		appendHistory(historyFile, input)

		// Run
		agentBusy.Store(true)
		runErr := agent.RunStream(input)
		agentBusy.Store(false)
		speakLastReply(agent)
		sigCount.Store(0) // 任务结束重置: 下次任务首个 Ctrl+C 仍是取消而非退出
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) {
				fmt.Fprintf(os.Stderr, "\n%s 任务已取消\n", color(ansi.yellow, "⚡"))
			} else {
				fmt.Fprintf(os.Stderr, "\n%s %v\n", color(ansi.red, "✗"), runErr)
			}
		}
		fmt.Println()
		fmt.Println(statusBarCacheText(agent, 20) + "  " + dim("─ "+agent.stats.StringZh()))
		fmt.Println()
	}

	// Session summary (可视化结算卡)
	if card := sessionSummaryCard(agent); card != "" {
		fmt.Println()
		for _, ln := range strings.Split(strings.TrimSuffix(card, "\n"), "\n") {
			fmt.Println(ln)
		}
	}
}
