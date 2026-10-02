// style_test.go: ANSI 样式、尺寸格式化与 spinner 的行为契约。
package main

import (
	"os"
	"strings"
	"testing"
)

// withStderrCapture 把 stderr 重定向到临时文件, 返回读取函数。
// 被测函数(displayToolCode / spinner)直接写 os.Stderr, 不重定向会污染测试输出。
func withStderrCapture(t *testing.T) func() string {
	t.Helper()
	old := os.Stderr
	f, err := os.CreateTemp(t.TempDir(), "stderr*.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = old
		_ = f.Close()
	})
	return func() string {
		_ = f.Sync()
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatalf("读 stderr 捕获文件失败: %v", err)
		}
		return string(b)
	}
}

func TestStyle_FormatSize(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0B"},
		{1, "1B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1048575, "1024.0KB"},
		{1048576, "1.0MB"},
		{1572864, "1.5MB"},
	}
	for _, c := range cases {
		if got := formatSize(c.in); got != c.want {
			t.Errorf("formatSize(%d) = %q 期望 %q", c.in, got, c.want)
		}
	}
}

func TestStyle_AnsiConstants(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"reset", ansi.reset, "\033[0m"},
		{"bold", ansi.bold, "\033[1m"},
		{"dim", ansi.dim, "\033[2m"},
		{"red", ansi.red, "\033[31m"},
		{"green", ansi.green, "\033[32m"},
		{"yellow", ansi.yellow, "\033[33m"},
		{"blue", ansi.blue, "\033[34m"},
		{"magenta", ansi.magenta, "\033[35m"},
		{"cyan", ansi.cyan, "\033[36m"},
		{"white", ansi.white, "\033[37m"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("ansi.%s = %q 期望 %q", c.name, c.got, c.want)
		}
	}
}

func TestStyle_ColorHelpers(t *testing.T) {
	if got := color(ansi.red, "x"); got != ansi.red+"x"+ansi.reset {
		t.Errorf("color = %q", got)
	}
	if got := bold("x"); got != ansi.bold+"x"+ansi.reset {
		t.Errorf("bold = %q", got)
	}
	if got := dim("x"); got != ansi.dim+"x"+ansi.reset {
		t.Errorf("dim = %q", got)
	}
}

func TestStyle_ToolCodeHeader(t *testing.T) {
	got := toolCodeHeader("go")
	if !strings.Contains(got, "GO") {
		t.Errorf("语言标签应大写: %q", got)
	}
	if !strings.Contains(got, langEmoji("go")) {
		t.Errorf("应含语言图标: %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("头部应以换行结束: %q", got)
	}
	// 空语言 → CODE 标签
	empty := toolCodeHeader("")
	if !strings.Contains(empty, "CODE") {
		t.Errorf("空语言应用 CODE 标签: %q", empty)
	}
	// 同一语言两次调用逐字节相同(流式与整块兜底共用, 不得漂移)
	if again := toolCodeHeader("go"); again != got {
		t.Errorf("头部不确定: %q vs %q", got, again)
	}
}

func TestStyle_DisplayToolCode(t *testing.T) {
	get := withStderrCapture(t)
	displayToolCode("x := 1", "go")
	out := get()
	// 注意: 代码行会经语法高亮(数字着品红), 断言前必须剥离 ANSI
	if !strings.Contains(stripANSI(out), "x := 1") {
		t.Errorf("未输出代码: %q", out)
	}
	if !strings.Contains(out, "GO") {
		t.Errorf("未输出语言头部: %q", out)
	}
}

func TestStyle_DisplayToolCodeTruncation(t *testing.T) {
	t.Setenv("FORGE_CODE_MAX_LINES", "2")
	get := withStderrCapture(t)
	displayToolCode("l1\nl2\nl3\nl4", "python")
	out := get()
	if !strings.Contains(out, "l2") {
		t.Errorf("应显示前 2 行: %q", out)
	}
	if strings.Contains(out, "l3") {
		t.Errorf("超过上限的行不应显示: %q", out)
	}
	if !strings.Contains(out, "more lines") {
		t.Errorf("截断应给出提示: %q", out)
	}
}

func TestStyle_DisplayToolCodeFullByDefault(t *testing.T) {
	t.Setenv("FORGE_CODE_MAX_LINES", "0")
	get := withStderrCapture(t)
	displayToolCode("l1\nl2\nl3", "go")
	out := get()
	for _, want := range []string{"l1", "l2", "l3"} {
		if !strings.Contains(out, want) {
			t.Errorf("上限 0 应全量显示, 缺 %q: %q", want, out)
		}
	}
	if strings.Contains(out, "more lines") {
		t.Errorf("全量模式不应报截断: %q", out)
	}
}

func TestStyle_SpinnerStopIdempotent(t *testing.T) {
	get := withStderrCapture(t)
	s := startSpinner("处理中")
	if s == nil {
		t.Fatal("startSpinner 返回 nil")
	}
	s.stop()
	s.stop() // 第二次必须立即返回(不得 panic: close of closed channel)
	if out := get(); out == "" {
		t.Error("spinner 未向 stderr 写任何内容")
	}
}

func TestStyle_SpinnerFramesNonEmpty(t *testing.T) {
	if len(spinnerFrames) == 0 {
		t.Fatal("spinnerFrames 为空 —— startSpinner 会 panic(除零)")
	}
	for i, f := range spinnerFrames {
		if f == "" {
			t.Errorf("第 %d 帧为空", i)
		}
	}
}
