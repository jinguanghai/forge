package main

// cov_render_more_test.go — 渲染层分支补测 (ux.go 高亮/流式/Markdown)
// 不变量: 高亮只插 ANSI 转义, 不改动原始字符 → 去 ANSI 后必须逐字节等于输入。

import (
	"regexp"
	"strings"
	"testing"
)

var reAnsiStrip = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func stripAnsi(s string) string { return reAnsiStrip.ReplaceAllString(s, "") }

func TestCovRender_HighlightLineLangs(t *testing.T) {
	cases := []struct{ line, lang string }{
		{"func main() { return 1 }", "go"},
		{"package main", "go"},
		{"import \"fmt\"", "go"},
		{"def f(x): return x", "python"},
		{"print('hi') # comment", "python"},
		{"const a = 1; // note", "node"},
		{"echo hello | grep x", "sh"},
		{"#!/bin/bash", "bash"},
		{"x = 1 + 2", "math"},
		{"// 中文注释", ""},
		{"SELECT * FROM t", "sql"},
		{"", "go"},
	}
	for _, c := range cases {
		if got := stripAnsi(highlightLine(c.line, c.lang)); got != c.line {
			t.Errorf("highlightLine(%q,%q) 去 ANSI = %q, 期望原样", c.line, c.lang, got)
		}
	}
}

func TestCovRender_NormalizeLang(t *testing.T) {
	for _, in := range []string{"", "go", "golang", "py", "python", "js", "javascript", "node",
		"sh", "bash", "shell", "math", "logic", "regex", "chain", "knowledge", "unknown-lang"} {
		if got := normalizeLang(in); got == "" && in != "" {
			t.Errorf("normalizeLang(%q) 为空", in)
		}
	}
}

func TestCovRender_MarkdownLines(t *testing.T) {
	lines := []string{"**粗体** 文本", "__下划线__", "`code`", "- 列表项", "1. 有序项",
		"> 引用", "# 标题", "## 二级", "普通文本", "```go", "x := 1", "[链接](http://a.com)", ""}
	for _, l := range lines {
		if out := renderMarkdownLine(l); out == "" && l != "" {
			t.Errorf("renderMarkdownLine(%q) 返回空", l)
		}
	}
}

func TestCovRender_StreamRendererFeed(t *testing.T) {
	r := newStreamRenderer()
	var sb strings.Builder
	chunks := []string{"普通文本", "续行\n", "```go\n", "func main() {}\n", "```\n", "结束", "\n", ""}
	for _, c := range chunks {
		sb.WriteString(r.feed(c))
	}
	sb.WriteString(r.flush())
	out := stripAnsi(sb.String())
	for _, want := range []string{"普通文本", "func main() {}", "结束"} {
		if !strings.Contains(out, want) {
			t.Errorf("流式输出缺少 %q; got=%q", want, out)
		}
	}
}

func TestCovRender_ReasoningFeed(t *testing.T) {
	r := &reasoningRenderer{}
	var sb strings.Builder
	for _, c := range []string{"思考中", "\n", "第二步\n", "结论\n"} {
		sb.WriteString(r.feed(c))
	}
	out := stripAnsi(sb.String())
	if !strings.Contains(out, "结论") {
		t.Errorf("推理流输出缺少内容: %q", out)
	}
}
