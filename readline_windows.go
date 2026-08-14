//go:build windows

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

var (
	kernel32DLL                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode           = kernel32DLL.NewProc("GetConsoleMode")
	procSetConsoleMode           = kernel32DLL.NewProc("SetConsoleMode")
	procMBToWideChar             = kernel32DLL.NewProc("MultiByteToWideChar")
	procSetConsoleCursorPosition = kernel32DLL.NewProc("SetConsoleCursorPosition")
	procPeekConsoleInput         = kernel32DLL.NewProc("PeekConsoleInput")
	procReadConsoleInput         = kernel32DLL.NewProc("ReadConsoleInput")
	consoleProcsOK               bool // set true by probeConsoleProcs if all resolve
)

// probeConsoleProcs resolves every console LazyProc once.  If any procedure is missing
// from kernel32.dll the internal mustFind panics; we recover, mark consoleProcsOK=false,
// and readLine falls back to plain buffered line mode without touching the console mode.
func probeConsoleProcs() {
	if consoleProcsOK {
		return // already probed successfully
	}
	defer func() {
		if r := recover(); r != nil {
			consoleProcsOK = false
		}
	}()
	// Force resolution of every LazyProc by calling them with dummy/zero arguments.
	// The first call triggers mustFind; if it panics we catch it above.
	// We use a real console handle so the calls themselves are harmless probes.
	h := syscall.Handle(os.Stdin.Fd())
	var mode uint32
	var rec inputRecord
	var numRead uint32
	procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	procPeekConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&numRead)))
	procReadConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&numRead)))
	procSetConsoleCursorPosition.Call(uintptr(h), 0)
	// procMBToWideChar is used only for GBK decoding – try it with an empty buffer.
	var dummy byte
	procMBToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&dummy)), 0, 0, 0)
	consoleProcsOK = true
}

const (
	cpACP             = 0 // system ANSI code page (GBK on zh-CN)
	mbErrInvalidChars = 0x8
)

const (
	enableLineInput = 0x0002
	enableEchoInput = 0x0004
)

// 粘贴突发检测参数
const (
	newlineProbe   = 30 * time.Millisecond  // 换行后跨批探测窗口(粘贴批间<1ms; 手动回车后打字不被吞)
	burstIdle      = 150 * time.Millisecond // 粘贴流空闲判定（兜底, 批尾探测实际用30ms）
	readBatchSize  = 8192                   // 批量读取缓冲（替代逐字节读取）
)

// keyEvent 是 INPUT_RECORD 的事件类型常量
const keyEvent = 0x0001

// errInterrupt signals Ctrl+C during line input (returned only when processed
// input is unavailable; normally Ctrl+C arrives as a signal instead).
var errInterrupt = errors.New("interrupt")

// filterControl strips ASCII control characters (NUL, SOH, ..., US and DEL)
// that can leak into the input buffer from corrupted pastes or binary junk.
// Tab is kept (it is meaningful when typed). Backspace/CR/LF/ESC are already
// consumed by the reader before this point.
func filterControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		if r == 0x7f {
			return -1
		}
		return r
	}, s)
}

func getConsoleMode(h syscall.Handle, mode *uint32) error {
	r1, _, e1 := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(mode)))
	if r1 == 0 {
		return e1
	}
	return nil
}

func setConsoleMode(h syscall.Handle, mode uint32) error {
	r1, _, e1 := procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	if r1 == 0 {
		return e1
	}
	return nil
}

// ---- 折行感知行编辑器 ----

// lineEditor 维护编辑缓冲与终端块位置，支持超长输入折行后的正确重绘。
type lineEditor struct {
	buf       []rune
	cursor    int
	prompt    string
	promptW   int
	termW     int
	lastLines int // 上次重绘占用的终端行数
	lastTop   int // 上次块顶的缓冲区行号
	cursorRow int // 光标在块内的行号（0-based）
}

func newLineEditor(prompt string) *lineEditor {
	tw := getTermWidth()
	if tw < 20 {
		tw = 80
	}
	return &lineEditor{
		prompt:    prompt,
		promptW:   displayWidth(prompt),
		termW:     tw,
		lastLines: 1,
	}
}

