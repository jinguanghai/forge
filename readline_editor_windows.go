//go:build windows

package main

import "fmt"

// redraw 折行感知重绘：清掉旧块 -> 重绘新块 -> 绝对定位光标。
// 相对光标移动在折行后会错乱，这里全部用 Windows 控制台 API 绝对定位。
func (ed *lineEditor) redraw() {
	// 每次重绘前刷新终端宽度：响应窗口拖拽/缩放（resize）
	ed.termW = ed.termWidth()
	if ed.termW < 20 {
		ed.termW = 80
	}
	text := string(ed.buf)
	lines := wrapText(ed.prompt+text, ed.termW)
	n := len(lines)
	// 最后一行恰好满宽时终端会再折出一行空行
	actualN := n
	if n > 0 && displayWidth(lines[n-1]) >= ed.termW {
		actualN = n + 1
	}

	oldTop, oldLines := ed.lastTop, ed.lastLines
	maxRow := ed.bottomBarRow - 1 // 状态栏上一行 = 编辑区最大行

	// 推算块顶：
	//  - 有底部状态栏约束：编辑块底部对齐到状态栏上一行(maxRow)，块整体上移，
	//    保证折行/多行粘贴产生的每一行都在可见区(状态栏上方)，避免原逻辑
	//    超出 maxRow 就 break 丢弃导致"两行后看不到输入的字"。
	//    块过高(超出整个窗口)时钳制到屏幕顶(0)，底部超宽部分截断(极端场景)。
	//  - 无状态栏(非TTY/自定义)：块顶跟随当前光标(原逻辑)。
	if ed.bottomBarRow >= 0 {
		ed.lastTop = maxRow - actualN + 1
	} else if pos, ok := getCursorPos(); ok {
		ed.lastTop = int(pos.Y) - ed.cursorRow
	}
	if ed.lastTop < 0 {
		ed.lastTop = 0
	}

	// 清掉旧块占用的行(块顶/块高变化后, 新旧行位集并集全清, 避免残留花屏)
	clearRow := func(r int) {
		if ed.bottomBarRow >= 0 && r > maxRow {
			return
		}
		setCursorPos(0, r)
		fmt.Fprint(ed.w(), "\033[K")
	}
	for i := 0; i < oldLines; i++ {
		clearRow(oldTop + i)
	}
	for i := ed.lastTop; i < oldTop; i++ {
		clearRow(i)
	}
	// 清块底下方空出的行(新块比旧块短时)
	for i := ed.lastTop + actualN; i < oldTop+oldLines; i++ {
		clearRow(i)
	}

	// 重绘新块
	for i := 0; i < actualN; i++ {
		r := ed.lastTop + i
		if ed.bottomBarRow >= 0 && r > maxRow {
			break
		}
		setCursorPos(0, r)
		fmt.Fprint(ed.w(), "\033[K")
		if i < n {
			fmt.Fprint(ed.w(), lines[i])
		}
	}

	// 光标目标位置：与 wrapText 同模型（宽字符行末不取模, 见 cursorBlockPos）
	curRow, curCol := cursorBlockPos(ed.prompt, ed.termW, ed.buf, ed.cursor)

	ed.lastLines = actualN
	ed.cursorRow = curRow
	// 光标不落入状态栏行: 超界时钳制到 status 上一行
	endRow := ed.lastTop + curRow
	if ed.bottomBarRow >= 0 && endRow > ed.bottomBarRow-1 {
		endRow = ed.bottomBarRow - 1
	}
	setCursorPos(curCol, endRow)
}

// commitCursorPlacement 提交前把光标移到编辑块底行行首 (Windows 绝对定位)。
// 多行输入时编辑光标可能在块中间: 先移到块底再换行, 否则后续输出从中间行
// 开始会覆盖块下方内容("看不到自己写了什么")。
func commitCursorPlacement(ed *lineEditor) {
	if consoleProcsOK {
		setCursorPos(0, ed.lastTop+ed.lastLines-1)
	}
}
