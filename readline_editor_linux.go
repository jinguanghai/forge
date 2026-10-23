//go:build linux

package main

import "fmt"

// ---- Linux 重绘: ANSI 相对移动模型的平台层(薄封装) ----
//
// 序列怎么生成在 readline_render.go(跨平台纯函数, 有模拟器判据); 这里只负责
// 取宽度、写出去、同步块状态。

// redraw 折行感知重绘 (Linux/ANSI 版)。
//
// 与 Windows 版的差异: 终端没有「缓冲区绝对行号」, 无法 setCursorPos(0, row)
// 绝对定位, 故改用相对移动(见 redrawSequence 的模型说明)。
//
// 块位置为何可靠: 重绘期间没有异步输出(readLine 独占终端, 状态栏在 Linux 侧
// 不绘制), 所有光标移动都由本函数自己发出, 故相对行号不会漂移。
func (ed *lineEditor) redraw() {
	tw := ed.termWidth()
	if tw < 20 {
		tw = 80
	}
	ed.termW = tw
	seq, n, row := redrawSequence(ed.prompt, tw, ed.buf, ed.cursor, ed.lastLines, ed.cursorRow)
	fmt.Fprint(ed.w(), seq)
	ed.lastLines, ed.cursorRow = n, row
}

// commitCursorPlacement 提交前把光标移到编辑块底行行首 (Linux 相对移动版)。
func commitCursorPlacement(ed *lineEditor) {
	seq, row := commitSequence(ed.lastLines, ed.cursorRow)
	fmt.Fprint(ed.w(), seq)
	ed.cursorRow = row
}