// runeWidth 已移至 width.go（跨平台共享，覆盖假名/韩文/扩展区）。
// wrapText 按显示宽度将文本折成多行（每行 <= width 列）。
func wrapText(s string, width int) []string {
	if width <= 0 {
		width = 80
	}
	var lines []string
	var cur strings.Builder
	w := 0
	for _, r := range s {
		rw := runeWidth(r)
		if w > 0 && w+rw > width {
			lines = append(lines, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(r)
		w += rw
	}
	lines = append(lines, cur.String())
	if len(lines) == 0 {
		lines = []string{""}
	}
	return lines
}

// inputRecord 对应 Windows INPUT_RECORD（EventType + 16 字节 union）。
type inputRecord struct {
	EventType uint16
	_         [2]byte
	Event     [16]byte
}

// getCursorPos 返回光标在控制台缓冲区中的位置。
func getCursorPos() (coord, bool) {
	h := syscall.Handle(os.Stdout.Fd())
	var info consoleScreenBufferInfo
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return coord{}, false
	}
	return info.CursorPosition, true
}

// setCursorPos 绝对定位光标到控制台缓冲区 (x, y)。
func setCursorPos(x, y int) {
	h := syscall.Handle(os.Stdout.Fd())
	v := uint32(uint16(x)) | uint32(uint16(y))<<16
	procSetConsoleCursorPosition.Call(uintptr(h), uintptr(v))
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

	// 推算块顶：当前光标屏幕行 - 光标在块内的行号
	if pos, ok := getCursorPos(); ok {
		ed.lastTop = int(pos.Y) - ed.cursorRow
		if ed.lastTop < 0 {
			ed.lastTop = 0
		}
	}

	// 逐行清 + 重绘
	for i := 0; i < actualN; i++ {
		setCursorPos(0, ed.lastTop+i)
		fmt.Print("\033[K")
		if i < n {
			fmt.Print(lines[i])
		}
	}
	// 清掉旧块多余的行
	for i := actualN; i < ed.lastLines; i++ {
		setCursorPos(0, ed.lastTop+i)
		fmt.Print("\033[K")
	}

	// 光标目标位置（块内行/列）
	curW := ed.promptW + displayWidth(string(ed.buf[:ed.cursor]))
	curRow := curW / ed.termW
	curCol := curW % ed.termW

	ed.lastLines = actualN
	ed.cursorRow = curRow
	setCursorPos(curCol, ed.lastTop+curRow)
}

// ---- 无泄漏的输入探测 ----

// stdinHasInput 窥视控制台输入缓冲：仅当首个事件是按键事件时返回 true。
// 非按键事件（窗口调整/焦点变化等）会被消费掉，避免 ReadFile 卡住。
func stdinHasInput() bool {
	if !consoleProcsOK {
		return false
	}
	h := syscall.Handle(os.Stdin.Fd())
	var rec [1]inputRecord
	var numRead uint32
	r1, _, _ := procPeekConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&rec[0])), 1, uintptr(unsafe.Pointer(&numRead)))
	if r1 == 0 || numRead == 0 {
		return false
	}
	if rec[0].EventType == keyEvent {
		return true
	}
	// 非按键事件：消费掉（Peek 不消费，必须 Read 才能让出缓冲）
	procReadConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&rec[0])), 1, uintptr(unsafe.Pointer(&numRead)))
	return false
}

// waitInput 等待最多 d 时间；一旦检测到按键输入立即返回 true。
// 相比 goroutine+channel 的读超时，这里不消费按键、不泄漏 goroutine。
func waitInput(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if stdinHasInput() {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return stdinHasInput()
		}
		sleep := 5 * time.Millisecond
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
}

// ---- readLine ----

// readLine reads a line with up/down history navigation and Tab completion.
// Falls back to plain line mode when console raw mode is unavailable or
// FORGE_NO_READLINE=1 is set.
//
// 多行粘贴支持：检测到突发字节流（一次 Read >=2 字节）即进入粘贴模式，
// 粘贴内容中的换行不触发提交；流空闲（burstIdle 无新字节）或换行后无
// 后续字节（newlineProbe）才提交。多行粘贴作为一条完整输入返回。
// inputSession 封装一次行输入的编辑会话，含粘贴突发收集状态机。
// 终端 IO 依赖通过字段注入，便于单元测试（真实 readLine 注入控制台实现）。
type inputSession struct {
	ed         *lineEditor
	history    []string
	histIdx    int
	pending    []byte
	burst      bool
	sawNewline bool

	// 注入的终端依赖
	probe       func(time.Duration) bool // 探测输入缓冲是否有后续字节（waitInput）
	readNext    func() (byte, error)     // 读取一个后续字节
	echoNewline func()                   // 提交时打印换行
	redrawFn    func()                   // 整块重绘
}

