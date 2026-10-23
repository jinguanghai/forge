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
	lastCkpH := printSessionHints(cfg) // 会话检查点: 检测上次会话, 提示可恢复
	lineHistory := readHistory(historyFile, 50)
	asm := &lineAssembler{}

	for {
		input, quit, ready := readInputLine(agent, cfg, showReasoning, lineHistory, asm)
		if quit {
			break
		}
		if !ready {
			continue
		}
		outcome, next := handleLocalInput(input, cfg, agent, historyFile, &showReasoning, &lastCkpH)
		if outcome == localQuit {
			break
		}
		if outcome == localHandled {
			continue
		}
		printFoldHint(cfg, next)
		if guardInteractiveInput(cfg, agent, next) { // 输入护栏: 越权/注入指令在交给 LLM 前阻断
			continue
		}
		runInteractiveTurn(agent, next, historyFile)
	}

	printSessionSummary(agent) // Session summary (可视化结算卡)
}

// lineAssembler 累积跨行输入: 反斜杠续行、未闭合定界符、手动多行。
type lineAssembler struct {
	buf  strings.Builder
	inML bool
}

// feed 处理一行原始输入, 返回组装后的完整输入与是否可提交。
// ready=false 表示仍在续行中, 调用方应继续读下一行。
func (a *lineAssembler) feed(line string) (string, bool) {
	pasted := strings.Contains(line, "\n")
	trimmed := strings.TrimSpace(line)
	if !pasted && strings.HasSuffix(trimmed, "\\") {
		core := strings.TrimSuffix(trimmed, "\\")
		if !isPathLike(core) {
			a.inML = true
			a.buf.WriteString(core)
			a.buf.WriteString("\n")
			return "", false
		}
	}
	if a.inML {
		a.buf.WriteString(line)
		line = a.buf.String()
		a.buf.Reset()
		a.inML = false
	}
	if !pasted && unbalancedDelimiters(line) {
		a.inML = true
		a.buf.WriteString(line)
		a.buf.WriteString("\n")
		return "", false
	}
	return strings.TrimSpace(line), true
}

// buildPrompt 构造当前轮的输入提示符(含推理标记与多行续行标记)。
func buildPrompt(showReasoning, inMultiLine bool) string {
	prompt := tuiGradient("铸剑炉", tuiPrimePalette())
	if showReasoning {
		prompt += color(ansi.yellow, "·R")
	}
	if inMultiLine {
		return color(ansi.dim, " ... ")
	}
	return prompt + " » "
}

// readInputLine 刷新状态栏、读一行并组装。
// quit=true 表示收到中断/EOF; ready=false 表示本行不构成完整输入(续行中/空行/输入错误)。
func readInputLine(agent *AgentRunner, cfg *Config, showReasoning bool, hist []string, asm *lineAssembler) (input string, quit bool, ready bool) {
	drawStatusBar(agent, cfg.WorkDir) // 底部状态栏: 每轮刷新
	line, err := readLine(buildPrompt(showReasoning, asm.inML), hist)
	if err != nil {
		if errors.Is(err, errInterrupt) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
			fmt.Println()
			return "", true, false
		}
		fmt.Fprintf(os.Stderr, "%s 输入错误: %v\n", color(ansi.red, "✗"), err)
		return "", false, false
	}
	input, ready = asm.feed(line)
	if !ready || input == "" {
		return "", false, false
	}
	return input, false, true
}

// localOutcome 本地命令处理结果。
type localOutcome int

const (
	localUnhandled localOutcome = iota // 非本地命令, 交给 LLM
	localHandled                       // 已处理, 继续下一轮
	localQuit                          // 请求退出主循环
)

