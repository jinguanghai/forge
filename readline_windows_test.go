package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

// fakeStream 模拟 Windows 控制台输入缓冲（用于单元测试）。
type fakeStream struct {
	queue []byte
}

func (f *fakeStream) probe(time.Duration) bool { return len(f.queue) > 0 }

func (f *fakeStream) readNext() (byte, error) {
	if len(f.queue) == 0 {
		return 0, io.EOF
	}
	b := f.queue[0]
	f.queue = f.queue[1:]
	return b, nil
}

func newTestSession(input []byte) (*inputSession, *fakeStream) {
	s := newInputSession("> ", nil)
	f := &fakeStream{queue: input}
	s.probe = f.probe
	s.readNext = f.readNext
	s.echoNewline = func() {}
	s.redrawFn = func() {}
	// 回显与重绘一律丢弃: 生产路径直接写 os.Stdout, 会让测试输出粘在
	// go test -v 的 "--- PASS/FAIL:" 行首 (污染按行首统计的工具, 可致 FAIL 漏报)。
	s.ed.out = io.Discard
	return s, f
}

// runSession 模拟真实 Read 循环：queue 中的输入按 batchSize 分批喂给 processBatch，
// 全部喂完后用空批触发 burst 空闲结束判定。
// 返回 (最终缓冲内容, 是否提交, error)。
func runSession(input []byte, batchSize int) (string, bool, error) {
	s, f := newTestSession(input)
	for len(f.queue) > 0 {
		n := batchSize
		if n > len(f.queue) {
			n = len(f.queue)
		}
		d := f.queue[:n]
		f.queue = f.queue[n:]
		committed, err := s.processBatch(d)
		if err != nil {
			return string(s.ed.buf), committed, err
		}
		if committed {
			return string(s.ed.buf), true, nil
		}
	}
	// 模拟用户停顿：空批触发 burst 结束（若已结束则无副作用）
	committed, err := s.processBatch(nil)
	return string(s.ed.buf), committed, err
}

// ---------- 场景 1：同批多行粘贴（LF）——核心修复点 ----------
func TestMultiLinePaste_SameBatch_LF(t *testing.T) {
	got, committed, err := runSession([]byte("line1\nline2"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("多行粘贴应直接提交，实际未提交; buf=%q", got)
	}
	if got != "line1\nline2" {
		t.Fatalf("多行粘贴被截断/错序: got=%q want=%q", got, "line1\nline2")
	}
}

// ---------- 场景 2：同批多行粘贴（CRLF 归一） ----------
func TestMultiLinePaste_SameBatch_CRLF(t *testing.T) {
	got, committed, err := runSession([]byte("a\r\nb\r\nc"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("应提交")
	}
	if got != "a\nb\nc" {
		t.Fatalf("CRLF 应归一为 \\n: got=%q", got)
	}
}

// ---------- 场景 3：跨批多行粘贴（换行恰在批尾） ----------
func TestMultiLinePaste_CrossBatch(t *testing.T) {
	got, committed, err := runSession([]byte("line1\nline2\nline3"), 6)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("应提交")
	}
	if got != "line1\nline2\nline3" {
		t.Fatalf("跨批多行: got=%q", got)
	}
}

// ---------- 场景 4：跨批 CRLF ----------
func TestMultiLinePaste_CRLF_CrossBatch(t *testing.T) {
	got, committed, err := runSession([]byte("a\r\nb"), 3)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("应提交")
	}
	if got != "a\nb" {
		t.Fatalf("跨批 CRLF: got=%q want=%q", got, "a\nb")
	}
}