func newInputSession(prompt string, history []string) *inputSession {
	return &inputSession{
		ed:      newLineEditor(prompt),
		history: history,
		histIdx: len(history), // len(history) == "new input" position
	}
}

// handleByte 处理一个非换行字节。返回 committed=true 表示输入已提交。
// applySeq 执行一个 ESC 序列(不含 ESC 前缀, 如 "[A"/"[1;5C"/"[200~")对应的动作。
// 批内完整序列与流式序列共用同一动作表; 未知序列静默忽略(色码/功能键)。
func (s *inputSession) applySeq(seq string) {
	switch seq {
	case "[A", "[5~": // Up / PgUp: history back
		if s.histIdx > 0 {
			s.histIdx--
			s.ed.buf = []rune(s.history[s.histIdx])
			s.ed.cursor = len(s.ed.buf)
			s.redrawFn()
		}
	case "[B", "[6~": // Down / PgDn: history forward
		if s.histIdx < len(s.history) {
			s.histIdx++
			if s.histIdx == len(s.history) {
				s.ed.buf = nil
			} else {
				s.ed.buf = []rune(s.history[s.histIdx])
			}
			s.ed.cursor = len(s.ed.buf)
			s.redrawFn()
		}
	case "[C": // Right
		if s.ed.cursor < len(s.ed.buf) {
			s.ed.cursor++
			s.redrawFn()
		}
	case "[D": // Left
		if s.ed.cursor > 0 {
			s.ed.cursor--
			s.redrawFn()
		}
	case "[H", "[1~": // Home
		if s.ed.cursor != 0 {
			s.ed.cursor = 0
			s.redrawFn()
		}
	case "[F", "[4~": // End
		if s.ed.cursor != len(s.ed.buf) {
			s.ed.cursor = len(s.ed.buf)
			s.redrawFn()
		}
	case "[3~": // Delete
		if s.ed.cursor < len(s.ed.buf) {
			s.ed.buf = append(s.ed.buf[:s.ed.cursor], s.ed.buf[s.ed.cursor+1:]...)
			s.redrawFn()
		}
	case "[1;5C": // Ctrl+Right
		s.ed.cursor = nextWord(s.ed.buf, s.ed.cursor)
		s.redrawFn()
	case "[1;5D": // Ctrl+Left
		s.ed.cursor = prevWord(s.ed.buf, s.ed.cursor)
		s.redrawFn()
	}
	// "[2~" (Insert) and F1-F12 sequences are intentionally ignored.
}

func (s *inputSession) handleByte(ch byte) (bool, error) {
	switch {
	case ch == 0x03: // Ctrl+C
		s.echoNewline()
		flushPending(s.ed, &s.pending)
		return true, errInterrupt

	case ch == 0x1b: // ESC sequence (arrows, Delete, Home/End, Fn keys...)
		if s.burst {
			// 粘贴流中的 ESC：丢弃（防 ANSI 色码花屏），不阻塞收集
			return false, nil
		}
		seq := readEscapeSeq(s.readNext)
		s.applySeq(seq)
		// "[2~" (Insert) and F1-F12 sequences are intentionally ignored.

	case ch == 0x08 || ch == 0x7f: // Backspace
		if s.burst {
			consumePending(s.ed, &s.pending, false) // 先刷 pending，保持删除顺序正确
		}
		if s.ed.cursor > 0 {
			s.ed.buf = append(s.ed.buf[:s.ed.cursor-1], s.ed.buf[s.ed.cursor:]...)
			s.ed.cursor--
			if !s.burst {
				s.redrawFn()
			}
		}

	case ch == 0x09: // Tab
		completed := tabComplete(string(s.ed.buf))
		if completed != "" {
			s.ed.buf = []rune(completed)
			s.ed.cursor = len(s.ed.buf)
			if !s.burst {
				s.redrawFn()
			}
		} else if s.burst {
			consumePending(s.ed, &s.pending, false) // 先刷 pending 再插入，保持顺序
			insertRune(s.ed, '\t')
		} else {
			insertRune(s.ed, '\t')
		}

	default:
		// UTF-8 multi-byte handling with a GBK fallback.
		s.pending = append(s.pending, ch)
		if !s.burst {
			consumePending(s.ed, &s.pending, false)
		}
		// burst 模式：字节先累积，流结束时统一解码+重绘
	}
	return false, nil
}

