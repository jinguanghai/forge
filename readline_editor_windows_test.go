//go:build windows

package main

import (
	"bytes"
	"io"
	"testing"
)

// mkTestEditor 构造一个输出被捕获的编辑器。
// insertRune/redraw 会写终端：不注入 io.Discard 的话回显会直接写进程 stdout，
// 把 go test -v 的 "--- PASS/FAIL: TestX" 行首污染成 "abcd--- PASS: ..."，
// 任何按行首统计测试结果的工具都会漏计（含漏报 FAIL），属静默失败。
func mkTestEditor(prompt string) *lineEditor {
	ed := newLineEditor(prompt)
	ed.out = io.Discard
	return ed
}

func TestEditorWrapTextHardSplit(t *testing.T) {
	// wrapText 按【显示宽度】硬切，不在词边界断开（与 shell 的 word wrap 不同）。
	// "hello world" 宽 11，限宽 5 → 5/5/1 三行，第二行带前导空格。
	got := wrapText("hello world", 5)
	want := []string{"hello", " worl", "d"}
	if len(got) != len(want) {
		t.Fatalf("wrapText 行数 = %d, 期望 %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("wrapText[%d] = %q, 期望 %q", i, got[i], want[i])
		}
	}
}

func TestEditorWrapTextWideRunes(t *testing.T) {
	// 汉字显示宽度 2：限宽 4 → 每行恰 2 个汉字（若按 rune 计数会变成每行 4 个，
	// 导致重绘时块行数算错、光标定位漂移）。
	got := wrapText("中文测试", 4)
	want := []string{"中文", "测试"}
	if len(got) != len(want) {
		t.Fatalf("wrapText 行数 = %d, 期望 %d (%q)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("wrapText[%d] = %q, 期望 %q", i, got[i], want[i])
		}
	}
}

func TestEditorWrapTextEmptyAndShort(t *testing.T) {
	if got := wrapText("", 10); len(got) != 1 || got[0] != "" {
		t.Errorf("wrapText(\"\",10) = %q, 期望 [\"\"]", got)
	}
	if got := wrapText("abc", 10); len(got) != 1 || got[0] != "abc" {
		t.Errorf("wrapText(\"abc\",10) = %q, 期望 [\"abc\"]", got)
	}
}

func TestEditorCursorBlockPosNoWrap(t *testing.T) {
	// prompt "> " 宽 2，buf "abc" 3 列 → 同行第 5 列
	row, col := cursorBlockPos("> ", 80, []rune("abc"), 3)
	if row != 0 || col != 5 {
		t.Errorf("cursorBlockPos = (%d,%d), 期望 (0,5)", row, col)
	}
}

func TestEditorCursorBlockPosWraps(t *testing.T) {
	// termW=20 减 promptW=2 → 首行 18 列；25 个 rune → 第二行第 7 列
	row, col := cursorBlockPos("> ", 20, []rune("aaaaabbbbbcccccdddddeeeee"), 25)
	if row != 1 || col != 7 {
		t.Errorf("cursorBlockPos = (%d,%d), 期望 (1,7)", row, col)
	}
}

func TestEditorWordNavigation(t *testing.T) {
	b := []rune("hello world")
	cases := []struct {
		fn   func([]rune, int) int
		cur  int
		want int
	}{
		{prevWord, 11, 6}, // 从末尾退到 "world" 首
		{prevWord, 5, 0},  // 从词中退到 "hello" 首
		{prevWord, 0, 0},  // 已在首，不得越界
		{nextWord, 0, 6},  // 跳到 "world" 首
		{nextWord, 5, 6},
		{nextWord, 11, 11}, // 已在尾，不得越界
	}
	for i, c := range cases {
		if got := c.fn(b, c.cur); got != c.want {
			t.Errorf("case %d: cur=%d got=%d, 期望 %d", i, c.cur, got, c.want)
		}
	}
}

