// main_utils_test.go: 主程序杂项工具函数的行为契约。
//
// 重点:
//   - isPeakHourAt 用固定时间测, 不依赖"现在几点"(否则测试结果随运行时刻漂移)
//   - 历史文件往返必须无损(多行输入存一行)
//   - 跨平台: 只引用两平台共有的符号, 否则 Windows 能过、Linux 崩
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMainUtils_DisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 4},
		{"a中", 3},
		{"\x1b[31mab\x1b[0m", 2}, // ANSI 不占显示宽度
		{"⚡", 2},
		{"·", 1},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d 期望 %d", c.in, got, c.want)
		}
	}
}

func TestMainUtils_FitWidth(t *testing.T) {
	// 未超宽: 原样返回(含 ANSI)
	if got := fitWidth("abc", 10); got != "abc" {
		t.Errorf("未超宽应原样返回, 实际 %q", got)
	}
	if got := fitWidth("中文", 5); got != "中文" {
		t.Errorf("未超宽应原样返回, 实际 %q", got)
	}
	// 超宽: 截断 + 省略号, 显示宽度不得超 maxW
	exact := []struct {
		in   string
		maxW int
		want string
	}{
		{"abcdef", 3, "ab…"},
		{"中文中文", 5, "中文…"},
	}
	for _, c := range exact {
		got := fitWidth(c.in, c.maxW)
		if got != c.want {
			t.Errorf("fitWidth(%q, %d) = %q 期望 %q", c.in, c.maxW, got, c.want)
		}
	}
	// 性质: 任何输入截断后宽度 <= maxW
	inputs := []string{
		"abcdefghij", "中文中文中文", "\x1b[31mabcdef\x1b[0m",
		"a中b文c", "⚡⚡⚡", strings.Repeat("x", 100),
	}
	for _, in := range inputs {
		for maxW := 1; maxW <= 12; maxW++ {
			got := fitWidth(in, maxW)
			if w := displayWidth(got); w > maxW {
				t.Errorf("fitWidth(%q, %d) = %q 宽度 %d 超限", in, maxW, got, w)
			}
		}
	}
	// 超宽输入必须带省略号; ANSI 前缀须保留
	got := fitWidth("\x1b[31mabcdef\x1b[0m", 4)
	if !strings.Contains(got, "…") {
		t.Errorf("超宽输入应带省略号: %q", got)
	}
	if !strings.HasPrefix(got, "\x1b[31m") {
		t.Errorf("ANSI 前缀应保留: %q", got)
	}
}