// commit 提交当前缓冲：重绘确保显示正确，打印换行，清空 pending。
func (s *inputSession) commit() (bool, error) {
	s.redrawFn()
	s.echoNewline()
	flushPending(s.ed, &s.pending)
	return true, nil
}

// processBatch 处理一批从 stdin 读到的字节（批量读取优化）。
// 返回 committed=true 表示输入已提交（调用方应立即返回 string(ed.buf)）。
func (s *inputSession) processBatch(data []byte) (bool, error) {
	// burst 唯一入口 = 批内/批尾换行(粘贴特征); 中文IME上屏(纯文字)永不触发
	for i := 0; i < len(data); i++ {
		ch := data[i]

		// 换行处理(20260808 重写):
		//  - 批中换行(后面还有内容) -> 必然是粘贴: 动态进入收集模式(手动输入不可能批中带\n)
		//  - 批尾换行(非burst)      -> 手动回车: 立即提交, 0延迟, 不做跨批探测
		//  - 批尾换行(burst)        -> 粘贴流的换行: 先刷积累字节再保留\n(顺序保真)
		if ch == '\r' || ch == '\n' {
			if i+1 < len(data) {
				// 批中换行: 粘贴特征, 进入收集模式
				if !s.burst {
					s.burst = true
				}
				s.sawNewline = true
				consumePending(s.ed, &s.pending, false)
				if ch == '\r' && data[i+1] == '\n' {
					i++ // CRLF 同批
				}
				insertRune(s.ed, '\n')
				continue
			}
			// 批尾换行
			if !s.burst {
				// 手动回车 vs 跨批粘贴: 30ms 短探测。
				// (20260808: 原 300ms 会把回车后立刻打的字误判为粘贴并吞进 pending;
				//  30ms 覆盖粘贴批间间隔(<1ms); 手动回车最多延迟 30ms 提交, 人无感知)
				if s.probe(30 * time.Millisecond) {
					next, err := s.readNext()
					if err != nil {
						return false, err
					}
					s.burst = true
					s.sawNewline = true
					consumePending(s.ed, &s.pending, false)
					if ch == '\r' && next == '\n' {
						insertRune(s.ed, '\n') // CRLF: 批尾\r + 开头\n = 一个换行
						continue
					}
					insertRune(s.ed, '\n') // 保留跨批换行
					s.pending = append(s.pending, next)
					continue
				}
				// 手动回车: 立即提交(30ms 内无后续 -> 提交, 不吞键)
				return s.commit()
			}
			// 粘贴流中的批尾换行: 先刷积累字节再插换行(保序)
			s.sawNewline = true
			consumePending(s.ed, &s.pending, false)
			insertRune(s.ed, '\n')
			continue
		}

		// ESC 序列(20260808): 批内完整序列解析(导航键执行/色码静默忽略);
		// 批尾不完整序列丢弃到批尾(防色码残片); 孤立/批尾 ESC 交给 handleByte 流式兜底。
		if ch == 0x1b {
			if i+1 < len(data) && data[i+1] == '[' {
				j := i + 2
				for j < len(data) {
					c := data[j]
					if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '~' {
						j++
						break
					}
					j++
				}
				if j < len(data) {
					// 批内完整序列: 导航执行, 色码/未知序列静默忽略
					s.applySeq(string(data[i+1 : j]))
					i = j - 1
					continue
				}
				// 序列不完整(截断在批尾): 丢弃到批尾, 防色码残片花屏
				i = j - 1
				continue
			}
			// 孤立 ESC / 批尾 ESC: 交给 handleByte (readEscapeSeq 流式读取)
			committed, herr := s.handleByte(ch)
			if herr != nil {
				return committed, herr
			}
			if committed {
				return true, nil
			}
			continue
		}

		// 非换行非 ESC 字节
		committed, herr := s.handleByte(ch)
		if herr != nil {
			return committed, herr
		}
		if committed {
			return true, nil
		}
	}

	// 批尾：burst 短探测(30ms)——粘贴流是否还在继续？
	// (20260808: 原 burstIdle=300ms 会吞掉手动回车后 300ms 内键入的字符 -> "突然无法输入")
	if s.burst {
		if s.probe(30 * time.Millisecond) {
			// 还有后续字节：读一个并处理，然后由调用方继续 Read
			next, err := s.readNext()
			if err != nil {
				return false, err
			}
			if next == '\r' || next == '\n' {
				// 跨批换行：一律作为粘贴内容
				s.sawNewline = true
				consumePending(s.ed, &s.pending, false)
				if next == '\r' {
					// 可能 CRLF：探测并消费 \n
					if s.probe(newlineProbe) {
						n2, err2 := s.readNext()
						if err2 == nil && n2 == '\n' {
							insertRune(s.ed, '\n')
							return false, nil
						}
						if err2 == nil {
							s.pending = append(s.pending, n2)
						}
					}
				}
				insertRune(s.ed, '\n')
				return false, nil
			}
			s.pending = append(s.pending, next)
			return false, nil
		}

		// 粘贴流结束
		s.burst = false
		consumePending(s.ed, &s.pending, false)
		s.redrawFn()
		if s.sawNewline {
			// 多行粘贴：直接提交
			return s.commit()
		}
		// 单行粘贴：留在编辑区，等待用户继续编辑/回车
	}
	return false, nil
}

