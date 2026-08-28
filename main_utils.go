package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

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
	// 升级: 调用炫酷横幅, 取代旧的单色 ASCII 框。
	printTuiBanner(cfg)
}

// buildRouteText 构建路由行文本。单模型(Flash==Pro)时不重复模型名，双模型时完整展示，避免窄终端截断。
func buildRouteText(cfg *Config, textMax int) string {
	if cfg.ModelFlash == cfg.ModelPro {
		return fitWidth(color(ansi.dim, "路由:")+" 未分档（"+routerModeShort(cfg.RouterMode)+"）", textMax)
	}
	return fitWidth(color(ansi.dim, "路由:")+" "+cfg.ModelFlash+" ⚡ ↔ "+cfg.ModelPro+" ("+routerModeShort(cfg.RouterMode)+")", textMax)
}

// ─── Commands ───────────────────────────────────────────────

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
	fmt.Println("  DEEPSEEK_MODEL_VISION 视觉模型 (识图, 默认 deepseek-v4-flash-vision-exp)")
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
