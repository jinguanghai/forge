package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode/utf8"
	"syscall"
	"time"
	"unsafe"
)

// agentBusy is true while a task is running (used by the signal handler to
// decide whether Ctrl+C cancels the task or exits the program).
var agentBusy atomic.Bool

// exitRequested 由 /upgrade 命令置位; 主循环检测到后 break 退出,
// 走正常 return 路径触发 defer agent.Shutdown() 保存缓存。
var exitRequested bool

const AppVersion = "3.0.0"

func main() {
	// ─── 自替换：forge_new.exe 启动时替换旧 forge.exe ──────
	myName := filepath.Base(os.Args[0])
	if strings.TrimSuffix(myName, ".exe") == "forge_new" {
		exeDir := filepath.Dir(os.Args[0])
		oldExe := filepath.Join(exeDir, "forge.exe")
		newExe := filepath.Join(exeDir, "forge_new.exe")
		// 替换旧二进制
		if _, err := os.Stat(newExe); err == nil {
			os.Remove(oldExe) // 忽略错误（可能被占用）
			os.Rename(newExe, oldExe)
		}
	}

	// Enable UTF-8 on Windows console
	enableWindowsUTF8()

	// Parse flags
	showReasoning := true
	nonFlagArgs := []string{}
	for _, arg := range os.Args[1:] {
		// Flags are only recognized before any query text. Once a query
		// has started, everything (including "-..." tokens) belongs to the
		// query, so "告诉我 --version 是什么" is not swallowed by flags.
		if len(nonFlagArgs) > 0 {
			nonFlagArgs = append(nonFlagArgs, arg)
			continue
		}
		switch arg {
		case "--reasoning", "-r":
			showReasoning = true
		case "--help", "-h":
			printHelp()
			return
		case "--version", "-v":
			fmt.Printf("铸剑炉 v%s — 流式智能体 · 编译器沙箱\n", AppVersion)
			return
		default:
			nonFlagArgs = append(nonFlagArgs, arg)
		}
	}

	if showReasoning {
		os.Setenv("FORGE_SHOW_REASONING", "true")
	}

	// Load config
	cfg, err := LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s Config error: %v\n", color(ansi.red, "✗"), err)
		os.Exit(1)
	}
	cfg.ShowReasoning = showReasoning

	// ─── 行为事件日志 (I-2): .forge\events.jsonl 追加式证据链 ───
	initEventLog(cfg.WorkDir)

	// ─── 排空式重启报告 (I-1): 检测上次自改审计 → 报告 → 标记 done ───
	reportLastUpgrade(cfg.WorkDir)

	// ─── 记忆自愈: memory.json 损坏时自动从 .bak 恢复 ──────
	if recovered, herr := HealMemory(cfg.WorkDir); herr != nil {
		fmt.Fprintf(os.Stderr, "%s 记忆自愈失败: %v\n", color(ansi.yellow, "⚠"), herr)
	} else if recovered {
		fmt.Fprintf(os.Stderr, "%s 记忆已从 .bak 自动恢复\n", color(ansi.yellow, "♻"))
	}

	// ─── 启动时自动感知硬件环境 ──────
	senseScript := filepath.Join(cfg.WorkDir, "sense.py")
	if _, err := os.Stat(senseScript); err == nil {
		senseCmd := exec.Command("python", senseScript)
		senseCmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
		senseCmd.Run()
	}

	// 缓存度量: 统计文件放工作目录 (六西格玛 P0)
	setCacheStatPath(cfg.WorkDir)

	// Initialize agent
	agent, err := NewAgentRunner(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s Agent init error: %v\n", color(ansi.red, "✗"), err)
		os.Exit(1)
	}

	// ─── Signal handling ──────────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

	var sigCount atomic.Int32
	go func() {
		for sig := range sigCh {
			if agentBusy.Load() && sig == os.Interrupt && sigCount.Load() == 0 {
				// First Ctrl+C while a task is running → cancel the task only.
				sigCount.Add(1)
				fmt.Fprintf(os.Stderr, "\n%s 已取消当前任务（再次 Ctrl+C 退出）\n", color(ansi.yellow, "⚡"))
				agent.CancelCurrent()
				continue
			}
			if !agentBusy.Load() && sig == os.Interrupt {
				// Idle at the prompt: exit immediately. The main goroutine is
				// blocked in a raw-mode stdin read and cannot be woken up, so
				// the old 5s "graceful shutdown" wait deadlocked here.
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "%s 再见。\n", color(ansi.yellow, "⚡"))
				os.Exit(0)
			}
			// Task running (2nd Ctrl+C) or SIGTERM/SIGQUIT: cancel and exit.
			fmt.Fprintf(os.Stderr, "\n%s Received signal: %v — shutting down...\n",
				color(ansi.yellow, "⚡"), sig)
			agent.Shutdown()
			os.Exit(0)
		}
	}()

	defer func() {
		agent.Shutdown()
	}()

	// ─── Windows 控制台关闭事件: 点 X 也优雅退出 ──────
	installCtrlCloseHandler(agent)

	// 峰谷避让提醒 (六西格玛 Improve #3): DeepSeek 高峰时段价格×2
	checkPeakHour()

	// Setup history
	forgeDir := filepath.Join(cfg.WorkDir, ".forge")
	if err := os.MkdirAll(forgeDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to create forge directory %s: %v\n", forgeDir, err)
	}
	historyFile := filepath.Join(forgeDir, "history")

	// ─── 躯壳自检: 启动时报告最近24h错误(无问题则静默) ───────
	printHealthReport(cfg.WorkDir)

	// ─── Welcome ──────────────────────────────────────────
	printWelcome(cfg)

	// ─── Non-interactive mode ─────────────────────────────
	if len(nonFlagArgs) > 0 {
		query := strings.Join(nonFlagArgs, " ")
		fmt.Printf("%s %s\n\n", color(ansi.cyan, "▶"), query)
		if blocked, kind, hit, level := checkInputGuard(query); blocked {
			if level == "critical" {
				// 二级裁决 (二期): 破坏性/攻击性指令由独立 LLM 复核后才放行
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
		if err := agent.RunStream(query); err != nil {
			if err == context.Canceled {
				fmt.Fprintf(os.Stderr, "\n%s Interrupted\n", color(ansi.yellow, "⚡"))
			} else {
				fmt.Fprintf(os.Stderr, "\n%s %v\n", color(ansi.red, "✗"), err)
				os.Exit(1)
			}
		}
		agentBusy.Store(false)
		fmt.Printf("\n%s\n", dim(agent.stats.String()))
		// 非交互模式结束: 顺带报告缓存命中率 (若本次有请求)
		cs := cacheStatsSummary()
		if strings.Contains(cs, "请求数") {
			fmt.Printf("%s\n", dim(strings.SplitN(cs, "\n", 2)[0]))
		}
		return
	}

	// ─── Interactive loop ─────────────────────────────────
	// ─── 会话检查点 (DMAIC I2): 检测上次会话, 提示可恢复 ───
	var lastCkpH []ChatMessage
	if h, savedAt, err := loadCheckpoint(cfg.WorkDir); err == nil && len(h) > 0 {
		lastCkpH = h
		fmt.Printf("%s 检测到上次会话检查点（保存于 %s, 共 %d 轮对话）\n",
			color(ansi.cyan, "♻"), savedAt, len(h)/2)
		fmt.Println(dim("输入 恢复  可续接上次会话上下文；直接输入新指令则开始新会话。"))
		fmt.Println()
	}

	// Line history for up/down navigation
	lineHistory := readHistory(historyFile, 50)

	multiLineBuf := strings.Builder{}
	inMultiLine := false

	for {
		// Build prompt
		prompt := color(ansi.green, "铸剑炉")
		if showReasoning {
			prompt += color(ansi.yellow, "·R")
		}
		if inMultiLine {
			prompt = color(ansi.dim, " ... ")
		} else {
			prompt += " » "
		}

		line, err := readLine(prompt, lineHistory)
		// 多行粘贴：readLine 已把粘贴的完整多行作为一条输入返回，
		// 直接提交，跳过 \ 续行与括号自动续行，避免粘贴内容被误判为未完成。
		pastedMultiLine := strings.Contains(line, "\n")
		if err != nil {
			if errors.Is(err, errInterrupt) || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
				fmt.Println()
				break
			}
			fmt.Fprintf(os.Stderr, "%s 输入错误: %v\n", color(ansi.red, "✗"), err)
			continue
		}

		// Multi-line continuation (trailing backslash). Windows paths like
		// C:\\path\\to\\forge\\ end with a backslash but must NOT be treated as a
		// continuation, otherwise pasting a path enters multiline mode.
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

		// Auto multi-line: unclosed brackets/parens/quotes continue input
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

		// ─── 折叠展开记忆系统 v2.1：π-φ 折展节律（确定性，不依赖LLM）───
		// 规则: "展开<任务名>"→半显化摘要(回写π); "深入<任务名>"→读全文(回写π); 未命中→交给LLM
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

		// ─── 会话恢复命令: "恢复" 续接上次检查点 ───
		if (input == "恢复" || strings.EqualFold(input, "resume")) && lastCkpH != nil {
			agent.RestoreHistory(lastCkpH)
			fmt.Printf("%s 已恢复上次会话（%d 轮对话上下文）\n", color(ansi.green, "♻"), len(lastCkpH)/2)
			lastCkpH = nil
			continue
		}

		// ─── Commands ──────────────────────────────────
		lowCmd := strings.ToLower(input)
		if strings.HasPrefix(input, "/") || lowCmd == "升级" || lowCmd == "upgrade" {
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

		// ─── 折叠记忆相关性触发 (M3): 对话命中折叠项关键词→静默提示一次(24h去重) ───
		if rel := FindRelevantFold(cfg.WorkDir, input); rel != nil {
			fmt.Printf("%s 相关折叠记忆【%s】— %s\n",
				color(ansi.cyan, "📎"), rel.Name, truncateCN(rel.Summary, 40))
			fmt.Printf("   输入「展开%s」查看摘要, 「深入%s」查看全文\n", rel.Name, rel.Name)
			MarkHinted(cfg.WorkDir, rel.ID)
			fmt.Println()
		}

		// ─── 输入护栏 (DMAIC I3): 越权/注入指令在交给 LLM 前阻断 ───
		if blocked, kind, hit, level := checkInputGuard(input); blocked {
			if level == "critical" {
				// 二级裁决 (二期): 破坏性/攻击性指令由独立 LLM 复核后才放行
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
				// 通过复核 → 继续正常执行
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
		sigCount.Store(0) // 任务结束重置：下次任务首个 Ctrl+C 仍是取消而非退出
		if runErr != nil {
			if errors.Is(runErr, context.Canceled) {
				fmt.Fprintf(os.Stderr, "\n%s 任务已取消\n", color(ansi.yellow, "⚡"))
			} else {
				fmt.Fprintf(os.Stderr, "\n%s %v\n", color(ansi.red, "✗"), runErr)
			}
		}
		fmt.Println()
		fmt.Println(dim("─ " + agent.stats.String()))
		fmt.Println()
	}

	// ─── Session summary ────────────────────────────────
	fmt.Println(dim("──── 会话结束 ────"))
	fmt.Println("  " + agent.stats.String())
	fmt.Println(dim("────────────────────"))
}

// ─── Welcome screen ─────────────────────────────────────────

// displayWidth 计算字符串的终端显示宽度（跳过 ANSI 码，CJK 字符计2列）
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func displayWidth(s string) int {
	// 去掉 ANSI 转义序列
	clean := ansiEscapeRe.ReplaceAllString(s, "")
	w := 0
	for _, r := range clean {
		w += runeWidth(r) // 统一宽度判定（width.go），与行编辑器一致
	}
	return w
}

// padRight 将字符串填充到目标显示宽度（右侧补空格）
func padRight(s string, targetWidth int) string {
	current := displayWidth(s)
	if current >= targetWidth {
		return s
	}
	return s + strings.Repeat(" ", targetWidth-current)
}

// fitWidth 将字符串截断到 maxW 显示宽度（超宽时以 … 结尾），保留 ANSI 转义序列。
// 用于欢迎画面等定宽排版：任何超长内容（模型名/接口等）都不会破坏边框。
func fitWidth(s string, maxW int) string {
	if displayWidth(s) <= maxW {
		return s
	}
	var sb strings.Builder
	w := 0
	needEllipsis := false
	for len(s) > 0 {
		if s[0] == 0x1b { // ANSI 转义序列原样保留（不占显示宽度）
			idx := strings.IndexByte(s, 'm')
			if idx < 0 {
				break
			}
			sb.WriteString(s[:idx+1])
			s = s[idx+1:]
			continue
		}
		r, size := utf8.DecodeRuneInString(s)
		rw := runeWidth(r)
		if w+rw > maxW-1 {
			needEllipsis = true
			break
		}
		sb.WriteRune(r)
		w += rw
		s = s[size:]
	}
	if needEllipsis {
		sb.WriteString("…")
	}
	return sb.String()
}

func printWelcome(cfg *Config) {
	// 自适应终端宽度（54~80 列），终端宽度不可用时回退 54
	w := getTermWidth()
	if w < 54 {
		w = 54
	}
	if w > 80 {
		w = 80
	}
	width := w            // 终端显示列宽（含前导2空格）
	contentW := width - 4 // 顶框内容宽度：去掉 "  ╔" 和 "╗"
	textMax := width - 7  // 内容行文本最大列宽："  ║"(3)+"  "(2)+文本+" ║"(2)=width
	fmt.Println()
	// 顶框
	fmt.Println(padRight(color(ansi.cyan, "  ╔")+color(ansi.cyan, strings.Repeat("═", contentW))+color(ansi.cyan, "╗"), width))
	// 标题行
	title := fitWidth(color(ansi.bold, "铸剑炉 v"+AppVersion+" — 通用数字智能体"), textMax)
	fmt.Println(padRight(color(ansi.cyan, "  ║")+"  "+title, width-2) + color(ansi.cyan, " ║"))
	// 分隔线
	fmt.Println(padRight(color(ansi.cyan, "  ╠")+color(ansi.cyan, strings.Repeat("═", contentW))+color(ansi.cyan, "╣"), width))
	// 模型行
	modelText := fitWidth(color(ansi.dim, "模型:")+" "+cfg.Model, textMax)
	fmt.Println(padRight(color(ansi.cyan, "  ║")+"  "+modelText, width-2) + color(ansi.cyan, " ║"))
	// 路由行（精简文案+自动截断，防超宽破坏边框）
	routeText := fitWidth(color(ansi.dim, "路由:")+" "+cfg.ModelFlash+" ⚡ ↔ "+cfg.ModelPro+" ("+routerModeShort(cfg.RouterMode)+")", textMax)
	fmt.Println(padRight(color(ansi.cyan, "  ║")+"  "+routeText, width-2) + color(ansi.cyan, " ║"))
	// 接口行
	apiText := fitWidth(color(ansi.dim, "接口:")+" "+cfg.BaseURL, textMax)
	fmt.Println(padRight(color(ansi.cyan, "  ║")+"  "+apiText, width-2) + color(ansi.cyan, " ║"))
	// 推理链行
	if cfg.ShowReasoning {
		reasonText := fitWidth(color(ansi.yellow, "推理链: 开  (🧠 可见 · "+effortLabel(cfg)+")"), textMax)
		fmt.Println(padRight(color(ansi.cyan, "  ║")+"  "+reasonText, width-2) + color(ansi.cyan, " ║"))
	} else {
		reasonText := fitWidth(color(ansi.dim, "推理链: 关  (--reasoning 开启)"), textMax)
		fmt.Println(padRight(color(ansi.cyan, "  ║")+"  "+reasonText, width-2) + color(ansi.cyan, " ║"))
	}
	// 底框
	fmt.Println(padRight(color(ansi.cyan, "  ╚")+color(ansi.cyan, strings.Repeat("═", contentW))+color(ansi.cyan, "╝"), width))
	fmt.Println()
	fmt.Println(color(ansi.dim, "  命令:") + " /help  /stats  /history  /clear  /reasoning  /model  /health  /tools  /last  /upgrade")
	fmt.Println(color(ansi.dim, "  输入 'exit' 或 Ctrl+C 退出。 用 \\ 续行多行输入。"))
	fmt.Println()
}

// ─── Commands ───────────────────────────────────────────────

func handleCommand(input string, agent *AgentRunner, cfg *Config, historyFile string, showReasoning *bool) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/help", "/h", "/?":
		fmt.Println()
		fmt.Println(bold("铸剑炉 命令:"))
		fmt.Println("  " + color(ansi.cyan, "/help") + "      — 显示本帮助")
		fmt.Println("  " + color(ansi.cyan, "/stats") + "     — 显示会话统计")
		fmt.Println("  " + color(ansi.cyan, "/history") + "   — 显示输入历史（最近20条）")
		fmt.Println("  " + color(ansi.cyan, "/clear") + "     — 清屏")
		fmt.Println("  " + color(ansi.cyan, "/reasoning") + " — 切换推理链显示")
		fmt.Println("  " + color(ansi.cyan, "/model") + "     — 显示当前模型信息")
		fmt.Println("  " + color(ansi.cyan, "/health") + "    — 健康检查")
		fmt.Println("  " + color(ansi.cyan, "/tools") + "     — 列出可用语言编译器 (gates)")
		fmt.Println("  " + color(ansi.cyan, "/last") + "      — 显示最近一次工具完整输出")
		fmt.Println("  " + color(ansi.cyan, "/cache") + "     — DeepSeek 前缀缓存命中统计")
		fmt.Println("  " + color(ansi.cyan, "/folded") + "    — 折叠展开记忆系统总账")
		fmt.Println("  " + color(ansi.cyan, "/unfold") + "    — 展开折叠任务: /unfold <任务名>")
		fmt.Println("  " + color(ansi.cyan, "/memdiag") + "   — 记忆节律π-φ健康卦象")
		fmt.Println("  " + color(ansi.cyan, "/upgrade") + "   — 自检后自动切换到新版本窗口")
		fmt.Println()
		fmt.Println(dim("  快捷键: ↑↓ 历史 · Tab 命令补全 · Ctrl+C 取消任务(再按退出)"))
		fmt.Println()

	case "/stats":
		fmt.Println()
		fmt.Println(bold("会话统计:"))
		fmt.Println("  " + agent.stats.String())
		fs := agent.forge.Stats()
		fmt.Printf("  forge: builds=%v cache_hits=%v errors=%v cache_size=%v\n",
			fs["builds"], fs["cache_hits"], fs["errors"], fs["cache_size"])
		fmt.Println()

	case "/history":
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

	case "/clear":
		fmt.Print("\033[2J\033[H")
		printWelcome(cfg)

	case "/reasoning":
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

	case "/model":
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

	case "/router":
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

	case "/upgrade":
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

	case "/tools", "/gates":
		fmt.Println()
		fmt.Println(bold("可用语言编译器 (17 gates):"))
		gates := []string{
			"python", "go", "sh/bash", "node/js", "deno", "rust",
			"tcc/c", "math", "logic", "system", "knowledge",
			"regex", "chain", "eprover", "repair", "self", "tcm",
		}
		for i, g := range gates {
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

	case "/folded", "/folds":
		fmt.Println()
		fmt.Println(ListFoldedTasks(cfg.WorkDir))
		fmt.Println()

	case "/memdiag":
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

	case "/unfold":
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

	case "/last":
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

	case "/cache":
		fmt.Println()
		fmt.Println(bold("DeepSeek 前缀缓存命中:"))
		fmt.Println(cacheStatsSummary())
		fmt.Println(dim("  统计文件: cache_stats.jsonl (工作目录) · 数据由 stream_options.include_usage 采集"))
		fmt.Println()

	case "/diagnose":
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
		yang := clampF(2+float64(builds%10), 0, 10)        // 构建活跃
		yin := clampF(4+float64(size%8), 0, 10)           // 缓存积累=承载稳定
		biao := clampF(3+float64(len(agent.history)%8), 0, 10) // 外部交互面
		li := clampF(5+float64(len(agent.history)%6), 0, 10)   // 内部上下文
		cold := clampF(10-float64(errs), 0, 10)           // 错误低=寒少
		heat := clampF(2+float64(errs%5), 0, 10)          // 错误高=热亢
		xu := clampF(10-float64(hits%10), 0, 10)          // 缓存命中低=虚
		shi := clampF(2+float64(size%6), 0, 10)           // 缓存占用=实
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

	case "/health":
		fmt.Println()
		pong := agent.forge.Ping()
		fmt.Printf("  %s %s\n", bold("Forge:"), color(ansi.green, pong))
		if agent.llm.IsHealthy() {
			fmt.Printf("  %s %s\n", bold("LLM:"), color(ansi.green, "healthy"))
		} else {
			fmt.Printf("  %s %s\n", bold("LLM:"), color(ansi.red, "degraded"))
		}
		fmt.Println()

	default:
		fmt.Printf("  %s 未知命令: %s (输入 /help 查看帮助)\n", color(ansi.red, "✗"), cmd)
	}
}
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}


// ─── Peak-hour reminder ─────────────────────────────────────
// checkPeakHour 打印 DeepSeek 高峰时段提醒 (北京时间 9-12 / 14-18, 价格×2)。
// 省钱第一杠杆: 高峰价格翻倍, 影响比缓存命中率更大。
func checkPeakHour() {
	h := time.Now().Hour()
	if (h >= 9 && h < 12) || (h >= 14 && h < 18) {
		fmt.Fprintf(os.Stderr, "%s DeepSeek 高峰时段 (价格×2): 当前 %02d:00 北京时间\n",
			color(ansi.yellow, "⚠️"), h)
		fmt.Fprintf(os.Stderr, "   省钱建议: 非紧急任务请避开 9:00-12:00 / 14:00-18:00。\n")
	} else {
		fmt.Fprintf(os.Stderr, "%s 当前非高峰时段 (价格正常)。\n", color(ansi.green, "✓"))
	}
}

// ─── History file ───────────────────────────────────────────

func appendHistory(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(fmt.Sprintf("%d|%s\n", time.Now().Unix(), escapeHistory(line)))
	// Close immediately: on Windows a held handle blocks os.Rename with
	// "used by another process", so the file must be fully closed before
	// any rotation attempt. (defer f.Close() would keep it open here.)
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: close history %s: %v\n", path, err)
	}

	// Rotate if too large (>100KB). Stat via the path, not the handle.
	if info, err := os.Stat(path); err == nil && info.Size() > 100*1024 {
		if err := os.Rename(path, path+".old"); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: failed to rotate log file %s: %v\n", path, err)
		}
	}
}