func readLine(prompt string, history []string) (string, error) {
	if os.Getenv("FORGE_NO_READLINE") == "1" {
		return readLineFallback(prompt)
	}

	// Probe console procs on first call. If any proc is missing the internal
	// mustFind panics; we catch it and fall back to plain line mode *before*
	// we touch the console mode, so the terminal stays in cooked mode.
	probeConsoleProcs()
	if !consoleProcsOK {
		return readLineFallback(prompt)
	}

	h := syscall.Handle(os.Stdin.Fd())
	var orig uint32
	if err := getConsoleMode(h, &orig); err != nil {
		return readLineFallback(prompt)
	}

	// Raw mode: disable line input and echo (keep processed input so Ctrl+C
	// still arrives as a signal rather than a raw byte).
	raw := orig &^ (enableLineInput | enableEchoInput)
	if err := setConsoleMode(h, raw); err != nil {
		return readLineFallback(prompt)
	}
	defer setConsoleMode(h, orig)

	fmt.Print(prompt)

	s := newInputSession(prompt, history)
	// 初始化块顶：prompt 打印后光标所在行即编辑块顶（redraw 推算基准）
	if pos, ok := getCursorPos(); ok {
		s.ed.lastTop = int(pos.Y)
		s.ed.cursorRow = 0
	}
	readByte := func() (byte, error) {
		var one [1]byte
		for {
			n, err := os.Stdin.Read(one[:])
			if n > 0 {
				return one[0], nil
			}
			if err != nil {
				return 0, err
			}
		}
	}
	s.probe = waitInput
	s.readNext = readByte
	s.echoNewline = func() { fmt.Println() }
	s.redrawFn = s.ed.redraw

	// 批量读取：8192 字节/次，替代逐字节 syscall
	var rbuf [readBatchSize]byte
	for {
		n, err := os.Stdin.Read(rbuf[:])
		if n == 0 {
			if err != nil {
				return "", err
			}
			continue
		}
		committed, herr := s.processBatch(rbuf[:n])
		if herr != nil {
			return "", herr
		}
		if committed {
			return filterControl(string(s.ed.buf)), nil
		}
	}
}

func consumePending(ed *lineEditor, pending *[]byte, silent bool) {
	for len(*pending) > 0 {
		r, sz := utf8.DecodeRune(*pending)
		if r != utf8.RuneError || sz > 1 {
			if silent {
				insertSilent(ed, r)
			} else {
				insertRune(ed, r)
			}
			*pending = (*pending)[sz:]
			continue
		}
		// Invalid or incomplete sequence.
		if utf8.RuneStart((*pending)[0]) {
			if len(*pending) < utf8.UTFMax {
				break
			}
			if rs, ok := gbkDecode(*pending); ok && len(rs) > 0 {
				for _, rr := range rs {
					if silent {
						insertSilent(ed, rr)
					} else {
						insertRune(ed, rr)
					}
				}
				*pending = (*pending)[:0]
				break
			}
			*pending = (*pending)[1:]
			continue
		}
		if rs, ok := gbkDecode(*pending); ok && len(rs) > 0 {
			for _, rr := range rs {
				if silent {
					insertSilent(ed, rr)
				} else {
					insertRune(ed, rr)
				}
			}
			*pending = (*pending)[:0]
			break
		}
		*pending = (*pending)[1:]
	}
}

