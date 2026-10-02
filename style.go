// style.go: ANSI 样式常量与终端配色

package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

type spinner struct {
	stopCh chan struct{}
	doneCh chan struct{}
	mu     sync.Mutex
	msg    string
}

// ─── ANSI helpers ───────────────────────────────────────────

var ansi = struct {
	reset, bold, dim, red, green, yellow, blue, magenta, cyan, white string
}{
	reset: "\033[0m", bold: "\033[1m", dim: "\033[2m",
	red: "\033[31m", green: "\033[32m", yellow: "\033[33m",
	blue: "\033[34m", magenta: "\033[35m", cyan: "\033[36m", white: "\033[37m",
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func color(c, s string) string { return c + s + ansi.reset }

func bold(s string) string { return ansi.bold + s + ansi.reset }

func dim(s string) string { return ansi.dim + s + ansi.reset }

// startSpinner renders an animated indicator on stderr until stop() is called.
// 帧彩色化 + 阶段标签可动态更新, 过程节奏可见。
func startSpinner(msg string) *spinner {
	s := &spinner{stopCh: make(chan struct{}), doneCh: make(chan struct{}), msg: msg}
	go func() {
		idx := 0
		for {
			select {
			case <-s.stopCh:
				fmt.Fprint(os.Stderr, "\r\033[K")
				close(s.doneCh)
				return
			case <-time.After(100 * time.Millisecond):
				s.mu.Lock()
				m := s.msg
				s.mu.Unlock()
				fmt.Fprintf(os.Stderr, "\r%s %s", color(ansi.cyan, spinnerFrames[idx%len(spinnerFrames)]), bold(m))
				idx++
			}
		}
	}()
	return s
}

func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// toolCodeHeader 工具代码块的头部边框。
// 流式显示(toolCodeStreamer.render)与整块兜底显示(displayToolCode)共用同一实现,
// 防止两处格式漂移 —— 同一次工具调用的代码块首行必须视觉一致。
func toolCodeHeader(lang string) string {
	langLabel := strings.ToUpper(lang)
	if lang == "" {
		langLabel = "CODE"
	}
	return fmt.Sprintf("  %s %s\n", langEmoji(lang), color(ansi.cyan, "─── "+langLabel+" "+strings.Repeat("─", max(0, 40-len(langLabel)))))
}

// ─── Tool execution display (anti-hallucination) ───────────────

// displayToolCode prints the source code being executed with syntax highlighting.
// This lets the user verify that the tool was actually called, preventing LLM hallucination.
func displayToolCode(code, lang string) {
	fmt.Fprint(os.Stderr, toolCodeHeader(lang))

	codeLines := strings.Split(code, "\n")
	maxShow := codeMaxLines()
	showLines := codeLines
	truncated := false
	// maxShow<=0 表示全量(不截断), 这是六西格玛控制阶段默认
	if maxShow > 0 && len(codeLines) > maxShow {
		showLines = codeLines[:maxShow]
		truncated = true
	}

	for _, cl := range showLines {
		// Apply syntax highlighting
		highlighted := highlightLine(cl, lang)
		fmt.Fprintf(os.Stderr, "  %s %s\n", dim("│"), highlighted)
	}
	if truncated {
		fmt.Fprintf(os.Stderr, "  %s %s\n", dim("│"), dim(fmt.Sprintf("... +%d more lines", len(codeLines)-maxShow)))
	}
}

func (s *spinner) stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	<-s.doneCh
}
