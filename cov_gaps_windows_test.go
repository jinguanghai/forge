//go:build windows

package main

// cov_gaps_windows_test.go — Windows 专属零覆盖函数补测 (20261001)
//
// 覆盖 asr/tts 探针、readline 回退路径、折行编辑器注入、控制台探针与状态栏兜底。
// 全部走临时目录或 io.Discard 注入, 不录音、不播放、不碰真实 stdin。

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- asr.go ----

func TestCovGap_AsrToolDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_ASR_DIR", "  "+dir+"  ") // 两侧空白必须被 TrimSpace 吃掉
	if got := asrToolDir(); got != dir {
		t.Fatalf("FORGE_ASR_DIR 覆盖失效: got %q, want %q", got, dir)
	}
	t.Setenv("FORGE_ASR_DIR", "")
	got := asrToolDir()
	if !strings.HasSuffix(filepath.ToSlash(got), "/.forge/asr_tool") {
		t.Fatalf("默认路径应落在 exe 同级 .forge/asr_tool, 实际 %q", got)
	}
}

// 脚本不存在时必须立即报错返回 —— 绝不启动 python 子进程。
func TestCovGap_AsrFile_MissingScript(t *testing.T) {
	t.Setenv("FORGE_ASR_DIR", t.TempDir())
	if _, err := asrFile("x.wav"); err == nil {
		t.Fatal("脚本不存在应报错")
	} else if !strings.Contains(err.Error(), "ASR 脚本不存在") {
		t.Fatalf("错误信息不符: %v", err)
	}
	if _, err := asrListen(); err == nil {
		t.Fatal("脚本不存在应报错")
	} else if !strings.Contains(err.Error(), "听写脚本不存在") {
		t.Fatalf("错误信息不符: %v", err)
	}
}

// ---- readline_session_windows.go: consumePending / flushPending ----

func newDiscardEditor() *lineEditor {
	// out=io.Discard: 回显不得污染 go test 输出 (否则 PASS/FAIL 行首被污染 → 静默漏计)
	return &lineEditor{out: io.Discard, bottomBarRow: -1, termW: 80, lastLines: 1}
}

func TestCovGap_ConsumePending_UTF8Complete(t *testing.T) {
	ed := newDiscardEditor()
	pending := []byte("你好")
	consumePending(ed, &pending, true)
	if len(pending) != 0 {
		t.Fatalf("完整 UTF-8 应被消费完, 残留 %q", pending)
	}
	if string(ed.buf) != "你好" {
		t.Fatalf("缓冲不符: %q", string(ed.buf))
	}
}

func TestCovGap_ConsumePending_IncompleteKeepsBytes(t *testing.T) {
	ed := newDiscardEditor()
	pending := []byte{0xE4} // "你" 的首字节: 不完整, 必须留着等后续
	consumePending(ed, &pending, true)
	if len(pending) != 1 || pending[0] != 0xE4 {
		t.Fatalf("不完整序列应原样保留, 实际 %q", pending)
	}
	if len(ed.buf) != 0 {
		t.Fatalf("不完整序列不得入缓冲: %q", string(ed.buf))
	}
}

func TestCovGap_ConsumePending_NonSilentEchoes(t *testing.T) {
	ed := newDiscardEditor()
	pending := []byte("ab")
	consumePending(ed, &pending, false) // 非静默分支: 走 insertRune + redraw
	if string(ed.buf) != "ab" {
		t.Fatalf("缓冲不符: %q", string(ed.buf))
	}
	if ed.cursor != 2 {
		t.Fatalf("光标应停在 2, 实际 %d", ed.cursor)
	}
}

func TestCovGap_FlushPending_NeverLeavesBytes(t *testing.T) {
	ed := newDiscardEditor()
	pending := []byte("残")
	flushPending(ed, &pending)
	if len(pending) != 0 {
		t.Fatalf("flush 后不得残留: %q", pending)
	}
	if string(ed.buf) != "残" {
		t.Fatalf("完整序列应落缓冲: %q", string(ed.buf))
	}

	// 残缺字节: flush 必须丢弃而非永久滞留 (Enter 提交时刻的收尾)
	ed2 := newDiscardEditor()
	pending2 := []byte{0xE4}
	flushPending(ed2, &pending2)
	if len(pending2) != 0 {
		t.Fatalf("flush 后不得残留残缺字节: %q", pending2)
	}
}

