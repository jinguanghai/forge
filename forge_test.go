package main

import (
	"strings"
	"testing"
)

// ─── 编译错误解析 ────────────────────────────────────────────

func TestParseGoErr(t *testing.T) {
	text := "main.go:12:5: undefined: foo\nother.go:3:1: syntax error"
	errs := parseGoErr(text)
	if len(errs) != 2 {
		t.Fatalf("want 2 errors, got %d", len(errs))
	}
	e := errs[0]
	if e.Lang != "go" || e.Line != 12 || e.Col != 5 || e.Msg != "undefined: foo" {
		t.Errorf("err0 = %+v", e)
	}
	if errs[1].Line != 3 || errs[1].Col != 1 {
		t.Errorf("err1 = %+v", errs[1])
	}
}

func TestParsePyErr(t *testing.T) {
	text := "Traceback (most recent call last):\n" +
		"  File \"C:\\x\\code.py\", line 4, in <module>\n" +
		"    print(1/0)\n" +
		"ZeroDivisionError: division by zero"
	errs := parsePyErr(text)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d", len(errs))
	}
	e := errs[0]
	if e.Line != 4 {
		t.Errorf("Line = %d, want 4", e.Line)
	}
	if !strings.Contains(e.Msg, "ZeroDivisionError") {
		t.Errorf("Msg = %q", e.Msg)
	}
}

func TestParseNodeErr(t *testing.T) {
	text := "[stdin]:3\nTypeError: x is not a function\n    at [stdin]:3:15"
	errs := parseNodeErr(text)
	if len(errs) != 1 {
		t.Fatalf("want 1 error, got %d", len(errs))
	}
	e := errs[0]
	if e.Line != 3 {
		t.Errorf("Line = %d, want 3", e.Line)
	}
	if !strings.Contains(e.Msg, "TypeError") {
		t.Errorf("Msg = %q", e.Msg)
	}
}

func TestParseCompilerError_ANSIStrip(t *testing.T) {
	text := "\x1b[31mmain.go:1:1: boom\x1b[0m"
	errs := parseCompilerError("go", text)
	if len(errs) != 1 || errs[0].Msg != "boom" {
		t.Fatalf("ANSI not stripped: %+v", errs)
	}
}

func TestParseCompilerError_UnknownLang(t *testing.T) {
	if errs := parseCompilerError("fortran", "anything"); errs != nil {
		t.Fatalf("unknown lang should return nil, got %+v", errs)
	}
}

func TestCompilerErrorsToJSON(t *testing.T) {
	if compilerErrorsToJSON(nil) != "" {
		t.Error("empty input should return empty string")
	}
	js := compilerErrorsToJSON([]CompilerError{{Lang: "go", Line: 1, Col: 2, Msg: "m"}})
	if !strings.Contains(js, `"lang":"go"`) || !strings.Contains(js, `"line":1`) || !strings.Contains(js, `"col":2`) {
		t.Errorf("json = %s", js)
	}
}

// ─── 分发/回退决策 ──────────────────────────────────────────

func TestShouldFallback(t *testing.T) {
	if shouldFallback(ForgeGateResult{OK: true}) {
		t.Error("OK result should not fallback")
	}
	for _, errText := range []string{"timeout after 30s", "command not found", "找不到 python", "unsupported language: xx"} {
		if !shouldFallback(ForgeGateResult{OK: false, Error: errText}) {
			t.Errorf("should fallback for %q", errText)
		}
	}
	if shouldFallback(ForgeGateResult{OK: false, Error: "syntax error"}) {
		t.Error("compile error should NOT fallback")
	}
}