// flushPending decodes any leftover bytes (e.g. GBK bytes pending at the
// moment Enter was pressed) so they are not silently lost.
func flushPending(ed *lineEditor, pending *[]byte) {
	consumePending(ed, pending, true)
	if len(*pending) > 0 {
		if rs, ok := gbkDecode(*pending); ok && len(rs) > 0 {
			for _, rr := range rs {
				insertSilent(ed, rr)
			}
		}
		*pending = (*pending)[:0]
	}
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
		fmt.Print(string(r))
		// 快路径也必须同步块内光标行号：超宽时终端会自动回绕换行，
		// 若 cursorRow 不更新，后续 redraw 的块顶推算 (光标Y-cursorRow) 会错位花屏。
		if pos, ok := getCursorPos(); ok && int(pos.Y) >= ed.lastTop {
			ed.cursorRow = int(pos.Y) - ed.lastTop
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

// maxInputRunes caps a single input line (~1M runes) to bound memory.
const maxInputRunes = 1 << 20

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
	cmds := []string{"/help", "/stats", "/history", "/clear", "/reasoning", "/model", "/router", "/health", "/tools", "/last"}
	for _, c := range cmds {
		if strings.HasPrefix(c, trimmed) && c != trimmed {
			return c + " "
		}
	}
	return ""
}

// gbkDecode decodes b as the system ANSI code page (CP_ACP; GBK on zh-CN
// systems). Returns (nil, false) if b is not valid in that code page.
func gbkDecode(b []byte) ([]rune, bool) {
	if len(b) == 0 {
		return nil, false
	}
	n, _, _ := procMBToWideChar.Call(
		uintptr(cpACP), uintptr(mbErrInvalidChars),
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		0, 0)
	if n == 0 {
		return nil, false
	}
	wbuf := make([]uint16, n)
	r, _, _ := procMBToWideChar.Call(
		uintptr(cpACP), uintptr(mbErrInvalidChars),
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)),
		uintptr(unsafe.Pointer(&wbuf[0])), n)
	if r == 0 {
		return nil, false
	}
	rs := make([]rune, 0, n)
	for i := uintptr(0); i < n; i++ {
		rs = append(rs, rune(wbuf[i]))
	}
	return rs, true
}

// fallbackReader is package-level: a fresh bufio.Reader per call would
// discard bytes already buffered (e.g. the second line of a multiline
// paste) and return a spurious EOF.
var fallbackReader *bufio.Reader

func readLineFallback(prompt string) (string, error) {
	fmt.Print(prompt)
	if fallbackReader == nil {
		fallbackReader = bufio.NewReader(os.Stdin)
	}
	line, err := fallbackReader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	return filterControl(strings.TrimRight(line, "\r\n")), nil
}

// readEscapeSeq parses a complete escape sequence after ESC. Full CSI
// sequences (ESC [ params final-byte, e.g. "[3~" for Delete, "[15~" for
// F5) are consumed entirely so no junk bytes leak into the input buffer.
// A lone ESC press (no byte within 50ms) returns "" instead of blocking.
// 用 waitInput 探测（不消费字节、不泄漏 goroutine）。
func readEscapeSeq(readByte func() (byte, error)) string {
	if !waitInput(50 * time.Millisecond) {
		return "" // lone ESC - ignore
	}
	first, err := readByte()
	if err != nil {
		return "" // Alt+key (ESC x) - consume the modifier char, return ""
	}
	if first != '[' {
		return ""
	}
	seq := "["
	for len(seq) < 16 {
		if !waitInput(50 * time.Millisecond) {
			break
		}
		next, err := readByte()
		if err != nil {
			break
		}
		seq += string(next)
		if next >= 0x40 && next <= 0x7e { // CSI final byte
			break
		}
	}
	return seq
}