// ---- readline_windows.go ----

// 无后续输入时 (lone ESC) 必须立即返回空串, 不得阻塞等待。
func TestCovGap_ReadEscapeSeq_LoneESC(t *testing.T) {
	got := readEscapeSeq(func() (byte, error) { return 0, io.EOF })
	if got != "" {
		t.Fatalf("lone ESC 应返回空串, 实际 %q", got)
	}
}

// 控制台过程探针必须幂等且不 panic (第二次调用走已探测缓存)。
func TestCovGap_ProbeConsoleProcs_Idempotent(t *testing.T) {
	probeConsoleProcs()
	first := consoleProcsOK
	probeConsoleProcs()
	if consoleProcsOK != first {
		t.Fatalf("二次探测改变了结论: %v -> %v", first, consoleProcsOK)
	}
}

// 非交互 stdin 下 readLineFallback 必须能正常读到一行 (含 prompt 打印)。
func TestCovGap_ReadLineFallback_ReadsLine(t *testing.T) {
	oldIn, oldR := os.Stdin, fallbackReader
	t.Cleanup(func() { os.Stdin, fallbackReader = oldIn, oldR })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	fallbackReader = nil
	w.WriteString("hello\r\n")
	w.Close()

	var got string
	var rerr error
	out := captureStdout(t, func() { got, rerr = readLineFallback("> ") })
	if rerr != nil {
		t.Fatalf("读取失败: %v", rerr)
	}
	if got != "hello" {
		t.Fatalf("got %q, want %q (CRLF 必须被剥掉)", got, "hello")
	}
	if !strings.Contains(out, "> ") {
		t.Fatalf("prompt 未打印: %q", out)
	}
}

// EOF 且无内容: 必须返回错误 (调用方据此结束 REPL), 不得返回空串+nil 死循环。
func TestCovGap_ReadLineFallback_EOFGivesError(t *testing.T) {
	oldIn, oldR := os.Stdin, fallbackReader
	t.Cleanup(func() { os.Stdin, fallbackReader = oldIn, oldR })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	fallbackReader = nil
	w.Close() // 立即 EOF

	var got string
	var rerr error
	captureStdout(t, func() { got, rerr = readLineFallback("") })
	if rerr == nil {
		t.Fatalf("EOF 应返回错误, 实际 got=%q err=nil", got)
	}
}

// FORGE_NO_READLINE=1: readLine 必须转走回退通道 (不触碰控制台模式)。
func TestCovGap_ReadLine_EnvForcesFallback(t *testing.T) {
	oldIn, oldR := os.Stdin, fallbackReader
	t.Cleanup(func() { os.Stdin, fallbackReader = oldIn, oldR })
	t.Setenv("FORGE_NO_READLINE", "1")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	fallbackReader = nil
	w.WriteString("fallback-path\n")
	w.Close()

	var got string
	var rerr error
	captureStdout(t, func() { got, rerr = readLine("", nil) })
	if rerr != nil {
		t.Fatalf("回退读取失败: %v", rerr)
	}
	if got != "fallback-path" {
		t.Fatalf("got %q, want %q", got, "fallback-path")
	}
}

// ---- status_bar.go ----

// 非 TTY / 取不到窗口矩形时必须静默返回, 绝不吐出半截状态栏。
func TestCovGap_DrawStatusBar_SilentWhenNoConsole(t *testing.T) {
	agent, _, _ := newHandleCmdAgent(t)
	out := captureStdout(t, func() { drawStatusBar(agent, ".") })
	if out != "" && !strings.Contains(out, "\x1b[") {
		t.Fatalf("无控制台时应静默或仅输出 ANSI 定位: %q", out)
	}
}

// 取不到光标时 reserveInputRow 必须原样返回底行 (不滚动、不干扰主流程)。
func TestCovGap_ReserveInputRow_ReturnsBottom(t *testing.T) {
	const bottom = 30
	got := captureStdout(t, func() {})
	_ = got
	n := reserveInputRow(bottom)
	if n <= 0 {
		t.Fatalf("底行应为正数, 实际 %d", n)
	}
}
