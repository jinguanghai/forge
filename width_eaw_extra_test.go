package main

import "testing"

// ─── 缺陷 #6 固化: runeWidth 漏判 EastAsianWidth=W 的符号区 ──────────
//
// 原实现只覆盖 CJK/假名/韩文/全角/emoji 主区，漏掉 0x2300-0x2BFF 的 W 属性散点。
// 源码实际用到的漏判字符正是进度管理输出格式的高频字符（"✅ 完成步骤N | ⏳ 剩余"），
// 在定宽排版（表格/状态栏/padRight）里少算 1 列 → 边框错位。
// 修复 = 新增 eawWideExtra 补充区间表 + 二分查找，不改动主 switch 的既有判定。

func TestRuneWidthEAWExtra_SourceChars(t *testing.T) {
	cases := []struct {
		r    rune
		desc string
	}{
		{0x2705, "✅ WHITE HEAVY CHECK MARK (源码 21 处)"},
		{0x274C, "❌ CROSS MARK (源码 3 处)"},
		{0x26D4, "⛔ NO ENTRY (源码 3 处)"},
		{0x23F3, "⏳ HOURGLASS WITH FLOWING SAND (源码 1 处)"},
		{0x2795, "➕ HEAVY PLUS SIGN (源码 1 处)"},
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != 2 {
			t.Errorf("runeWidth(U+%04X %s) = %d, want 2", c.r, c.desc, got)
		}
	}
}

// VS16 成对字符必须保持 2 列: 窄基字符(1) + U+FE0F(1) 恰好等于终端 emoji 呈现宽度。
// 若"照 EAW 标准"把 U+FE0F(Mn) 改成 0 列，这些字符会由 2 列变 1 列 → 与终端实际渲染不符。
// 本用例是防回归哨兵: 拦住未来"按标准修正"反而改错的动作。
func TestRuneWidthVS16PairStaysTwo(t *testing.T) {
	for _, s := range []string{"⚠️", "ℹ️", "⛓️", "☯️"} {
		if got := displayWidth(s); got != 2 {
			t.Errorf("displayWidth(%q) = %d, want 2 (基字符 1 + VS16 1)", s, got)
		}
	}
}

// 补充表不变式: 起点<=终点、按起点升序、互不重叠。
// 二分查找依赖有序性，表被写乱时此用例先失败，而不是让查找静默漏判。
func TestEAWWideExtraTableInvariants(t *testing.T) {
	if len(eawWideExtra) == 0 {
		t.Fatal("eawWideExtra 为空: 漏判修复被回退?")
	}
	for i, rg := range eawWideExtra {
		if rg[0] > rg[1] {
			t.Errorf("区间 %d 起点>终点: %04X..%04X", i, rg[0], rg[1])
		}
		if i > 0 && rg[0] <= eawWideExtra[i-1][1] {
			t.Errorf("区间 %d (%04X..%04X) 与前一个 (%04X..%04X) 重叠或乱序",
				i, rg[0], rg[1], eawWideExtra[i-1][0], eawWideExtra[i-1][1])
		}
	}
}

// 二分查找边界: 区间两端与中点必命中; 区间外侧的命中结果须与全表线性扫描一致。
func TestEAWWideExtraBinarySearchBoundaries(t *testing.T) {
	inside := func(r rune) bool {
		for _, o := range eawWideExtra {
			if r >= o[0] && r <= o[1] {
				return true
			}
		}
		return false
	}
	for i, rg := range eawWideExtra {
		for _, r := range []rune{rg[0], rg[1], (rg[0] + rg[1]) / 2} {
			if !eawWideExtraHit(r) {
				t.Errorf("区间 %d 内码点 %04X 未命中", i, r)
			}
		}
		for _, r := range []rune{rg[0] - 1, rg[1] + 1} {
			if r < 0 {
				continue
			}
			if got, want := eawWideExtraHit(r), inside(r); got != want {
				t.Errorf("区间 %d 外侧 %04X: 二分=%v 线性=%v", i, r, got, want)
			}
		}
	}
}

// 真实 UI 文本净宽度: 数值由 Unicode 15.1 EAW 表算出。
func TestRuneWidthRealUIText(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"✅ 完成步骤1: 定位零覆盖函数 | ⏳ 剩余: 步骤2,3,4", 49},
		{"❌ 失败: 编译错误", 17},
		{"⛔ 操作被主人拒绝", 17},
	}
	for _, c := range cases {
		if got := displayWidth(c.s); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// 回归: 补充表不得改变主 switch 的既有判定。
func TestRuneWidthExistingBehaviorUnchanged(t *testing.T) {
	cases := []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'Z', 1}, {'0', 1}, {' ', 1}, {'\n', 1},
		{0x26A0, 1}, // ⚠ 单独出现(不带 VS16)仍是 1 列
		{0x00E9, 1}, // é
		{'中', 2}, {0x3042, 2}, {0xAC00, 2}, {0x4E00, 2}, {0x20000, 2},
		{0x26A1, 2}, // ⚡ 主 switch 特例
		{0xFF01, 2}, // 全角感叹号
		{0xFF61, 1}, // 半角片假名(主 switch 显式排除)
		{0x1F600, 2},
	}
	for _, c := range cases {
		if got := runeWidth(c.r); got != c.want {
			t.Errorf("runeWidth(U+%04X) = %d, want %d", c.r, got, c.want)
		}
	}
}
