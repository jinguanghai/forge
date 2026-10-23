package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// cmdTip 是命令补全规范: name=显示名(含 / 及别名), args=可选参数候选。
// 与本文件 tabComplete 配对; 新命令须同步登记至此, 表由 handleCommand 与 REPL 命令汇总。
type cmdTip struct {
	name string
	args []string
}

// 粘贴突发检测参数
const (
	newlineProbe  = 30 * time.Millisecond // 换行后跨批探测窗口(粘贴批间<1ms; 手动回车后打字不被吞)
	readBatchSize = 8192                  // 批量读取缓冲（替代逐字节读取）
)

// maxInputRunes caps a single input line (~1M runes) to bound memory.
const maxInputRunes = 1 << 20

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

// fallbackReader is package-level: a fresh bufio.Reader per call would
// discard bytes already buffered (e.g. the second line of a multiline
// paste) and return a spurious EOF.
var fallbackReader *bufio.Reader

// probeConsoleProcs resolves every console LazyProc once.  If any procedure is missing
// from kernel32.dll the internal mustFind panics; we recover, mark consoleProcsOK=false,

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