// handleLocalInput 处理不经过 LLM 的本地输入: 折叠展开、会话恢复、会话命令、语音、斜杠命令、退出。
// 返回处理结果与(可能被语音替换的)本轮输入。
func handleLocalInput(input string, cfg *Config, agent *AgentRunner, historyFile string, showReasoning *bool, lastCkpH *[]ChatMessage) (localOutcome, string) {
	// 折叠展开记忆系统: "展开<任务名>"→半显化摘要; "深入<任务名>"→读全文
	if handleFoldUnfold(cfg, input) {
		return localHandled, ""
	}
	// 会话恢复命令: "恢复" 续接上次检查点
	if (input == "恢复" || strings.EqualFold(input, "resume")) && *lastCkpH != nil {
		agent.RestoreHistory(*lastCkpH)
		fmt.Printf("%s 已恢复上次会话（%d 轮对话上下文）\n", color(ansi.green, "♻"), len(*lastCkpH)/2)
		*lastCkpH = nil
		return localHandled, ""
	}
	lowCmd := strings.ToLower(input)
	if handleSessionCmd(input, lowCmd, cfg, agent, lastCkpH) { // 会话命令: /sessions /new /use <id>
		return localHandled, ""
	}
	// 语音输入: /听 或 /听 <音频文件> → 本地离线ASR → 识别文字作为本轮输入。
	input, isVoice, vok := resolveVoiceInput(input)
	if !vok {
		return localHandled, ""
	}
	if !isVoice && (strings.HasPrefix(input, "/") || lowCmd == "升级" || lowCmd == "upgrade") {
		handleCommand(input, agent, cfg, historyFile, showReasoning)
		if exitRequested {
			return localQuit, ""
		}
		return localHandled, ""
	}
	if strings.ToLower(input) == "exit" || strings.ToLower(input) == "quit" {
		fmt.Println(dim("再见。"))
		return localQuit, ""
	}
	return localUnhandled, input
}

// printFoldHint 折叠记忆相关性触发: 对话命中折叠项关键词→静默提示一次(24h去重)。
func printFoldHint(cfg *Config, input string) {
	rel := FindRelevantFold(cfg.WorkDir, input)
	if rel == nil {
		return
	}
	fmt.Printf("%s 相关折叠记忆【%s】— %s\n",
		color(ansi.cyan, "📎"), rel.Name, truncateCN(rel.Summary, 40))
	fmt.Printf("   输入「展开%s」查看摘要, 「深入%s」查看全文\n", rel.Name, rel.Name)
	MarkHinted(cfg.WorkDir, rel.ID)
	fmt.Println()
}

// printSessionSummary 会话结束时打印可视化结算卡。
func printSessionSummary(agent *AgentRunner) {
	card := sessionSummaryCard(agent)
	if card == "" {
		return
	}
	fmt.Println()
	for _, ln := range strings.Split(strings.TrimSuffix(card, "\n"), "\n") {
		fmt.Println(ln)
	}
}

// handleSessionCmd 处理 /sessions、/new、/use <id> 三条会话命令。
// 返回 true 表示已处理(调用方应 continue); 切换会话时把 *lastCkpH 置 nil。
func handleSessionCmd(input, lowCmd string, cfg *Config, agent *AgentRunner, lastCkpH *[]ChatMessage) bool {
	switch {
	case input == "/sessions" || lowCmd == "会话列表" || lowCmd == "sessions":
		sess, serr := listSessions(cfg.WorkDir)
		if serr != nil || len(sess) == 0 {
			fmt.Printf("%s 暂无历史会话。输入 /new 开始一个新会话。\n", color(ansi.cyan, "🗂"))
			fmt.Println()
			return true
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
		return true

	case input == "/new" || lowCmd == "新会话" || lowCmd == "newsession":
		if err := agent.saveCheckpoint(cfg.WorkDir); err != nil {
			fmt.Fprintf(os.Stderr, "%s 旧会话检查点保存失败: %v\n", color(ansi.yellow, "⚡"), err)
		}
		s, cerr := createSession(cfg.WorkDir)
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "%s 新会话创建失败: %v\n", color(ansi.red, "✗"), cerr)
			fmt.Println()
			return true
		}
		setEventSession(s.ID)
		agent.RestoreHistory(nil)
		*lastCkpH = nil
		fmt.Printf("%s 已创建新会话 %s\n", color(ansi.green, "➕"), s.ID)
		fmt.Println(dim("对话历史已清空；输入 /sessions 可随时查看并切换。"))
		fmt.Println()
		return true

	case strings.HasPrefix(input, "/use ") || strings.HasPrefix(lowCmd, "使用会话 "):
		parts := strings.Fields(strings.TrimPrefix(input, "/use "))
		if len(parts) == 0 {
			fmt.Println(dim("用法: /use <会话id> (用 /sessions 查看) 或 使用会话 <id>"))
			fmt.Println()
			return true
		}
		id := parts[0]
		if _, lerr := loadSession(cfg.WorkDir, id); lerr != nil {
			fmt.Printf("%s 会话 %s 不存在 (用 /sessions 查看全部)\n", color(ansi.yellow, "🛡"), dim(id))
			fmt.Println()
			return true
		}
		setCurrentSession(id)
		setEventSession(id)
		h, savedAt, herr := loadCheckpoint(cfg.WorkDir)
		agent.RestoreHistory(h)
		*lastCkpH = nil
		if herr == nil && len(h) > 0 {
			fmt.Printf("%s 已恢复会话 %s（保存于 %s, 共 %d 轮对话）\n",
				color(ansi.green, "♻"), id, savedAt, len(h)/2)
		} else {
			fmt.Printf("%s 已切换到会话 %s（空历史）\n", color(ansi.green, "♻"), id)
		}
		fmt.Println()
		return true
	}
	return false
}