func readHistory(path string, maxLines int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "|"); idx > 0 {
			line = line[idx+1:]
		}
		lines = append(lines, unescapeHistory(line))
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

// ─── Input helpers ─────────────────────────────────────────

// escapeHistory escapes newlines so a multi-line input is stored as a
// single history entry (the file is line-oriented).
func escapeHistory(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}

// unescapeHistory reverses escapeHistory.
func unescapeHistory(s string) string {
	return strings.ReplaceAll(s, "\\n", "\n")
}

// isPathLike reports whether s looks like a Windows path (drive letter
// or UNC root). Used to avoid treating trailing backslashes in paths as
// multiline continuations.
func isPathLike(s string) bool {
	if filepath.IsAbs(s) {
		return true
	}
	if len(s) >= 2 && s[1] == ':' &&
		((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return true
	}
	return false
}

// unbalancedDelimiters reports whether a line has unclosed (), [], {}, or
// quotes. Best-effort: brackets inside strings/comments are tracked so a
// multi-line code snippet continues naturally.
func unbalancedDelimiters(s string) bool {
	stack := []rune{}
	inStr := rune(0) // 0 = not in string; otherwise the quote rune
	escaped := false
	for _, r := range s {
		if inStr != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == inStr {
				inStr = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			inStr = r
		case '(', '[', '{':
			stack = append(stack, r)
		case ')':
			if len(stack) > 0 && stack[len(stack)-1] == '(' {
				stack = stack[:len(stack)-1]
			} else {
				return false // mismatched closer — treat as complete
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			} else {
				return false
			}
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			} else {
				return false
			}
		}
	}
	return inStr != 0 || len(stack) > 0
}

