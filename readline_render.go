package main

import (
	"fmt"
	"strings"
)

// ---- 重绘序列生成 (跨平台纯函数, T22 第 3 层) ----
//
// Linux 终端没有「缓冲区绝对行号」, 重绘走相对移动模型: 回块顶 -> 逐行清绘 ->
// 从块底上移到目标行。这里把「序列怎么生成」抽成纯函数(无 IO、无终端查询),
// 使模型可在任何平台被测试 —— 测试用 ANSI 模拟器解释序列并校验光标落点,
// 平台层只剩「把序列写出去」这一步。
//
// 模型不变量(由 readline_render_test.go 的模拟器断言):
//
//	序列执行完毕, 光标相对块顶的行号 == 返回的 cursorRow, 列号 == 目标列。
func redrawSequence(prompt string, termW int, buf []rune, cursor, lastLines, cursorRow int) (seq string, newLastLines, newCursorRow int) {
	lines := wrapText(prompt+string(buf), termW)
	n := len(lines)
	curRow, curCol := cursorBlockPos(prompt, termW, buf, cursor)

	var b strings.Builder
	// 回块顶: 当前光标在块内第 cursorRow 行, 先 \r 到列 0, 再上移 cursorRow 行
	b.WriteString("\r")
	if cursorRow > 0 {
		fmt.Fprintf(&b, "\033[%dA", cursorRow)
	}
	// 逐行清 + 绘。
	//
	// 每行(含恰好铺满的末行)都补 \r\n: 真实终端写满最后一列后光标处于
	// 「延迟回绕」(pending wrap) 状态, 显式 \r 会取消它、\n 再下移一行 ——
	// 与「不补 \r\n 让下一个字符触发回绕」占用的行数相同, 但语义确定。
	// 于是无论满宽与否, 绘完 n 行后光标都落在「第 n 行行首」。
	for i := 0; i < n; i++ {
		b.WriteString("\033[K")
		b.WriteString(lines[i])
		b.WriteString("\r\n")
	}
	// 旧块比新块高时清掉多出的行(光标随之落到第 max(n,lastLines) 行行首)
	bottom := n
	if lastLines > bottom {
		for i := n; i < lastLines; i++ {
			b.WriteString("\033[K\r\n")
		}
		bottom = lastLines
	}
	// 从块底回到目标行, 再横移到目标列
	if up := bottom - curRow; up > 0 {
		fmt.Fprintf(&b, "\033[%dA", up)
	}
	b.WriteString("\r")
	if curCol > 0 {
		fmt.Fprintf(&b, "\033[%dC", curCol)
	}
	return b.String(), n, curRow
}

// commitSequence 生成「提交前把光标移到编辑块底行行首」的序列(纯函数)。
// 多行输入时编辑光标可能在块中间: 先移到块底再换行, 否则后续输出从中间行
// 开始会覆盖块下方内容("看不到自己写了什么")。
func commitSequence(lastLines, cursorRow int) (seq string, newCursorRow int) {
	var b strings.Builder
	b.WriteString("\r")
	row := cursorRow
	if down := lastLines - 1 - cursorRow; down > 0 {
		fmt.Fprintf(&b, "\033[%dB", down)
		row = lastLines - 1
	}
	return b.String(), row
}