// guardInteractiveInput 交互模式输入护栏: 越权/注入指令在交给 LLM 前阻断。
// 返回 true 表示已阻断(调用方应 continue); false 表示放行, 继续正常执行。
func guardInteractiveInput(cfg *Config, agent *AgentRunner, input string) bool {
	blocked, kind, hit, level := checkInputGuard(input)
	if !blocked {
		return false
	}
	if level == "critical" {
		allowed, reason, rerr := agent.ReviewCritical(input, kind, hit)
		logGuardEvent(cfg.WorkDir, level, kind, hit, guardVerdictStr(allowed, rerr), reason, input)
		logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "level": level, "verdict": guardVerdictStr(allowed, rerr)})
		if rerr != nil {
			fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」(独立复核不可用, 安全默认拒绝)\n", color(ansi.yellow, "🛡"), kind, hit)
			fmt.Println(dim("此指令涉及破坏性/攻击性操作, 未交给模型执行。"))
			fmt.Println()
			return true
		}
		if !allowed {
			fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」独立复核拒绝: %s\n", color(ansi.red, "🚨 L3 守门人报警"), kind, hit, reason)
			fmt.Println(dim("此指令涉及破坏性/攻击性操作, 未交给模型执行。"))
			fmt.Println(dim("如确属正当需求(如防御演练), 请重新表述并说明用途后再次输入。"))
			fmt.Println()
			return true
		}
		fmt.Printf("\n%s 输入护栏 [%s] 命中「%s」但已通过独立复核, 继续执行: %s\n", color(ansi.green, "🛡"), kind, hit, reason)
		fmt.Println()
		return false
	}
	logGuardEvent(cfg.WorkDir, level, kind, hit, "blocked", "词库直接阻断", input)
	logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "level": level})
	fmt.Printf("\n%s 输入护栏 [%s] 已阻断: 命中「%s」\n", color(ansi.yellow, "🛡"), kind, hit)
	fmt.Println(dim("此指令涉及越权或注入意图, 未交给模型执行。"))
	fmt.Println(dim("如确属正当需求, 请重新表述(例如说明用途)后再次输入。"))
	fmt.Println()
	return true
}

// runInteractiveTurn 执行一轮对话: 记历史 → RunStream → 语音播报 → 状态栏结算。
// sigCount 在任务结束后重置: 下次任务首个 Ctrl+C 仍是取消而非退出。
func runInteractiveTurn(agent *AgentRunner, input, historyFile string) {
	// Save to history (隐私优先: 默认不落盘用户原文, 见 privacy.go)
	if persistConversation() {
		appendHistory(historyFile, input)
	}

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
}

// printSessionHints 启动时提示历史会话数与可恢复的检查点。
// 返回可恢复的检查点历史(nil 表示无可恢复)。
func printSessionHints(cfg *Config) []ChatMessage {
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
	return lastCkpH
}

// handleFoldUnfold 处理「展开<任务名>」/「深入<任务名>」折叠记忆命令。
// 返回 true 表示已处理(调用方应 continue)。
func handleFoldUnfold(cfg *Config, input string) bool {
	mode, name := matchFoldUnfoldCmd(input)
	if mode <= 0 {
		return false
	}
	var out string
	var err error
	if mode == 2 {
		out, err = UnfoldDeep(cfg.WorkDir, name)
	} else {
		out, err = UnfoldPreview(cfg.WorkDir, name)
	}
	if err != nil {
		return false
	}
	fmt.Println()
	fmt.Println(out)
	fmt.Println()
	return true
}

// resolveVoiceInput 处理 /听 语音输入: 命中则跑本地离线 ASR, 返回识别文本。
// 返回 (新输入, 是否语音, ok); ok=false 表示未识别到语音, 调用方应 continue。
// 打字输入与语音输入分离: 键盘=打字通道, /听=语音通道, 两者互不干扰。
func resolveVoiceInput(input string) (string, bool, bool) {
	hit, apath := isListenCmd(input)
	if !hit {
		return input, false, true
	}
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
		return input, false, false
	}
	fmt.Printf("\n%s 语音输入: %s\n", color(ansi.cyan, "🎤"), voiceText)
	fmt.Println()
	return voiceText, true, true
}