// ---------- 场景 5：单行粘贴留在编辑区（可继续编辑） ----------
func TestSingleLinePaste_StaysEditable(t *testing.T) {
	got, committed, err := runSession([]byte("hello"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if committed {
		t.Fatalf("单行粘贴不应自动提交")
	}
	if got != "hello" {
		t.Fatalf("单行粘贴内容应完整保留: got=%q", got)
	}

	// 用户继续编辑：追加内容后回车提交
	s, _ := newTestSession(nil)
	s.ed.buf = []rune(got)
	s.ed.cursor = len(s.ed.buf)
	c, err := s.processBatch([]byte(" world"))
	if err != nil {
		t.Fatalf("追加 err: %v", err)
	}
	if c {
		t.Fatalf("追加不应提交")
	}
	c, err = s.processBatch([]byte("\r"))
	if err != nil {
		t.Fatalf("回车 err: %v", err)
	}
	if !c {
		t.Fatalf("回车应提交")
	}
	if string(s.ed.buf) != "hello world" {
		t.Fatalf("编辑后提交内容: got=%q", string(s.ed.buf))
	}
}

// ---------- 场景 6：粘贴流中的 ANSI 色码被丢弃 ----------
func TestPasteWithANSI_Dropped(t *testing.T) {
	got, committed, err := runSession([]byte("ab\x1b[31mcd\x1b[0m"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if committed {
		t.Fatalf("应留在编辑区")
	}
	if got != "abcd" {
		t.Fatalf("ANSI 应丢弃: got=%q", got)
	}
}

// ---------- 场景 7：中文跨批（UTF-8 3字节被拆散） ----------
func TestChinese_CrossBatch(t *testing.T) {
	got, committed, err := runSession([]byte("铸剑炉"), 2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if committed {
		t.Fatalf("单行不应提交")
	}
	if got != "铸剑炉" {
		t.Fatalf("中文跨批: got=%q", got)
	}
}

// ---------- 场景 8：中文多行粘贴 ----------
func TestChinese_MultiLinePaste(t *testing.T) {
	got, committed, err := runSession([]byte("第一行\n第二行"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("应提交")
	}
	if got != "第一行\n第二行" {
		t.Fatalf("中文多行: got=%q", got)
	}
}

// ---------- 场景 9：大粘贴（100KB，多批） ----------
func TestLargePaste_100KB(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 3000; i++ {
		sb.WriteString("行内容 line content with some text\n")
	}
	input := []byte(sb.String())
	got, committed, err := runSession(input, 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("大粘贴应提交")
	}
	if got != string(input) {
		t.Fatalf("大粘贴内容不一致: len(got)=%d len(want)=%d", len(got), len(input))
	}
}

// ---------- 场景 10：手动逐字符输入 + 回车提交（不触发 burst） ----------
func TestManualTyping_EnterCommits(t *testing.T) {
	got, committed, err := runSession([]byte("hi"), 1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if committed {
		t.Fatalf("单字节输入不应提交")
	}
	if got != "hi" {
		t.Fatalf("手动输入: got=%q", got)
	}
	// 回车提交
	s, _ := newTestSession(nil)
	s.ed.buf = []rune(got)
	s.ed.cursor = len(s.ed.buf)
	c, err := s.processBatch([]byte("\r"))
	if err != nil {
		t.Fatalf("回车 err: %v", err)
	}
	if !c {
		t.Fatalf("回车应提交")
	}
	if string(s.ed.buf) != "hi" {
		t.Fatalf("提交内容: got=%q", string(s.ed.buf))
	}
}

// ---------- 场景 11：Ctrl+C 中断 ----------
func TestCtrlC_Interrupt(t *testing.T) {
	_, _, err := runSession([]byte("ab\x03"), 8192)
	if err == nil {
		t.Fatalf("Ctrl+C 应返回中断错误")
	}
}

// ---------- 场景 12：粘贴以换行结尾 ----------
func TestPasteEndingWithNewline(t *testing.T) {
	got, committed, err := runSession([]byte("line1\nline2\n"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("应提交")
	}
	if got != "line1\nline2\n" {
		t.Fatalf("尾部换行应保留: got=%q", got)
	}
}

// ---------- 场景 13：Tab 在粘贴流中 ----------
func TestTabInBurst(t *testing.T) {
	got, committed, err := runSession([]byte("ab\tcd"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if committed {
		t.Fatalf("单行不应提交")
	}
	if got != "ab\tcd" {
		t.Fatalf("Tab 应保留: got=%q", got)
	}
}

// ---------- 场景 14：粘贴中的 Backspace（保留原样字节，交由提交后处理） ----------
func TestBackspaceInBurst(t *testing.T) {
	got, committed, err := runSession([]byte("ab\x7fcd"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// burst 中 Backspace 删除 buf 末尾字符
	if got != "acd" {
		t.Fatalf("burst 中退格应删一个字符: got=%q", got)
	}
	_ = committed
}

// ---------- 场景 15：粘贴后立即回车（<300ms，burst 未结束） ----------
func TestPasteThenQuickEnter(t *testing.T) {
	s, _ := newTestSession(nil)
	// 第一批：粘贴 "hello"（触发 burst，批尾探测无后续 → burst 结束，留在编辑区）
	c, err := s.processBatch([]byte("hello"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c {
		t.Fatalf("单行粘贴不应提交")
	}
	// 用户立即按回车：新一批 \r，burst 已结束 → 正常提交
	c, err = s.processBatch([]byte("\r"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !c {
		t.Fatalf("回车应提交")
	}
	if string(s.ed.buf) != "hello" {
		t.Fatalf("提交内容: got=%q", string(s.ed.buf))
	}
}

// ---------- 场景 16：空输入直接回车（空消息） ----------
func TestEmptyEnter(t *testing.T) {
	got, committed, err := runSession([]byte("\r"), 8192)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !committed {
		t.Fatalf("回车应提交")
	}
	if got != "" {
		t.Fatalf("空提交: got=%q", got)
	}
}
