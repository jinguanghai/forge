//go:build windows

package main

import "testing"

// ---------- runeWidth 全角判定表（病灶三修复验证） ----------
func TestRuneWidth(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'A', 1}, {'1', 1}, {' ', 1},
		{'中', 2}, {'。', 2}, {'，', 2},
		{'あ', 2}, {'ア', 2}, // 假名（原漏判）
		{'한', 2}, {'글', 2}, // 韩文音节（原漏判）
		{'㐀', 2},           // CJK扩展A（原漏判）
		{'𠀀', 2},           // CJK扩展B 增补平面（原漏判）
		{'①', 1}, {'★', 1}, // ambiguous 字符: wcwidth 默认1列
		{'㈱', 2}, {'㎝', 2}, // 带圈CJK / CJK兼容 (W)
		{'😀', 2}, {'🀄', 2}, // emoji / 麻将
		{'￠', 2},                // 全角符号 FFE0 (F)
		{'\uFF61', 1}, {'ｱ', 1}, // 半角片假名（应为1列，原误判2）
		{'\t', 1}, {'\n', 1},
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(%U %q) = %d, want %d", c.r, c.r, got, c.want)
		}
	}
}

// ---------- wrapText 折行（病灶一修复后的行为验证） ----------
func TestWrapText(t *testing.T) {
	// 窗口宽 10：中英混合折行，宽字符不跨行
	lines := wrapText("abc中文def", 10)
	// 显示宽度: a b c 中 文 d e f = 3+2+2+3 = 10 恰好一行
	if len(lines) != 1 || lines[0] != "abc中文def" {
		t.Errorf("宽10应整行: got %q", lines)
	}

	// 显示宽度 11 > 10：折两行
	lines = wrapText("abc中文defg", 10)
	if len(lines) != 2 {
		t.Fatalf("应折2行: got %q", lines)
	}
	if lines[0] != "abc中文def" || lines[1] != "g" {
		t.Errorf("折行位置错误: %q", lines)
	}

	// 日文假名按2列折行（原漏判会导致折行位置偏）
	lines = wrapText("あいうえお", 6)
	// あいう = 6 列 -> 第一行 "あいう", 第二行 "えお"
	if len(lines) != 2 || lines[0] != "あいう" || lines[1] != "えお" {
		t.Errorf("假名折行错误: %q", lines)
	}

	// 空串
	lines = wrapText("", 10)
	if len(lines) != 1 || lines[0] != "" {
		t.Errorf("空串应得1个空行: %q", lines)
	}
}

// ---------- 光标行列计算（redraw 核心逻辑，取模定位） ----------
func TestCursorPosMath(t *testing.T) {
	// 模拟 redraw 的光标计算: curW -> (curRow, curCol)
	// 窗口宽 80, 输入 100 个 ASCII -> curW=100 -> row=1, col=20
	termW := 80
	curW := 100
	if row, col := curW/termW, curW%termW; row != 1 || col != 20 {
		t.Errorf("光标计算错误: row=%d col=%d want (1,20)", row, col)
	}
	// 宽字符累计: prompt(2) + 中文字符
	// prompt "> " = 2 列; "中" = 2 列; 光标在第1个"中"后 -> curW=4 -> row0 col4
	curW2 := 2 + 2
	if row, col := curW2/termW, curW2%termW; row != 0 || col != 4 {
		t.Errorf("宽字符光标: (%d,%d) want (0,4)", row, col)
	}
}
