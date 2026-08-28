package main

import (
	"fmt"
	"strings"
)

// status_bar.go — REPL 底部常驻状态栏 (档位A: 每轮刷新)。
// 每次进入输入前, 在终端可视窗口底部绘制一行状态栏:
//   缓存命中率(近20条) | 会话统计(turns/tokens/tools/耗时/模型)
// 幂等且失败静默: 非 TTY/无法取终端尺寸时直接返回, 绝不干扰主流程。

// drawStatusBar 绘制一次状态栏, 并把光标移到状态栏上一行供 readLine 起笔。
func drawStatusBar(agent *AgentRunner, workDir string) {
	// 兜底: 确保控制台探针已初始化(修复首轮/非 readLine 触发上下文状态栏缺失)
	ensureConsoleProbe()
	top, bottom, ok := consoleWindowRect()
	if !ok {
		return
	}
	h := bottom - top + 1
	if h < 2 {
		return
	}
	w := getTermWidth()
	if w <= 0 {
		w = 80
	}

	// 缓存命中率: 优先会话累计, 无则回退近20条(见 statusBarCacheText)
	left := statusBarCacheText(agent, 20)
	stats := "—"
	if agent != nil && agent.stats != nil {
		stats = agent.stats.StringZh()
	}
	model := ""
	if agent != nil && agent.stats != nil && agent.stats.LastModel != "" {
		model = agent.stats.LastModel
	}

	// 三色分区: 缓存(绿) | 模型(青) | 会话统计(dim)
	leftSeg := " " + left + "  "
	midSeg := ""
	if model != "" {
		midSeg = color(ansi.cyan, "模型 "+model) + "  "
	}
	rightSeg := " " + color(ansi.dim, stats) + " "

	text := leftSeg + midSeg + rightSeg
	disp := displayWidth(text)
	if disp > w {
		text = truncateDisplay(text, w)
	} else if disp < w {
		text += strings.Repeat(" ", w-disp)
	}

	// 深灰渐变底板 + 彩色文本; 不支持 256 背景时退化为反显。
	bg := "\033[48;5;234m"
	setCursorPos(0, bottom)
	fmt.Print("\033[K")
	fmt.Print(bg + text + ansi.reset)
	setCursorPos(0, bottom-1)
}

// statusBarCacheText 计算状态栏缓存命中率文本段。
// 优先当前会话累计命中率(实时, type=cache_usage 驱动), 无则回退近 n 条历史平均,
// 再无命中/未命中样本时显示 "--"。
func statusBarCacheText(agent *AgentRunner, n int) string {
	rate, nRecs, rok := cacheHitRate(n)
	if agent != nil && agent.stats != nil {
		if sr, sok := agent.stats.cacheRate(); sok {
			return color(ansi.green, fmt.Sprintf("缓存命中 %.1f%% 会话", sr))
		}
		if rok {
			return color(ansi.green, fmt.Sprintf("缓存命中 %.1f%% 近%d条", rate, nRecs))
		}
		return color(ansi.dim, "缓存命中 --")
	}
	if rok {
		return color(ansi.green, fmt.Sprintf("缓存命中 %.1f%% 近%d条", rate, nRecs))
	}
	return color(ansi.dim, "缓存命中 --")
}

// truncateDisplay 按显示宽度截断字符串到不超过 col 列。
func truncateDisplay(s string, col int) string {
	var sb strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w+rw > col {
			break
		}
		sb.WriteRune(r)
		w += rw
	}
	return sb.String()
}
