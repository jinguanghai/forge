package main

import (
	"strings"
	"testing"
)

// ─── 缺陷 #4 固化: truncateDisplay 不感知 ANSI 转义序列 ──────────────
//
// 旧实现逐 rune 计宽，把 ESC 序列的每个字节都算作 1 列，后果:
//   ①可见内容被提前截断（实测三段彩色文本 w=30 时只显示 21 列，欠 9 列 = ESC[32m + ESC[0m 的字节数）
//   ②截断点可能落在序列内部，残缺 CSI 泄漏到终端（裸 ESC 会让终端把后续文本当参数解析）
// 修复 = 与 displayWidth/fitWidth 同口径: ESC 序列原样透传且不占宽度。

// ansiBrokenAt 返回输出中第一个无终结符的 ANSI 序列位置，-1 表示全部完好。
// 判据: 每个 ESC 后面必须是 '['，且到下一个 'm' 之间不含 ESC。
func ansiBrokenAt(s string) int {
	i := 0
	for i < len(s) {
		if s[i] != 0x1b {
			i++
			continue
		}
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return i // 无终结符
		}
		seg := s[i : i+j+1]
		if len(seg) < 3 || seg[1] != '[' {
			return i // 畸形
		}
		i += j + 1
	}
	return -1
}

func TestTruncateDisplay_PlainASCII(t *testing.T) {
	cases := []struct {
		in   string
		col  int
		want string
	}{
		// 截断发生时末尾追加省略号 "…" (1 列), 实际可见宽 = col-1 + 1 = col
		{"hello world", 5, "hell…"},
		{"hello", 99, "hello"}, // 无截断, 不加省略号
		{"hello", 0, ""},       // col=0 直接空串
		{"hello", 1, "…"},      // col=1 预算=0, 一上来就截断, 只剩省略号
		{"", 10, ""},
	}
	for _, c := range cases {
		if got := truncateDisplay(c.in, c.col); got != c.want {
			t.Errorf("truncateDisplay(%q, %d) = %q, want %q", c.in, c.col, got, c.want)
		}
	}
}

// 核心回归: 带 ANSI 的文本截断后可见宽度必须恰好用满 col 列（不欠列）。
// 旧实现在此必然失败: 实测 col=30 只产出 21 列。
func TestTruncateDisplay_ANSIWideIsExact(t *testing.T) {
	text := color(ansi.green, "缓存命中 98.5% 会话") + "  " + color(ansi.dim, "turns=12 tokens=34567 tools=8 12.3s")
	if w := displayWidth(text); w <= 30 {
		t.Fatalf("测试文本太短(%d 列)，无法验证截断", w)
	}
	for _, col := range []int{20, 30, 40, 50} {
		got := truncateDisplay(text, col)
		if got == text {
			t.Errorf("col=%d 未发生截断", col)
			continue
		}
		// 截断后可见宽应 = col (含末尾省略号)
		if w := displayWidth(got); w != col {
			t.Errorf("col=%d: 截断后可见宽 %d 列(欠 %d), 输出 %q", col, w, col-w, got)
		}
		// 截断必须带省略号标记, 避免主人看不出
		if !strings.HasSuffix(stripANSI(got), "…") {
			t.Errorf("col=%d: 截断后未带省略号: %q", col, got)
		}
		if bad := ansiBrokenAt(got); bad >= 0 {
			t.Errorf("col=%d: 输出含残缺 ANSI 序列(位置 %d): %q", col, bad, got)
		}
	}
}

// 逐列扫描: 任何 col 下都不得产出残缺/裸 ESC（旧实现 col=1 直接产出裸 ESC）。
func TestTruncateDisplay_NeverEmitsBrokenEscape(t *testing.T) {
	text := color(ansi.green, "会话缓存") + color(ansi.cyan, "模型 flash") + color(ansi.dim, "统计")
	for col := 0; col <= displayWidth(text)+5; col++ {
		got := truncateDisplay(text, col)
		if bad := ansiBrokenAt(got); bad >= 0 {
			t.Fatalf("col=%d 产出残缺 ANSI 序列: %q", col, got)
		}
		if w := displayWidth(got); w > col {
			t.Fatalf("col=%d 超出预算: 实际 %d 列 %q", col, w, got)
		}
	}
}

func TestTruncateDisplay_CJKAndEmoji(t *testing.T) {
	cases := []struct {
		in   string
		col  int
		want int // 期望可见宽度
	}{
		// 截断时末尾追加省略号 "…" (1 列); 输入未截断时实际宽 = col (不够填)
		// 实测预算 budget = col-1:
		//   "中文测试" col=4: budget=3
		//     "中"(w=2≤3) 写 → w=2
		//     "文"(2+2=4>3) 截断 +省略号 → 总宽 = 2+1 = 3
		{"中文测试", 4, 3},
		{"中文测试", 5, 5}, // budget=4: "中"2 + "文"2 = 4; "测" 4+2>4 截断 +省略号 → 4+1=5
		{"中文测试", 3, 3}, // budget=2: "中"2 写 w=2; "文" 2+2>2 截断 → 2+1=3
		{"中文测试", 1, 1}, // budget=0: 一开始就截断 → 1 列省略号
		{"✅ 完成", 2, 1}, // budget=1: ✅(2)>1 立即截断 → 1
		{"✅ 完成", 3, 3}, // budget=2: ✅(2≤2) 写 w=2; " " 2+1>2 截断 → 2+1=3
		{"✅ 完成", 1, 1}, // budget=0: → 1 列省略号
	}
	for _, c := range cases {
		got := truncateDisplay(c.in, c.col)
		if w := displayWidth(got); w != c.want {
			t.Errorf("truncateDisplay(%q, %d) 可见宽 = %d, want %d (输出 %q)", c.in, c.col, w, c.want, got)
		}
	}
}

// 与 fitWidth 同口径: 纯可见内容（无 ANSI）时两者截断结果一致。
func TestTruncateDisplay_ConsistentWithFitWidth(t *testing.T) {
	// truncate 与 fitWidth 现在同口径: 截断时都补省略号, 不截断时都不补。
	for _, s := range []string{"abcdefghij", "中文中文中文", "✅❌⛔ 混排 abc"} {
		for col := 1; col <= displayWidth(s)+2; col++ {
			got := truncateDisplay(s, col)
			fw := fitWidth(s, col)
			// 同列下两者输出必须完全一致 (口径钉死)
			if got != fw {
				t.Errorf("truncate(%q,%d)=%q 与 fitWidth=%q 不一致", s, col, got, fw)
			}
			if w := displayWidth(got); w > col {
				t.Errorf("truncateDisplay(%q,%d) 超预算: %d 列", s, col, w)
			}
		}
	}
}

// 残缺 ANSI 序列（无 'm' 终结符）: 整段丢弃，绝不把裸 ESC 泄漏到终端。
// 裸 ESC 会让终端把后续文本当作 CSI 参数继续解析（吞输出/颜色错乱）。
func TestTruncateDisplay_DropsMalformedEscape(t *testing.T) {
	for _, s := range []string{
		"\x1b[48;5;234", // 缺终结符
		"abc\x1b[",      // 畸形
		"abc\x1b",       // 裸 ESC
		"\x1b",          // 仅 ESC
	} {
		got := truncateDisplay(s, 100)
		if bad := ansiBrokenAt(got); bad >= 0 {
			t.Errorf("truncateDisplay(%q) 泄漏残缺序列: %q", s, got)
		}
		if strings.ContainsRune(got, 0x1b) {
			t.Errorf("truncateDisplay(%q) 输出仍含 ESC: %q", s, got)
		}
	}
}
