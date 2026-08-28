package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ─── ANSI helpers ───────────────────────────────────────────

var ansi = struct {
	reset, bold, dim, red, green, yellow, blue, magenta, cyan, white string
}{
	reset: "\033[0m", bold: "\033[1m", dim: "\033[2m",
	red: "\033[31m", green: "\033[32m", yellow: "\033[33m",
	blue: "\033[34m", magenta: "\033[35m", cyan: "\033[36m", white: "\033[37m",
}

func color(c, s string) string { return c + s + ansi.reset }

func bold(s string) string { return ansi.bold + s + ansi.reset }

func dim(s string) string { return ansi.dim + s + ansi.reset }

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type spinner struct {
	stopCh chan struct{}
	doneCh chan struct{}
	mu     sync.Mutex
	msg    string
}

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

func (s *spinner) setMsg(msg string) {
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

func (s *spinner) stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	<-s.doneCh
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

func displayToolCode(code, lang string) {
	emoji := langEmoji(lang)
	langLabel := strings.ToUpper(lang)
	if lang == "" {
		langLabel = "CODE"
	}

	// Top border
	fmt.Fprintf(os.Stderr, "  %s %s\n", emoji, color(ansi.cyan, "─── "+langLabel+" "+strings.Repeat("─", max(0, 40-len(langLabel)))))

	codeLines := strings.Split(code, "\n")
	maxShow := 30
	showLines := codeLines
	truncated := false
	if len(codeLines) > maxShow {
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
