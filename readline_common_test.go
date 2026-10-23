package main

import (
	"strings"
	"testing"
	"time"
)

// readline_common.go 的跨平台契约判据(双平台可跑)。

func TestFilterControlStripsControlChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"he\x00llo", "hello"},
		{"tab\tkept", "tab\tkept"},
		{"del\x7fhere", "delhere"},
		{"中文\x01文本", "中文文本"},
		{"a\x1fb", "ab"},
	}
	for _, c := range cases {
		if got := filterControl(c.in); got != c.want {
			t.Errorf("filterControl(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestReadEscapeSeqDoesNotBlockWhenNoInput(t *testing.T) {
	// 无后续字节时 readEscapeSeq 必须快速返回空串, 且不消费任何字节 ——
	// 否则单按 ESC 会卡住输入循环(readline_windows.go 的 waitInput 契约)。
	calls := 0
	start := time.Now()
	seq := readEscapeSeq(func() (byte, error) { calls++; return 'x', nil })
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("无输入时 readEscapeSeq 阻塞过久: %v", d)
	}
	if seq != "" && !strings.HasPrefix(seq, "[") {
		t.Errorf("readEscapeSeq 返回值异常: %q", seq)
	}
	if seq == "" && calls != 0 {
		t.Errorf("无输入时不应消费字节, 实际调用 readByte %d 次", calls)
	}
}

func TestCmdTipsWellFormed(t *testing.T) {
	if len(cmdTips) == 0 {
		t.Fatal("cmdTips 为空 —— Tab 补全形同虚设")
	}
	seen := map[string]bool{}
	for _, tip := range cmdTips {
		if !strings.HasPrefix(tip.name, "/") {
			t.Errorf("命令候选 %q 必须以 / 开头", tip.name)
		}
		if seen[tip.name] {
			t.Errorf("命令候选 %q 重复", tip.name)
		}
		seen[tip.name] = true
	}
}

func TestMaxInputRunesBounded(t *testing.T) {
	if maxInputRunes <= 0 || maxInputRunes > 1<<22 {
		t.Errorf("maxInputRunes=%d 不合理(须为正且有界, 防粘贴撑爆内存)", maxInputRunes)
	}
}

func TestPasteProbeWindowsAreShort(t *testing.T) {
	// 30ms 是实测定标值(20260808): 覆盖粘贴批间间隔(<1ms) 又不吞手动回车后的键入。
	// 放大到 300ms 会把「回车后立刻打字」误判成粘贴(历史事故)。
	if newlineProbe > 100*time.Millisecond {
		t.Errorf("newlineProbe=%v 过大 —— 会吞掉手动回车后键入的字符", newlineProbe)
	}
	if readBatchSize < 256 {
		t.Errorf("readBatchSize=%d 过小 —— 批量读取优化失效", readBatchSize)
	}
}
