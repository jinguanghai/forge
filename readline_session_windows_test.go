//go:build windows

package main

import (
	"io"
	"testing"
	"time"
)

// mkTestSession 构造一个终端依赖全部注入的会话（不碰真实控制台）。
// 返回的重绘计数器用于断言"无操作不重绘"（避免无谓重绘带来的闪烁与开销）。
func mkTestSession(hist []string) (*inputSession, *int) {
	s := newInputSession("> ", hist)
	n := 0
	s.redrawFn = func() { n++ }
	s.echoNewline = func() {}
	s.probe = func(time.Duration) bool { return false }
	s.readNext = func() (byte, error) { return 0, io.EOF }
	s.ed.out = io.Discard
	return s, &n
}

func TestSessionApplySeqHistoryBack(t *testing.T) {
	s, n := mkTestSession([]string{"one", "two"})
	if s.histIdx != 2 {
		t.Fatalf("初始 histIdx = %d, 期望 2 (== len(history), 即\"新输入\"位置)", s.histIdx)
	}
	s.applySeq("[A")
	if s.histIdx != 1 || string(s.ed.buf) != "two" || s.ed.cursor != 3 {
		t.Errorf("[A 后 histIdx=%d buf=%q cursor=%d, 期望 1/\"two\"/3", s.histIdx, string(s.ed.buf), s.ed.cursor)
	}
	s.applySeq("[A")
	if s.histIdx != 0 || string(s.ed.buf) != "one" {
		t.Errorf("再 [A 后 histIdx=%d buf=%q, 期望 0/\"one\"", s.histIdx, string(s.ed.buf))
	}
	if *n != 2 {
		t.Errorf("重绘次数 = %d, 期望 2（每次有效翻历史各一次）", *n)
	}
}

func TestSessionApplySeqHistoryForward(t *testing.T) {
	s, _ := mkTestSession([]string{"one", "two"})
	s.applySeq("[A")
	s.applySeq("[A")
	s.applySeq("[B")
	if s.histIdx != 1 || string(s.ed.buf) != "two" {
		t.Errorf("[B 后 histIdx=%d buf=%q, 期望 1/\"two\"", s.histIdx, string(s.ed.buf))
	}
	s.applySeq("[B")
	// 越过末尾回到"新输入"位置：缓冲必须清空，否则用户会看到上一条历史残留
	if s.histIdx != 2 || string(s.ed.buf) != "" || s.ed.cursor != 0 {
		t.Errorf("到底后 histIdx=%d buf=%q cursor=%d, 期望 2/\"\"/0", s.histIdx, string(s.ed.buf), s.ed.cursor)
	}
}

func TestSessionApplySeqHistoryBoundary(t *testing.T) {
	s, n := mkTestSession([]string{"one"})
	// 已在最旧一条，再 [A 不得越界
	s.applySeq("[A")
	*n = 0
	s.applySeq("[A")
	if s.histIdx != 0 || *n != 0 {
		t.Errorf("顶部再 [A: histIdx=%d 重绘=%d, 期望 0/0（越界不应重绘）", s.histIdx, *n)
	}
	// 已在"新输入"位置，再 [B 不得越界
	s.applySeq("[B")
	s.applySeq("[B")
	*n = 0
	s.applySeq("[B")
	if s.histIdx != 1 || *n != 0 {
		t.Errorf("底部再 [B: histIdx=%d 重绘=%d, 期望 1/0", s.histIdx, *n)
	}
}

func TestSessionApplySeqCursorMove(t *testing.T) {
	s, _ := mkTestSession(nil)
	s.ed.buf = []rune("abc")
	s.ed.cursor = 0
	s.applySeq("[C") // Right
	if s.ed.cursor != 1 {
		t.Errorf("[C 后 cursor = %d, 期望 1", s.ed.cursor)
	}
	s.applySeq("[D") // Left
	if s.ed.cursor != 0 {
		t.Errorf("[D 后 cursor = %d, 期望 0", s.ed.cursor)
	}
	s.applySeq("[F") // End
	if s.ed.cursor != 3 {
		t.Errorf("[F 后 cursor = %d, 期望 3", s.ed.cursor)
	}
	s.applySeq("[H") // Home
	if s.ed.cursor != 0 {
		t.Errorf("[H 后 cursor = %d, 期望 0", s.ed.cursor)
	}
}

