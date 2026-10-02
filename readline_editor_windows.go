//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

// ---- 折行感知行编辑器 ----

// lineEditor 维护编辑缓冲与终端块位置，支持超长输入折行后的正确重绘。
type lineEditor struct {
	buf          []rune
	cursor       int
	prompt       string
	promptW      int
	termW        int
	lastLines    int // 上次重绘占用的终端行数
	lastTop      int // 上次块顶的缓冲区行号
	cursorRow    int // 光标在块内的行号（0-based）
	bottomBarRow int // 状态栏所在行(bottom); redraw 禁止触碰该行, -1=未约束(非TTY/自定义)

	// out 是编辑器的输出目标 (默认 os.Stdout)。抽成字段是为了让测试可注入
	// io.Discard —— 否则回显会直接写进程 stdout, 把 go test -v 的
	// "--- PASS/FAIL: TestX" 行首污染成 "abcd--- PASS: ...", 任何按行首
	// 统计测试结果的工具都会漏计 (含漏报 FAIL), 属静默失败。
	out io.Writer
}

func newLineEditor(prompt string) *lineEditor {
	tw := getTermWidth()
	if tw < 20 {
		tw = 80
	}
	return &lineEditor{
		out:          os.Stdout,
		bottomBarRow: -1,
		prompt:       prompt,
		promptW:      displayWidth(prompt),
		termW:        tw,
		lastLines:    1,
	}
}