func TestPickFallback(t *testing.T) {
	// 四期 I: deno/tcc/rust 已裁剪, 不再有 fallback 分支
	cases := map[string]string{
		"sh": "python", "bash": "python",
		"python": "node", "node": "python", "js": "python",
		"go": "", "deno": "", "ts": "", "tcc": "", "c": "", "rust": "",
	}
	for in, want := range cases {
		if got := pickFallback(in); got != want {
			t.Errorf("pickFallback(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── 小工具函数 ─────────────────────────────────────────────

func TestTruncateMsg(t *testing.T) {
	short := "hello"
	if truncateMsg(short) != short {
		t.Error("short message should pass through")
	}
	long := strings.Repeat("x", 400)
	got := truncateMsg(long)
	if len(got) != 303 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncateMsg len=%d", len(got))
	}
}

func TestAtoi(t *testing.T) {
	if atoi("12") != 12 || atoi("") != 0 || atoi("abc") != 0 || atoi("007") != 7 {
		t.Error("atoi wrong")
	}
}

func TestValidGateJSON(t *testing.T) {
	if !validGateJSON(`{"a":1}`) {
		t.Error("valid JSON rejected")
	}
	if !validGateJSON(`  {"a":1}  `) {
		t.Error("valid JSON with spaces rejected")
	}
	if validGateJSON(`{bad`) {
		t.Error("invalid JSON accepted")
	}
	if validGateJSON(`plain text`) {
		t.Error("non-JSON accepted")
	}
}

func TestForgeSplitCommand(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"python code.py", []string{"python", "code.py"}},
		{`echo "hello world"`, []string{"echo", "hello world"}},
		{`echo 'a b'`, []string{"echo", "a b"}},
		{"a  b   c", []string{"a", "b", "c"}},
		{`x"y z"w`, []string{"xy zw"}}, // 引号被吞, 内部空格保留
		{"", nil},
	}
	for _, c := range cases {
		got := forgeSplitCommand(c.in)
		if len(got) != len(c.want) {
			t.Errorf("split(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("split(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestSummarizeOutput(t *testing.T) {
	if s := summarizeOutput("", ""); s != "" {
		t.Errorf("empty summary = %q", s)
	}
	short := "a\nb"
	if s := summarizeOutput(short, ""); !strings.Contains(s, "a") || !strings.Contains(s, "b") {
		t.Errorf("short summary = %q", s)
	}
	// 8 行 → 前后各 3 + 省略标记
	long := ""
	for i := 1; i <= 8; i++ {
		long += "line" + string(rune('0'+i)) + "\n"
	}
	s := summarizeOutput(long, "")
	if !strings.Contains(s, "(8 lines total)") || !strings.Contains(s, "line1") || !strings.Contains(s, "line8") {
		t.Errorf("long summary = %q", s)
	}
	// stderr > 200 截断
	bigErr := strings.Repeat("e", 300)
	s2 := summarizeOutput("", bigErr)
	if len(s2) > 220 {
		t.Errorf("stderr not truncated: %d", len(s2))
	}
}

func TestTruncateOutput(t *testing.T) {
	short := "hello"
	if truncateOutput(short) != short {
		t.Error("short should pass through")
	}
	big := strings.Repeat("中", MaxForgeOutputLength+100)
	got := truncateOutput(big)
	if !strings.Contains(got, "[truncated]") {
		t.Error("no truncation marker")
	}
	if len(got) >= len(big) {
		t.Error("not actually truncated")
	}
	// 中文按 rune 计数, 头尾保留
	if !strings.HasPrefix(got, "中") || !strings.HasSuffix(got, "中") {
		t.Error("head/tail lost")
	}
}

func TestForgeDetectLang(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"package main\nfunc main() {}", "go"},
		{"#!/bin/bash\necho hi", "sh"},
		{"#!/usr/bin/env python3\nprint(1)", "python"},
		{"import math\nprint(math.pi)", "python"},
		{"console.log('x')", "node"},
		{"const a = 1;", "node"},
		// 四期 I: tcc/deno 已裁剪, C/TS 代码无特征 → 默认 python
		{"#include <stdio.h>\nint main() { return 0; }", "python"},
		{"let x: string = \"a\";", "python"},
		{"def f():\n    return 1", "python"},
		{"random text here", "python"}, // 默认 fallback
	}
	for _, c := range cases {
		if got := forgeDetectLang(c.code, ""); got != c.want {
			t.Errorf("detect(%q) = %q, want %q", c.code[:tmin(30, len(c.code))], got, c.want)
		}
	}
}

func tmin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