func TestSessionApplySeqCursorBoundary(t *testing.T) {
	s, n := mkTestSession(nil)
	s.ed.buf = []rune("ab")
	s.ed.cursor = 0
	*n = 0
	s.applySeq("[D") // 已在最左
	s.applySeq("[H")
	if s.ed.cursor != 0 || *n != 0 {
		t.Errorf("左边界: cursor=%d 重绘=%d, 期望 0/0", s.ed.cursor, *n)
	}
	s.ed.cursor = 2
	*n = 0
	s.applySeq("[C") // 已在最右
	s.applySeq("[F")
	if s.ed.cursor != 2 || *n != 0 {
		t.Errorf("右边界: cursor=%d 重绘=%d, 期望 2/0", s.ed.cursor, *n)
	}
}

func TestSessionApplySeqDelete(t *testing.T) {
	s, _ := mkTestSession(nil)
	s.ed.buf = []rune("abc")
	s.ed.cursor = 1
	s.applySeq("[3~") // Delete：删光标处字符，光标不动
	if string(s.ed.buf) != "ac" || s.ed.cursor != 1 {
		t.Errorf("Delete 后 buf=%q cursor=%d, 期望 \"ac\"/1", string(s.ed.buf), s.ed.cursor)
	}
	// 光标在末尾时 Delete 无效果
	s.ed.cursor = 2
	s.applySeq("[3~")
	if string(s.ed.buf) != "ac" {
		t.Errorf("末尾 Delete 后 buf=%q, 期望不变 \"ac\"", string(s.ed.buf))
	}
}

func TestSessionApplySeqCtrlWord(t *testing.T) {
	s, _ := mkTestSession(nil)
	s.ed.buf = []rune("hello world")
	s.ed.cursor = 11
	s.applySeq("[1;5D") // Ctrl+Left
	if s.ed.cursor != 6 {
		t.Errorf("Ctrl+Left 后 cursor = %d, 期望 6", s.ed.cursor)
	}
	s.applySeq("[1;5C") // Ctrl+Right
	if s.ed.cursor != 11 {
		t.Errorf("Ctrl+Right 后 cursor = %d, 期望 11", s.ed.cursor)
	}
}

func TestSessionApplySeqUnknownIgnored(t *testing.T) {
	s, n := mkTestSession(nil)
	s.ed.buf = []rune("abc")
	s.ed.cursor = 3
	*n = 0
	// "[2~"(Insert) 与 F1-F12 被有意忽略：不得改动缓冲，也不得触发重绘
	for _, seq := range []string{"[2~", "[15~", "[A~", "OP", ""} {
		s.applySeq(seq)
	}
	if string(s.ed.buf) != "abc" || s.ed.cursor != 3 || *n != 0 {
		t.Errorf("忽略序列后 buf=%q cursor=%d 重绘=%d, 期望 \"abc\"/3/0", string(s.ed.buf), s.ed.cursor, *n)
	}
}

func TestSessionHandleByteAppend(t *testing.T) {
	s, _ := mkTestSession(nil)
	ok, err := s.handleByte('a')
	if ok || err != nil {
		t.Errorf("handleByte('a') = (%v,%v), 期望 (false,nil)", ok, err)
	}
	s.handleByte('b')
	if string(s.ed.buf) != "ab" {
		t.Errorf("buf = %q, 期望 \"ab\"", string(s.ed.buf))
	}
	// 非 burst 模式下 pending 应被立即消费
	if len(s.pending) != 0 {
		t.Errorf("pending 长度 = %d, 期望 0（非 burst 应立即消费）", len(s.pending))
	}
}

func TestSessionHandleByteBackspace(t *testing.T) {
	s, _ := mkTestSession(nil)
	s.handleByte('a')
	s.handleByte('b')
	ok, _ := s.handleByte(0x08)
	if ok {
		t.Error("退格不应提交")
	}
	if string(s.ed.buf) != "a" || s.ed.cursor != 1 {
		t.Errorf("退格后 buf=%q cursor=%d, 期望 \"a\"/1", string(s.ed.buf), s.ed.cursor)
	}
	// DEL(0x7f) 与 BS(0x08) 等价
	s.handleByte(0x7f)
	if string(s.ed.buf) != "" || s.ed.cursor != 0 {
		t.Errorf("DEL 后 buf=%q cursor=%d, 期望 \"\"/0", string(s.ed.buf), s.ed.cursor)
	}
	// 空缓冲退格不得越界 panic
	s.handleByte(0x08)
	if s.ed.cursor != 0 {
		t.Errorf("空缓冲退格后 cursor = %d, 期望 0", s.ed.cursor)
	}
}