// ─── Help ───────────────────────────────────────────────────

func printHelp() {
	fmt.Println("铸剑炉 v" + AppVersion + " — 通用数字智能体 · 编译器沙箱")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  forge.exe              交互模式（默认）")
	fmt.Println("  forge.exe --reasoning  显示LLM推理/思考过程")
	fmt.Println("  forge.exe -r           同 --reasoning")
	fmt.Println("  forge.exe --help       显示本帮助")
	fmt.Println("  forge.exe --version    显示版本")
	fmt.Println("  forge.exe \"查询\"      运行单次查询（非交互）")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  forge.exe \"列出当前目录的文件\"")
	fmt.Println("  forge.exe -r \"解释架构\"")
	fmt.Println()
	fmt.Println("环境变量:")
	fmt.Println("  DEEPSEEK_API_KEY     DeepSeek API密钥")
	fmt.Println("  DEEPSEEK_MODEL       固定模型 (设置后路由为 fixed 模式)")
	fmt.Println("  DEEPSEEK_MODEL_FLASH  轻量模型 (默认 deepseek-v4-flash)")
	fmt.Println("  DEEPSEEK_MODEL_PRO    重量模型 (默认 deepseek-v4-pro)")
	fmt.Println("  DEEPSEEK_ROUTER       路由模式 auto|flash|pro|fixed (默认 auto)")
	fmt.Println("  FORGE_MAX_CODE_SIZE  最大代码长度 (默认 512KB)")
	fmt.Println("  FORGE_TOOL_TIMEOUT   工具超时 (默认 60s)")
	fmt.Println("  回合数限制已取消: 主循环无轮数上限, 由连续失败/重复调用/上下文取消兜底")
}

// enableWindowsUTF8 将 Windows 控制台代码页设为 UTF-8 (CP_UTF8 = 65001)，
// 保证中文输出不乱码。
func enableWindowsUTF8() {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	// 输出代码页与输入代码页都设为 UTF-8 (CP_UTF8 = 65001)，否则交互
	// 模式下粘贴/键入的中文可能被控制台按 GBK 解释成乱码。
	k32.NewProc("SetConsoleOutputCP").Call(65001)
	k32.NewProc("SetConsoleCP").Call(65001)

	// 启用虚拟终端处理，让 \033[K 等 ANSI 光标控制在旧版控制台配置下也生效。
	h := syscall.Handle(os.Stdout.Fd())
	var mode uint32
	const enableVirtualTerminalProcessing = 0x0004
	if r1, _, _ := k32.NewProc("GetConsoleMode").Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r1 != 0 {
		k32.NewProc("SetConsoleMode").Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	}
}