func TestMainUtils_ClampF(t *testing.T) {
	cases := []struct{ v, lo, hi, want float64 }{
		{5, 0, 10, 5},
		{-1, 0, 10, 0},
		{11, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
		{3.5, 1.0, 2.0, 2.0},
	}
	for _, c := range cases {
		if got := clampF(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clampF(%v,%v,%v) = %v 期望 %v", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestMainUtils_IsPeakHourAt(t *testing.T) {
	monday := time.Date(2026, 1, 5, 0, 0, 0, 0, time.Local)
	saturday := time.Date(2026, 1, 10, 0, 0, 0, 0, time.Local)
	sunday := time.Date(2026, 1, 11, 0, 0, 0, 0, time.Local)
	// 前提: 日期常量必须真的是那几天, 否则下面的断言静默失效
	if monday.Weekday() != time.Monday {
		t.Fatalf("日期常量失效: 2026-01-05 是 %v 不是周一", monday.Weekday())
	}
	if saturday.Weekday() != time.Saturday || sunday.Weekday() != time.Sunday {
		t.Fatalf("日期常量失效: 2026-01-10/11 不是周六/周日")
	}
	at := func(base time.Time, h, m int) time.Time {
		return time.Date(base.Year(), base.Month(), base.Day(), h, m, 0, 0, time.Local)
	}
	cases := []struct {
		name string
		tm   time.Time
		want bool
	}{
		{"周一 08:59", at(monday, 8, 59), false},
		{"周一 09:00", at(monday, 9, 0), true},
		{"周一 11:59", at(monday, 11, 59), true},
		{"周一 12:00 (右开)", at(monday, 12, 0), false},
		{"周一 13:59", at(monday, 13, 59), false},
		{"周一 14:00", at(monday, 14, 0), true},
		{"周一 17:59", at(monday, 17, 59), true},
		{"周一 18:00 (右开)", at(monday, 18, 0), false},
		{"周六 10:00", at(saturday, 10, 0), false},
		{"周日 15:00", at(sunday, 15, 0), false},
	}
	for _, c := range cases {
		if got := isPeakHourAt(c.tm); got != c.want {
			t.Errorf("[%s] isPeakHourAt = %v 期望 %v", c.name, got, c.want)
		}
	}
}

func TestMainUtils_EscapeRoundTrip(t *testing.T) {
	cases := []string{"", "single", "a\nb", "a\nb\nc", "中文\n换行", "tab\there"}
	for _, s := range cases {
		esc := escapeHistory(s)
		if strings.Contains(esc, "\n") {
			t.Errorf("escapeHistory(%q) 仍含真实换行: %q", s, esc)
		}
		if got := unescapeHistory(esc); got != s {
			t.Errorf("往返不等: %q -> %q -> %q", s, esc, got)
		}
	}
}

func TestMainUtils_IsPathLike(t *testing.T) {
	yes := []string{"C:\\Users\\x", "D:", "C:\\", "z:\\tmp"}
	no := []string{"", "abc", "1:x", ":x", "a/b"}
	for _, s := range yes {
		if !isPathLike(s) {
			t.Errorf("应判路径: %q", s)
		}
	}
	for _, s := range no {
		if isPathLike(s) {
			t.Errorf("不应判路径: %q", s)
		}
	}
}

func TestMainUtils_UnbalancedDelimiters(t *testing.T) {
	unbalanced := []string{"(", "(a", "([{}]", "a(", "\"abc", "'x", "`code", "{(a)"}
	balanced := []string{"", "a", "(a)", "([{}])", "\"a\"", "f(x) + g(y)", ")", "()"}
	for _, s := range unbalanced {
		if !unbalancedDelimiters(s) {
			t.Errorf("应判未闭合: %q", s)
		}
	}
	for _, s := range balanced {
		if unbalancedDelimiters(s) {
			t.Errorf("应判已闭合: %q", s)
		}
	}
}

func TestMainUtils_HistoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.txt")

	// 不存在的文件 → nil, 不 panic
	if got := readHistory(path, 10); got != nil {
		t.Errorf("缺失文件应返回 nil, 实际 %v", got)
	}

	appendHistory(path, "first")
	appendHistory(path, "多行\n输入")
	appendHistory(path, "third")

	got := readHistory(path, 10)
	want := []string{"first", "多行\n输入", "third"}
	if len(got) != len(want) {
		t.Fatalf("读回 %d 行 期望 %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 行 = %q 期望 %q", i, got[i], want[i])
		}
	}
	// maxLines 只保留最后 N 行
	tail := readHistory(path, 2)
	if len(tail) != 2 || tail[0] != "多行\n输入" || tail[1] != "third" {
		t.Errorf("maxLines=2 结果不符: %v", tail)
	}
}

func TestMainUtils_PrintHelpNoPanic(t *testing.T) {
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "help*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Stdout = f
	defer func() { os.Stdout = old }()

	printHelp()

	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Error("printHelp 未输出任何内容")
	}
}

func TestMainUtils_EnableWindowsUTF8DefinedOnAllPlatforms(t *testing.T) {
	// 该函数在 _windows 与 _other 各有一份; 任一侧被删都会让跨平台编译失败,
	// 这里用源码级断言提前钉住(而不是等 Linux 构建才暴露)。
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main_utils_windows.go", "main_utils_other.go"} {
		src, err := os.ReadFile(filepath.Join(wd, name))
		if err != nil {
			t.Errorf("读 %s 失败: %v", name, err)
			continue
		}
		if !strings.Contains(string(src), "func enableWindowsUTF8()") {
			t.Errorf("%s 缺 func enableWindowsUTF8() —— 跨平台缺口", name)
		}
	}
}
