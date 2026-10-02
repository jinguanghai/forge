//go:build windows

package main

// readline_windows_cov_test.go — 行编辑器纯逻辑分支补测 (无 TTY 依赖)
// 编辑器 out 可注入 io.Discard, 因此回显路径不会污染 go test 输出。

import (
	"io"
	"testing"
)

func covNewEd() *lineEditor {
	ed := newLineEditor("> ")
	ed.out = io.Discard
	return ed
}

func TestCovReadline_WordNav(t *testing.T) {
	buf := []rune("hello world 中文")
	for _, cur := range []int{0, 1, 5, 6, 11, 12, len(buf)} {
		p := prevWord(buf, cur)
		if p < 0 || p > len(buf) {
			t.Errorf("prevWord(cursor=%d)=%d 越界", cur, p)
		}
		n := nextWord(buf, cur)
		if n < 0 || n > len(buf) {
			t.Errorf("nextWord(cursor=%d)=%d 越界", cur, n)
		}
	}
	if got := prevWord([]rune("hello world"), 11); got != 6 {
		t.Errorf("prevWord(hello world, 11)=%d, want 6", got)
	}
	// nextWord 跳到「下一个词首」(跳过当前词与随后的空白): "hello world" 0 -> 6
	if got := nextWord([]rune("hello world"), 0); got != 6 {
		t.Errorf("nextWord(hello world, 0)=%d, want 6", got)
	}
	if got := prevWord(nil, 0); got != 0 {
		t.Errorf("prevWord(nil,0)=%d, want 0", got)
	}
}

func TestCovReadline_GbkAndFilter(t *testing.T) {
	rs, ok := gbkDecode([]byte{0xD6, 0xD0, 0xCE, 0xC4}) // GBK "中文"
	if !ok || string(rs) != "中文" {
		t.Errorf("gbkDecode 中文 = %q ok=%v", string(rs), ok)
	}
	if rs, ok := gbkDecode([]byte("abc")); ok && string(rs) != "abc" {
		t.Errorf("gbkDecode(ascii) = %q", string(rs))
	}
	if _, ok := gbkDecode([]byte{0xD6}); ok {
		t.Error("不完整 GBK 序列不应判成功")
	}
	if _, ok := gbkDecode(nil); ok {
		t.Error("空输入不应判成功")
	}
	for _, s := range []string{"abc", "中文", "a\x01b", "\x1b[31mred", ""} {
		_ = filterControl(s)
	}
}

func TestCovReadline_TabComplete(t *testing.T) {
	for _, s := range []string{"", "a", "readline", "./", "不存在的路径xyz", "C:\\"} {
		_ = tabComplete(s)
	}
}

func TestCovReadline_EscapeSeq(t *testing.T) {
	seqs := []string{"A", "B", "C", "D", "H", "F", "3~", "1~", "4~", "5~", "6~", "Z", ""}
	for _, s := range seqs {
		data := []byte(s)
		i := 0
		got := readEscapeSeq(func() (byte, error) {
			if i >= len(data) {
				return 0, io.EOF
			}
			b := data[i]
			i++
			return b, nil
		})
		_ = got
	}
}

func TestCovReadline_InsertAndPending(t *testing.T) {
	ed := covNewEd()
	insertRune(ed, 'a')
	insertRune(ed, '中')
	if len(ed.buf) == 0 {
		t.Error("insertRune 未写入缓冲区")
	}
	insertSilent(ed, 'b')
	pending := []byte("xy")
	consumePending(ed, &pending, true)
	flushPending(ed, &pending)
	if len(pending) != 0 {
		t.Logf("pending 剩余 %d 字节", len(pending))
	}
	// 边界: 空 pending 与 nil
	empty := []byte{}
	consumePending(ed, &empty, false)
	flushPending(ed, &empty)
}
