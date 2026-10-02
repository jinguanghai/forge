package main

import (
	"strings"
	"testing"
)

// 分块喂入: feed 返回"渲染后的显示文本"(含边框/缩进/高亮), code() 返回解码原文。
func TestStreamerChunked(t *testing.T) {
	chunks := []string{`{"act`, `ion":"run","code":"import o`, `s\\nprint(hi)"`, `,"lang":"python","inp`, `ut":""}`}
	var st toolCodeStreamer
	got := ""
	for _, c := range chunks {
		got += st.feed(c, "forge")
	}
	got += st.flush()
	plain := reAnsiStrip.ReplaceAllString(got, "")
	if !strings.Contains(plain, "import os") || !strings.Contains(plain, "print(hi)") {
		t.Fatalf("显示文本缺内容: got %q", got)
	}
	if st.code() != "import os\\nprint(hi)" {
		t.Fatalf("chunked mismatch: got %q", st.code())
	}
}

func TestExtractCodeBasic(t *testing.T) {
	if r := extractCodeFromArgs(`{"c":"x","code":"abc"}`); r != "abc" {
		t.Fatalf("basic=%q", r)
	}
	if r := extractCodeFromArgs(`{"code":"a\\nb"}`); r != "a\\nb" {
		t.Fatalf("esc=%q", r)
	}
	if r := extractCodeFromArgs(`{"code":"abc`); r != "abc" {
		t.Fatalf("unclosed=%q", r)
	}
	if r := extractCodeFromArgs(`{"action":"run"}`); r != "" {
		t.Fatalf("nocode=%q", r)
	}
}