func TestEditorInsertRuneAdvancesCursor(t *testing.T) {
	ed := mkTestEditor("> ")
	insertRune(ed, 'a')
	insertRune(ed, 'b')
	if string(ed.buf) != "ab" || ed.cursor != 2 {
		t.Errorf("insertRune 后 buf=%q cursor=%d, 期望 \"ab\"/2", string(ed.buf), ed.cursor)
	}
	// 光标移到中间后插入：应插入到光标处而非追加到末尾
	ed.cursor = 1
	insertRune(ed, 'X')
	if string(ed.buf) != "aXb" || ed.cursor != 2 {
		t.Errorf("中间插入后 buf=%q cursor=%d, 期望 \"aXb\"/2", string(ed.buf), ed.cursor)
	}
}

func TestEditorInsertSilent(t *testing.T) {
	ed := mkTestEditor("> ")
	insertSilent(ed, 'a')
	insertSilent(ed, 'b')
	if string(ed.buf) != "ab" || ed.cursor != 2 {
		t.Errorf("insertSilent 后 buf=%q cursor=%d, 期望 \"ab\"/2", string(ed.buf), ed.cursor)
	}
}

func TestEditorTabComplete(t *testing.T) {
	// 已知命令前缀 → 补全（含尾随空格，便于继续输入参数）
	if got := tabComplete("/he"); got != "/health " {
		t.Errorf("tabComplete(\"/he\") = %q, 期望 \"/health \"", got)
	}
	// 无匹配 → 返回空串（调用方据此决定是否插入字面 TAB）
	if got := tabComplete("xy"); got != "" {
		t.Errorf("tabComplete(\"xy\") = %q, 期望空串", got)
	}
	if got := tabComplete(""); got != "" {
		t.Errorf("tabComplete(\"\") = %q, 期望空串", got)
	}
}

func TestEditorNewLineEditorDefaults(t *testing.T) {
	ed := mkTestEditor("> ")
	if ed.promptW != 2 {
		t.Errorf("promptW = %d, 期望 2", ed.promptW)
	}
	if ed.termW < 20 {
		t.Errorf("termW = %d, 期望 >= 20（过窄会退化为 80）", ed.termW)
	}
	if ed.lastLines != 1 {
		t.Errorf("lastLines = %d, 期望 1", ed.lastLines)
	}
	// bottomBarRow = -1 表示"未约束"（非 TTY/自定义输出），redraw 不得触碰状态栏行
	if ed.bottomBarRow != -1 {
		t.Errorf("bottomBarRow = %d, 期望 -1", ed.bottomBarRow)
	}
}

func TestEditorRedrawDoesNotPanic(t *testing.T) {
	// redraw 依赖控制台 API；非 TTY 下 consoleProcsOK=false 应走降级路径而非 panic。
	var buf bytes.Buffer
	ed := newLineEditor("> ")
	ed.out = &buf
	ed.buf = []rune("hello")
	ed.cursor = 5
	ed.redraw()
	ed.buf = []rune("aaaaabbbbbcccccdddddeeeeefffff")
	ed.cursor = len(ed.buf)
	ed.redraw()
}

// tabComplete: 非斜杠 / 前缀补全 / 完整名不再补 / 参数模式 / 无匹配
func TestTabComplete_Branches(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"非斜杠开头不补全", "hello", ""},
		{"空行不补全", "", ""},
		{"命令前缀补全", "/he", "/health "},
		{"命令前缀补全二", "/hel", "/help "},
		{"无匹配前缀", "/zzzz", ""},
		{"完整命令名不再补全", "/help", ""},
		{"完整命令名(带参数型)不再补全", "/goal", ""},
		{"参数模式空前缀补首个参数", "/goal ", "/goal list "},
		{"参数模式前缀匹配", "/goal l", "/goal list "},
		{"参数模式无匹配", "/goal zzz", ""},
		{"主题参数补全", "/theme ", "/theme neon "},
		{"无参数命令空格后不补", "/help ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tabComplete(c.in); got != c.want {
				t.Errorf("tabComplete(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
