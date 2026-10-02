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
	"unsafe"
)

// cmdTip 是命令补全规范: name=显示名(含 / 及别名), args=可选参数候选。
// 与本文件 tabComplete 配对; 新命令须同步登记至此, 表由 handleCommand 与 REPL 命令汇总。
type cmdTip struct {
	name string
	args []string
}

const (
	cpACP             = 0 // system ANSI code page (GBK on zh-CN)
	mbErrInvalidChars = 0x8
)

const (
	enableLineInput            = 0x0002
	enableEchoInput            = 0x0004
	enableExtendedFlags        = 0x0080
	enableQuickEditMode        = 0x0040
	enableVirtualTerminalInput = 0x0200
)

// 粘贴突发检测参数
const (
	newlineProbe  = 30 * time.Millisecond // 换行后跨批探测窗口(粘贴批间<1ms; 手动回车后打字不被吞)
	readBatchSize = 8192                  // 批量读取缓冲（替代逐字节读取）
)

// keyEvent 是 INPUT_RECORD 的事件类型常量
const keyEvent = 0x0001

// maxInputRunes caps a single input line (~1M runes) to bound memory.
const maxInputRunes = 1 << 20

var (
	kernel32DLL                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode           = kernel32DLL.NewProc("GetConsoleMode")
	procSetConsoleMode           = kernel32DLL.NewProc("SetConsoleMode")
	procMBToWideChar             = kernel32DLL.NewProc("MultiByteToWideChar")
	procSetConsoleCursorPosition = kernel32DLL.NewProc("SetConsoleCursorPosition")
	// ⚠ kernel32.dll 只导出 PeekConsoleInputA/W 与 ReadConsoleInputA/W, 无后缀名不存在。
	// 写错 → resolveConsoleProcs/probeConsoleProcs 必然失败 → consoleProcsOK 恒 false
	// → readLine 永远走 readLineFallback、getTermWidth 恒 80、状态栏静默失效 (20260920 修复)。
	// 取 W 版: INPUT_RECORD.uChar 为 UTF-16, 与 inputRecord 的 16 字节 union 一致。
	procPeekConsoleInput = kernel32DLL.NewProc("PeekConsoleInputW")
	procReadConsoleInput = kernel32DLL.NewProc("ReadConsoleInputW")
	consoleProcsOK       bool // set true by probeConsoleProcs if all resolve
)

// errInterrupt signals Ctrl+C during line input (returned only when processed
// input is unavailable; normally Ctrl+C arrives as a signal instead).
var errInterrupt = errors.New("interrupt")

var cmdTips = []cmdTip{
	{"/?", nil}, {"/anchor", nil}, {"/cache", nil}, {"/clear", nil},
	{"/diagnose", nil}, {"/folded", nil}, {"/folds", nil}, {"/gates", nil},
	{"/gatesync", nil}, {"/goal", []string{"list", "pause", "resume", "complete", "blocked", "clear"}},
	{"/h", nil}, {"/health", nil}, {"/help", nil}, {"/history", nil},
	{"/last", nil}, {"/listen", nil}, {"/memdiag", nil}, {"/memhealth", nil},
	{"/model", nil}, {"/new", nil}, {"/reasoning", nil}, {"/router", []string{"auto", "flash", "pro", "fixed"}},
	{"/see", nil}, {"/sessions", nil}, {"/stats", nil}, {"/theme", []string{"neon", "cold", "warm"}},
	{"/tools", nil}, {"/unfold", nil}, {"/upgrade", nil}, {"/use", nil},
	{"/vision", nil}, {"/voice", nil}, {"/看图", nil},
}

// fallbackReader is package-level: a fresh bufio.Reader per call would
// discard bytes already buffered (e.g. the second line of a multiline
// paste) and return a spurious EOF.
var fallbackReader *bufio.Reader

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
	// 仅在确有输入时才 ReadConsoleInput: 避免无输入时阻塞(启动探针/首轮 drawStatusBar 场景)
	if numRead > 0 {
		procReadConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&rec)), 1, uintptr(unsafe.Pointer(&numRead)))
	}
	procSetConsoleCursorPosition.Call(uintptr(h), 0)
	// procMBToWideChar is used only for GBK decoding – try it with an empty buffer.
	var dummy byte
	procMBToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&dummy)), 0, 0, 0)
	consoleProcsOK = true
}

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

// rawConsoleMode 计算行编辑期间的 raw 控制台输入模式位 (纯函数, 便于哨兵测试)。
//
// 必须保留 ENABLE_QUICK_EDIT_MODE(0x40)——禁止再加回 &^= enableQuickEditMode。
// 机理(20260923 逐位受控实验定位): ConPTY 下清掉该位会同步给 Windows Terminal,
// WT 判定『应用接管鼠标』, 于是把滚轮事件转发进本进程 stdin; 本进程 os.Stdin.Read
// 按 ReadFile 语义直接丢弃鼠标记录 → 滚轮彻底静默(既不滚屏也不产生输入)。
// 实验数据: 同一进程逐位对比, 唯一变量=0x40, 清掉它滚轮画面 diff=0, 保留则 diff>45000。
//
// 其余位: 清 LINE_INPUT/ECHO 以进入行编辑, 置 VT_INPUT 以收 ANSI 键序;
// 不主动置位 QUICK_EDIT(仅保留原值), 避免改变非交互启动路径的行为。
func rawConsoleMode(orig uint32) uint32 {
	m := orig | enableVirtualTerminalInput | enableExtendedFlags
	m &^= enableLineInput | enableEchoInput
	return m
}

// cookedBaseline 把控制台输入模式补回「至少可交互」(20261002)。
// 实测事故: readLine 退出若 defer 未跑 (进程被强杀/超时退), 控制台永久停在
// 0x03F1 (LINE|ECHO|WINDOW 三位被清), 主人在 (y/N) 提示处敲键完全无反应 —
// 回车只产生 \r 不产生 \n, ReadString('\n') 永久阻塞; 且 ECHO 关闭导致
// 屏上什么都不显示。修正: 恢复目标强制补回 LINE_INPUT + ECHO_INPUT, 其余位
// 保留 — 不强加 WINDOW_INPUT (PowerShell 默认开, 某些宿主会刻意关, 不越权)。
// orig 已是 cooked 则原样返回; orig 缺 LINE 或 ECHO 则补回 (棘轮锁死的解药)。
func cookedBaseline(orig uint32) uint32 {
	return orig | enableLineInput | enableEchoInput
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
	raw := rawConsoleMode(orig)
	if err := setConsoleMode(h, raw); err != nil {
		return readLineFallback(prompt)
	}
	// 恢复时走 cookedBaseline: 即便 orig 因异常路径变成 raw 残留, 也能补回

	fmt.Print(prompt)

	s := newInputSession(prompt, history)
	// 初始化块顶：prompt 打印后光标所在行即编辑块顶（redraw 推算基准）
	if pos, ok := getCursorPos(); ok {
		s.ed.lastTop = int(pos.Y)
		s.ed.cursorRow = 0
	}
	// 记录状态栏所在行(bottom), 供 redraw 在清行/重绘时保留该行不被覆盖
	if _, bottom, ok := consoleWindowRect(); ok {
		s.ed.bottomBarRow = bottom
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
			// n==0 && err==nil: 底层伪空读(宽度未对齐/非阻塞), 让步防忙转
			time.Sleep(1 * time.Millisecond)
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
			// n==0 && err==nil: 让步防忙转
			time.Sleep(1 * time.Millisecond)
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