// runeWidth 已移至 width.go（跨平台共享，覆盖假名/韩文/扩展区）。
// wrapText 按显示宽度将文本折成多行（每行 <= width 列）。
// '\n' 视为强制换行（多行粘贴留在编辑区后 redraw 不会把换行拼成一行）。
func wrapText(s string, width int) []string {
	if width <= 0 {
		width = 80
	}
	var lines []string
	var cur strings.Builder
	w := 0
	i := 0
	for i < len(s) {
		// ANSI 转义序列不计显示宽度：跳过其字节，但保留在输出里。
		if s[i] == 0x1b {
			// CSI: ESC [ params ... final-byte (m 等)。final ∈ 0x40..0x7e。
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++ // past '['
				for j < len(s) {
					c := s[j]
					j++
					if c >= 0x40 && c <= 0x7e { // final byte
						break
					}
				}
				cur.WriteString(s[i:j])
				i = j
				continue
			}
			// 非 CSI 的 ESC 序列：至少消费 ESC + 下一个字节，不破坏折行。
			if j < len(s) {
				cur.WriteString(s[i : j+1])
				i = j + 1
				continue
			}
			cur.WriteByte(s[i])
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// 无效字节，按单宽处理避免死循环
			cur.WriteByte(s[i])
			w++
			i++
			continue
		}
		if r == '\n' {
			lines = append(lines, cur.String())
			cur.Reset()
			w = 0
			i += size
			continue
		}
		rw := runeWidth(r)
		if w > 0 && w+rw > width {
			lines = append(lines, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(r)
		w += rw
		i += size
	}
	lines = append(lines, cur.String())
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// cursorBlockPos 计算 buf[:cursor] 在 prompt+termW 下的块内 (行, 列)。
// 与 wrapText 共用同一折行模型（宽字符不跨行、行宽<=termW）。
// 修复: 原来 redraw 用 curW % termW 取模定位——当宽字符恰好到行末被 wrapText
// 折到下一行时, 上一行不满宽, 取模会把光标算进上一行末尾, 错位1列,
// 表现为"输入到行末后文字/光标错乱, 看不到自己写了什么"。
func cursorBlockPos(prompt string, termW int, buf []rune, cursor int) (row, col int) {
	prefix := prompt + string(buf[:cursor])
	lines := wrapText(prefix, termW)
	row = len(lines) - 1
	col = displayWidth(lines[row])
	if col >= termW {
		// 最后一行恰好满宽: 终端已自动回绕, 光标在下一行行首
		row++
		col = 0
	}
	return row, col
}

// insertRune inserts r at cursor with echo. When inserting at the end of the
// line the char is printed directly, avoiding a full-line redraw.
func insertRune(ed *lineEditor, r rune) {
	if len(ed.buf) >= maxInputRunes {
		return // protect against unbounded paste memory
	}
	atEnd := ed.cursor == len(ed.buf)
	ed.buf = append(ed.buf[:ed.cursor], append([]rune{r}, ed.buf[ed.cursor:]...)...)
	ed.cursor++
	if atEnd {
		fmt.Fprint(ed.w(), string(r))
		// 快路径也必须同步块内光标行号：超宽时终端会自动回绕换行，
		// 若 cursorRow 不更新，后续 redraw 的块顶推算 (光标Y-cursorRow) 会错位花屏。
		if pos, ok := getCursorPos(); ok && int(pos.Y) >= ed.lastTop {
			ed.cursorRow = int(pos.Y) - ed.lastTop
			// 同步块高：超宽回绕后块占多行，lastLines 不更新会导致
			// 后续 redraw 清旧块时漏清多余行（残留花屏）。
			if h := ed.cursorRow + 1; h > ed.lastLines {
				ed.lastLines = h
			}
		}
	} else {
		ed.redraw()
	}
}

// insertSilent inserts r at cursor without echo (defensive fallback;
// normal path always echoes so input is never invisible).
func insertSilent(ed *lineEditor, r rune) {
	if len(ed.buf) >= maxInputRunes {
		return
	}
	ed.buf = append(ed.buf[:ed.cursor], append([]rune{r}, ed.buf[ed.cursor:]...)...)
	ed.cursor++
}

// prevWord moves the cursor to the start of the previous
// whitespace-delimited word (Ctrl+Left).
func prevWord(buf []rune, cursor int) int {
	for cursor > 0 && (buf[cursor-1] == ' ' || buf[cursor-1] == '\t') {
		cursor--
	}
	for cursor > 0 && buf[cursor-1] != ' ' && buf[cursor-1] != '\t' {
		cursor--
	}
	return cursor
}

// nextWord moves the cursor to the start of the next
// whitespace-delimited word (Ctrl+Right).
func nextWord(buf []rune, cursor int) int {
	n := len(buf)
	for cursor < n && buf[cursor] != ' ' && buf[cursor] != '\t' {
		cursor++
	}
	for cursor < n && (buf[cursor] == ' ' || buf[cursor] == '\t') {
		cursor++
	}
	return cursor
}

func tabComplete(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	low := strings.ToLower(trimmed)
	// 参数模式前：若当前含空格且命令能带参数, 补参数候选
	// 参数模式判断基于「左 trim 保留尾随空格」的原文 —— TrimSpace 会吃掉 "/goal " 的尾随空格,
	// 使"打完命令敲空格再 Tab"永远进不了参数模式, 而这恰是最常见的补全时机。
	raw := strings.ToLower(strings.TrimLeft(line, " \t"))
	if sp := strings.Index(raw, " "); sp > 0 {
		cmdPart := raw[:sp]
		argPrefix := strings.TrimSpace(raw[sp+1:])
		for _, t := range cmdTips {
			if t.name != cmdPart {
				continue
			}
			for _, a := range t.args {
				if strings.HasPrefix(strings.ToLower(a), argPrefix) && a != argPrefix {
					return t.name + " " + a + " "
				}
			}
		}
		return ""
	}
	// 命令前缀补全: 遍历 cmdTips 全量, 返回首个前缀匹配
	for _, t := range cmdTips {
		ln := strings.ToLower(t.name)
		if strings.HasPrefix(ln, low) && t.name != trimmed {
			return t.name + " "
		}
	}
	return ""
}

// w 返回输出目标; out 未注入时回退 os.Stdout (防御: 任何构造点都安全)。
func (ed *lineEditor) w() io.Writer {
	if ed.out == nil {
		return os.Stdout
	}
	return ed.out
}

// redraw 折行感知重绘：清掉旧块 -> 重绘新块 -> 绝对定位光标。
// 相对光标移动在折行后会错乱，这里全部用 Windows 控制台 API 绝对定位。
func (ed *lineEditor) redraw() {
	// 每次重绘前刷新终端宽度：响应窗口拖拽/缩放（resize）
	ed.termW = getTermWidth()
	if ed.termW < 20 {
		ed.termW = 80
	}
	text := string(ed.buf)
	lines := wrapText(ed.prompt+text, ed.termW)
	n := len(lines)
	// 最后一行恰好满宽时终端会再折出一行空行
	actualN := n
	if n > 0 && displayWidth(lines[n-1]) >= ed.termW {
		actualN = n + 1
	}

	oldTop, oldLines := ed.lastTop, ed.lastLines
	maxRow := ed.bottomBarRow - 1 // 状态栏上一行 = 编辑区最大行

	// 推算块顶：
	//  - 有底部状态栏约束：编辑块底部对齐到状态栏上一行(maxRow)，块整体上移，
	//    保证折行/多行粘贴产生的每一行都在可见区(状态栏上方)，避免原逻辑
	//    超出 maxRow 就 break 丢弃导致"两行后看不到输入的字"。
	//    块过高(超出整个窗口)时钳制到屏幕顶(0)，底部超宽部分截断(极端场景)。
	//  - 无状态栏(非TTY/自定义)：块顶跟随当前光标(原逻辑)。
	if ed.bottomBarRow >= 0 {
		ed.lastTop = maxRow - actualN + 1
	} else if pos, ok := getCursorPos(); ok {
		ed.lastTop = int(pos.Y) - ed.cursorRow
	}
	if ed.lastTop < 0 {
		ed.lastTop = 0
	}

	// 清掉旧块占用的行(块顶/块高变化后, 新旧行位集并集全清, 避免残留花屏)
	clearRow := func(r int) {
		if ed.bottomBarRow >= 0 && r > maxRow {
			return
		}
		setCursorPos(0, r)
		fmt.Fprint(ed.w(), "\033[K")
	}
	for i := 0; i < oldLines; i++ {
		clearRow(oldTop + i)
	}
	for i := ed.lastTop; i < oldTop; i++ {
		clearRow(i)
	}
	// 清块底下方空出的行(新块比旧块短时)
	for i := ed.lastTop + actualN; i < oldTop+oldLines; i++ {
		clearRow(i)
	}

	// 重绘新块
	for i := 0; i < actualN; i++ {
		r := ed.lastTop + i
		if ed.bottomBarRow >= 0 && r > maxRow {
			break
		}
		setCursorPos(0, r)
		fmt.Fprint(ed.w(), "\033[K")
		if i < n {
			fmt.Fprint(ed.w(), lines[i])
		}
	}

	// 光标目标位置：与 wrapText 同模型（宽字符行末不取模, 见 cursorBlockPos）
	curRow, curCol := cursorBlockPos(ed.prompt, ed.termW, ed.buf, ed.cursor)

	ed.lastLines = actualN
	ed.cursorRow = curRow
	// 光标不落入状态栏行: 超界时钳制到 status 上一行
	endRow := ed.lastTop + curRow
	if ed.bottomBarRow >= 0 && endRow > ed.bottomBarRow-1 {
		endRow = ed.bottomBarRow - 1
	}
	setCursorPos(curCol, endRow)
}
