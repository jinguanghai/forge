package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// ansiSim 是极简 ANSI 光标模拟器: 解释编辑器输出的控制序列并追踪光标落点。
//
// 用它而不是硬编码期望字节串 —— 断言的是「模型不变量」(序列跑完光标落在哪),
// 而不是某个具体序列形态: 生成方式变了(比如合并重复 \r)只要落点正确就不该报红。
// 本机无 Linux(同 T22 第 2 层 ⑤), 这是 Linux 重绘模型唯一能在 Windows 上被
// 死程序验证的途径。
type ansiSim struct {
	row, col int
	width    int
	// pending 是真实终端的「延迟回绕」状态: 写满最后一列后光标仍停在该列,
	// 只有下一个可打印字符才会真正换行。这是本模拟器的关键 —— 若按「立即回绕」
	// 建模, 满宽行后紧跟的 \r\n 会被算成多走一行, 与真实终端不符。
	pending bool
}

func (s *ansiSim) feed(seq string) {
	i := 0
	for i < len(seq) {
		c := seq[i]
		switch {
		case c == '\r':
			s.col = 0
			s.pending = false
			i++
		case c == '\n':
			s.row++
			i++
		case c == 0x1b && i+1 < len(seq) && seq[i+1] == '[':
			j := i + 2
			for j < len(seq) && !(seq[j] >= 0x40 && seq[j] <= 0x7e) {
				j++
			}
			if j >= len(seq) {
				return // 不完整序列: 忽略余下
			}
			n := 1
			if p := seq[i+2 : j]; p != "" {
				if v, err := strconv.Atoi(strings.TrimLeft(p, "?")); err == nil {
					n = v
				}
			}
			switch seq[j] {
			case 'A':
				s.row -= n
				s.pending = false
			case 'B':
				s.row += n
				s.pending = false
			case 'C':
				s.col += n
				s.pending = false
			case 'D':
				s.col -= n
				s.pending = false
			case 'K': // 清行: 不改光标位置
			}
			i = j + 1
		default:
			r, size := utf8.DecodeRuneInString(seq[i:])
			if s.pending { // 上一个字符写满了末列: 此刻才真正换行
				s.row++
				s.col = 0
				s.pending = false
			}
			s.col += runeWidth(r)
			if s.width > 0 && s.col >= s.width {
				s.col = s.width
				s.pending = true
			}
			i += size
		}
	}
}

// TestRedrawSequenceCursorInvariant 断言重绘模型的不变量:
// 序列执行完毕, 光标落在 cursorBlockPos 给出的 (行, 列); 返回值与渲染行数一致。
func TestRedrawSequenceCursorInvariant(t *testing.T) {
	cases := []struct {
		name                         string
		prompt                       string
		termW                        int
		buf                          string
		cursor, lastLines, cursorRow int
	}{
		{"单行短输入", "» ", 80, "hello", 5, 1, 0},
		{"光标在行中", "» ", 80, "hello world", 5, 1, 0},
		{"超宽折行", "» ", 20, strings.Repeat("a", 50), 50, 1, 0},
		{"块高收缩", "» ", 20, "short", 5, 5, 4},
		{"块高增长", "» ", 20, strings.Repeat("b", 60), 60, 1, 0},
		{"多行粘贴", "» ", 30, "line1\nline2\nline3", 17, 3, 2},
		{"恰好铺满末行", "» ", 10, "12345678", 8, 1, 0},
		{"宽字符折行", "» ", 20, strings.Repeat("中", 12), 12, 1, 0},
		{"空输入", "» ", 80, "", 0, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			const startRow = 100
			sim := &ansiSim{row: startRow + c.cursorRow, width: c.termW}
			buf := []rune(c.buf)
			seq, newLines, newRow := redrawSequence(c.prompt, c.termW, buf, c.cursor, c.lastLines, c.cursorRow)
			sim.feed(seq)

			wantRow, wantCol := cursorBlockPos(c.prompt, c.termW, buf, c.cursor)
			if sim.row != startRow+wantRow {
				t.Errorf("光标行错位: got %d want %d (相对块顶 %d)", sim.row, startRow+wantRow, wantRow)
			}
			if sim.col != wantCol {
				t.Errorf("光标列错位: got %d want %d", sim.col, wantCol)
			}
			if newRow != wantRow {
				t.Errorf("返回 cursorRow 错: got %d want %d", newRow, wantRow)
			}
			if wantLines := len(wrapText(c.prompt+c.buf, c.termW)); newLines != wantLines {
				t.Errorf("返回 lastLines 错: got %d want %d", newLines, wantLines)
			}
		})
	}
}

// TestCommitSequenceCursorInvariant 断言提交序列把光标送到块底行行首。
func TestCommitSequenceCursorInvariant(t *testing.T) {
	const startRow = 200
	for _, c := range []struct{ lastLines, cursorRow int }{{1, 0}, {3, 2}, {5, 1}, {4, 3}, {2, 0}} {
		sim := &ansiSim{row: startRow + c.cursorRow, width: 80}
		seq, newRow := commitSequence(c.lastLines, c.cursorRow)
		sim.feed(seq)
		if sim.row != startRow+c.lastLines-1 || sim.col != 0 {
			t.Errorf("lastLines=%d cursorRow=%d: 光标 got (%d,%d) want (%d,0)",
				c.lastLines, c.cursorRow, sim.row, sim.col, startRow+c.lastLines-1)
		}
		if newRow != c.lastLines-1 {
			t.Errorf("lastLines=%d cursorRow=%d: 返回行 got %d want %d",
				c.lastLines, c.cursorRow, newRow, c.lastLines-1)
		}
	}
}

// TestRedrawSequenceNoAbsoluteCursor 断言 Linux 重绘序列不含绝对定位(H):
// 终端没有「缓冲区绝对行号」, 一旦有人把 Windows 的 setCursorPos 式绝对定位
// 搬进来, 折行/滚动场景就会错位 —— 这是平台边界, 由判据钉住。
func TestRedrawSequenceNoAbsoluteCursor(t *testing.T) {
	seq, _, _ := redrawSequence("» ", 20, []rune(strings.Repeat("x", 50)), 50, 3, 2)
	if strings.Contains(seq, "\033[H") || strings.Contains(seq, "\033[1;") {
		t.Errorf("Linux 重绘序列不应含绝对定位(CUP): %q", seq)
	}
}

// TestReadlineRenderIsPortable 结构哨兵: 重绘序列生成必须留在跨平台文件里
// (无 build tag) —— 否则本机(Windows)再也跑不到这些判据, 模型失去死程序验证。
func TestReadlineRenderIsPortable(t *testing.T) {
	b, err := os.ReadFile("readline_render.go")
	if err != nil {
		t.Fatalf("读 readline_render.go: %v", err)
	}
	if strings.Contains(string(b), "//go:build") {
		t.Error("readline_render.go 必须跨平台(无 build tag), 否则重绘模型失去可测性")
	}
	// Linux 平台层必须走纯函数, 不得自行手写序列(否则判据测的是另一个模型)
	lb, err := os.ReadFile("readline_editor_linux.go")
	if err != nil {
		t.Fatalf("读 readline_editor_linux.go: %v", err)
	}
	for _, want := range []string{"redrawSequence(", "commitSequence("} {
		if !strings.Contains(string(lb), want) {
			t.Errorf("readline_editor_linux.go 必须调用 %s", want)
		}
	}
	if strings.Contains(string(lb), `"\\033[`) {
		t.Error("readline_editor_linux.go 不应自行拼 ANSI 序列(应走纯函数)")
	}
}
