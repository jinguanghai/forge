package main

import "testing"

// status_bar_reserve_test.go —— 「输出溢出到输入端」修复的哨兵。
//
// 缺陷现场 (2026-10-01 截屏): 输出撑满窗口后, 输出末行落在输入行(bottom-1)上,
// readLine 的 prompt 直接覆盖其开头 —— 截屏里同一行出现
// 「炉·R » 给出的"像真的"结果比没有判据更危险…」(prompt + 输出末行)。
//
// 判据两层: ①换行数计算正确 ②不变量「腾行后内容末行 <= bottom-2」恒成立
// (bottom-1 = 输入行, bottom = 状态栏行 —— 被内容占用即缺陷复现)。

func TestInputReserveNewlines(t *testing.T) {
	cases := []struct {
		name                     string
		curX, curY, bottom, want int
	}{
		{"光标在 bottom-2 行首(输入行本就空)", 0, 17, 19, 0},
		{"光标在输入行行首(bottom-1)", 0, 18, 19, 0},
		{"光标在输入行行中(bottom-1)", 5, 18, 19, 2},
		{"光标在状态栏行行首(bottom)", 0, 19, 19, 1},
		{"光标在状态栏行行中(bottom)", 5, 19, 19, 2},
		{"光标远在窗口上方", 0, 3, 19, 0},
		{"光标在窗口外(用户滚动过窗口)", 0, 40, 19, 0},
		{"宽窗口同构", 5, 49, 50, 2},
	}
	for _, c := range cases {
		if got := inputReserveNewlines(c.curX, c.curY, c.bottom); got != c.want {
			t.Errorf("%s: inputReserveNewlines(%d,%d,%d)=%d want %d",
				c.name, c.curX, c.curY, c.bottom, got, c.want)
		}
	}
}

// TestInputReserveNewlines_Invariant 不变量: 任何位置组合下, 腾行后的内容末行
// 都不得落在 bottom-1/bottom 两行(输入行/状态栏行)上。
func TestInputReserveNewlines_Invariant(t *testing.T) {
	for bottom := 5; bottom <= 60; bottom++ {
		for curY := bottom - 6; curY <= bottom+2; curY++ {
			for _, curX := range []int{0, 1, 40} {
				if curY > bottom {
					continue // 该分支按定义返回 0, 由上面用例钉住
				}
				n := inputReserveNewlines(curX, curY, bottom)
				last := curY
				if curX == 0 {
					last = curY - 1
				}
				rolled := n - (bottom - curY)
				if rolled < 0 {
					rolled = 0
				}
				if got := last - rolled; got > bottom-2 {
					t.Errorf("curX=%d curY=%d bottom=%d: n=%d 上滚%d行后内容末行=%d 仍占用输入行/状态栏行(需 <= %d)",
						curX, curY, bottom, n, rolled, got, bottom-2)
				}
			}
		}
	}
}

// TestReserveInputRow_NoConsole 控制台不可用(测试进程 stdout 为管道)时必须
// 原样返回、不 panic、不写任何换行 —— 非 TTY 下不得污染输出流。
func TestReserveInputRow_NoConsole(t *testing.T) {
	old := consoleProcsOK
	consoleProcsOK = false
	defer func() { consoleProcsOK = old }()
	if got := reserveInputRow(42); got != 42 {
		t.Errorf("控制台不可用时 reserveInputRow(42)=%d, want 42", got)
	}
}
