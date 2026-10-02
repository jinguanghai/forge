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
	// 类型化判据: 只有产生点打了 EnvFailure 才算环境性失败。
	if !shouldFallback(ForgeGateResult{OK: false, EnvFailure: true, Error: "gate 二进制缺失"}) {
		t.Error("EnvFailure 应触发 fallback")
	}
	if shouldFallback(ForgeGateResult{OK: false, Timeout: true, EnvFailure: true}) {
		t.Error("超时不得换语言(重跑只会再烧一个超时周期)")
	}
	if shouldFallback(ForgeGateResult{OK: false, Error: "syntax error"}) {
		t.Error("compile error should NOT fallback")
	}
	// 缺陷回归哨兵(20260927): 错误文本含环境性关键词但类型未打标 → 不得换语言。
	// 旧实现用 strings.Contains 嗅探文本, 用户代码/脚本报错含这些词即被误判为
	// 环境失败, 触发无意义的换语言重跑(烧掉一个完整超时周期)。
	for _, errText := range []string{
		"timeout after 30s",
		"command not found",
		"FileNotFoundError: [Errno 2] No such file or directory",
		"bash: cat: command not found",
		"找不到 python",
		"不支持的语言: xx",
		"connection reset by peer",
	} {
		if shouldFallback(ForgeGateResult{OK: false, Error: errText}) {
			t.Errorf("文本嗅探不得复辟: %q 不应触发 fallback", errText)
		}
	}
}

func TestPickFallback(t *testing.T) {
	// 四期 I: deno/tcc/rust 已裁剪, 不再有 fallback 分支
	cases := map[string]string{
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

func TestSummarizeOutput(t *testing.T) {
	// 全空 → 全空 (P1-2: stderr 空不打印标签)
	if s := summarizeOutput("", ""); s != "" {
		t.Errorf("empty summary = %q", s)
	}
	// stderr 空时不应出现 [stderr] 标签 (向后兼容前缀, 但内容必须不含 stderr 提示)
	short := "a\nb"
	s := summarizeOutput(short, "")
	if !strings.Contains(s, "a") || !strings.Contains(s, "b") {
		t.Errorf("short summary = %q", s)
	}
	if strings.Contains(s, "[stderr]") {
		t.Errorf("stderr 为空时不应含 [stderr] 标签, 得到 %q", s)
	}
	// 9 行(含 Go test 样板) → 过滤样板后 ≤8 行, 全量展示
	long := "=== RUN TestX\n=== PAUSE TestX\n=== CONT  TestX\n"
	long += "line1\nline2\nline3\nline4\nline5\nline6\n"
	s = summarizeOutput(long, "")
	if !strings.Contains(s, "line1") || !strings.Contains(s, "line6") {
		t.Errorf("过滤样板后应含有效内容, 得到 %q", s)
	}
	// 样板行 === RUN/PAUSE/CONT 不应出现在摘要中
	for _, b := range []string{"=== RUN", "=== PAUSE", "=== CONT"} {
		if strings.Contains(s, b) {
			t.Errorf("摘要应已过滤样板行 %q, 得到 %q", b, s)
		}
	}
	// stderr > 200 截断
	bigErr := strings.Repeat("e", 300)
	s2 := summarizeOutput("", bigErr)
	if len(s2) > 220 {
		t.Errorf("stderr not truncated: %d", len(s2))
	}
	// stderr 有内容时直接附 stderr 段 (不再套 [stderr] 标签)
	if !strings.Contains(s2, "eee") {
		t.Errorf("stderr 应有内容, 得到 %q", s2)
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