func TestSessionHandleByteCtrlCInterrupt(t *testing.T) {
	s, _ := mkTestSession(nil)
	s.ed.buf = []rune("abc")
	ok, err := s.handleByte(0x03)
	if !ok {
		t.Error("Ctrl+C 应返回 ok=true")
	}
	if err != errInterrupt {
		t.Errorf("Ctrl+C err = %v, 期望 errInterrupt", err)
	}
	if len(s.pending) != 0 {
		t.Errorf("Ctrl+C 后 pending 长度 = %d, 期望 0（必须冲刷）", len(s.pending))
	}
}

func TestSessionHandleByteTab(t *testing.T) {
	s, _ := mkTestSession(nil)
	s.ed.buf = []rune("/he")
	s.ed.cursor = 3
	s.handleByte(0x09)
	if string(s.ed.buf) != "/health " {
		t.Errorf("Tab 补全后 buf = %q, 期望 \"/health \"", string(s.ed.buf))
	}
	if s.ed.cursor != len(s.ed.buf) {
		t.Errorf("补全后 cursor = %d, 期望 %d（末尾）", s.ed.cursor, len(s.ed.buf))
	}
	// 无补全候选且非 burst → 插入字面 TAB
	s2, _ := mkTestSession(nil)
	s2.handleByte(0x09)
	if string(s2.ed.buf) != "\t" {
		t.Errorf("无候选 Tab 后 buf = %q, 期望 \"\\t\"", string(s2.ed.buf))
	}
}

func TestSessionHandleByteLFDoesNotCommit(t *testing.T) {
	// LF 的提交由 processBatch/readLine 处理，handleByte 本身不得提交
	s, _ := mkTestSession(nil)
	ok, err := s.handleByte(0x0a)
	if ok || err != nil {
		t.Errorf("handleByte(0x0a) = (%v,%v), 期望 (false,nil)", ok, err)
	}
}

func TestSessionHandleByteEscInBurstDropped(t *testing.T) {
	// 粘贴流(burst)中的 ESC 必须丢弃：否则 ANSI 色码会被当成方向键序列执行，
	// 导致粘贴的彩色文本把光标乱跳、内容错乱。
	s, n := mkTestSession(nil)
	s.ed.buf = []rune("abc")
	s.ed.cursor = 3
	s.burst = true
	*n = 0
	ok, err := s.handleByte(0x1b)
	if ok || err != nil {
		t.Errorf("burst ESC = (%v,%v), 期望 (false,nil)", ok, err)
	}
	if string(s.ed.buf) != "abc" || *n != 0 {
		t.Errorf("burst ESC 后 buf=%q 重绘=%d, 期望不变 \"abc\"/0", string(s.ed.buf), *n)
	}
}

func TestSessionCommit(t *testing.T) {
	s, n := mkTestSession(nil)
	s.ed.buf = []rune("hello")
	s.ed.cursor = 5
	ok, err := s.commit()
	if !ok || err != nil {
		t.Errorf("commit() = (%v,%v), 期望 (true,nil)", ok, err)
	}
	if *n == 0 {
		t.Error("commit 应触发一次重绘（把光标移到块底再换行）")
	}
}

func TestSessionConsumePending(t *testing.T) {
	ed := mkTestEditor("> ")
	var pend []byte
	pend = append(pend, 'h', 'i')
	consumePending(ed, &pend, false)
	if string(ed.buf) != "hi" {
		t.Errorf("consumePending 后 buf = %q, 期望 \"hi\"", string(ed.buf))
	}
	if len(pend) != 0 {
		t.Errorf("pending 长度 = %d, 期望 0（必须清空，否则会重复消费）", len(pend))
	}
}

func TestSessionFlushPending(t *testing.T) {
	ed := mkTestEditor("> ")
	var pend []byte
	pend = append(pend, 'x')
	flushPending(ed, &pend)
	if string(ed.buf) != "x" {
		t.Errorf("flushPending 后 buf = %q, 期望 \"x\"", string(ed.buf))
	}
	if len(pend) != 0 {
		t.Errorf("pending 长度 = %d, 期望 0", len(pend))
	}
}

func TestSessionProcessBatch(t *testing.T) {
	s, _ := mkTestSession(nil)
	ok, err := s.processBatch([]byte("ab\n"))
	if !ok || err != nil {
		t.Errorf("processBatch(\"ab\\n\") = (%v,%v), 期望 (true,nil)", ok, err)
	}
	if string(s.ed.buf) != "ab" {
		t.Errorf("processBatch 后 buf = %q, 期望 \"ab\"（换行不得进入缓冲）", string(s.ed.buf))
	}
}
