package main

import (
	"strings"
	"testing"
)

// 复现+回归: wrapText 应把 ANSI 色码当 0 宽度, 折行按可见宽度。
func TestWrapTextSkipsAnsi(t *testing.T) {
	esc := "\033[38;2;255;0;0m"
	prompt := esc + "铸" + "\033[0m" + esc + "剑" + "\033[0m" + esc + "炉" + "\033[0m" + " » "
	s := prompt + strings.Repeat("a", 40)
	vis := displayWidth(s)
	lines := wrapText(s, 80)
	if len(lines) != 1 {
		t.Fatalf("wrapText 未跳过ANSI色码: 可见宽=%d(<80应1行), 实际 %d 行", vis, len(lines))
	}
}

// 长输入多行折行: 每行可见宽度 <= termW, 且各段可见宽合计=总可见宽(色码不虚增).
func TestWrapTextLongMultiLine(t *testing.T) {
	esc := "\033[38;2;1;2;3m"
	prompt := esc + "铸剑炉" + "\033[0m" + " » "
	// 90 个宽字符(漢, 宽2) + 40 个半角 a => 可见宽 90*2+40=220
	long := strings.Repeat("漢", 90) + strings.Repeat("a", 40)
	s := prompt + long
	w := 80
	lines := wrapText(s, w)
	if len(lines) < 2 {
		t.Fatalf("220 列应折多行: 得 %d 行", len(lines))
	}
	sum := 0
	for i, l := range lines {
		dw := displayWidth(l)
		if dw > w {
			t.Fatalf("行 %d 可见宽 %d 超过 termW %d", i, dw, w)
		}
		sum += dw
	}
	if sum != displayWidth(s) {
		t.Fatalf("可见宽合计 %d != 总可见 %d", sum, displayWidth(s))
	}
}
