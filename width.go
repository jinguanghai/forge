// 终端字符显示宽度（EastAsianWidth）—— 跨平台公共函数。
package main

// eawWideExtra 补充宽字符区间表（EastAsianWidth=W/F, Unicode 15.1）。
//
// 为什么是「补充表」而不是完整表: 下方 runeWidth 主 switch 是一套经过实测的
// 近似（含若干合并区间，如谚文字母用 0x1100-0x11FF 比标准的 0x1100-0x115F 宽），
// 全表重写会连带改变这些既有判定；本表只补「原实现漏判」的区间，改动面可控。
//
// 实测源码直接受影响的漏判字符（原判 1 列, 实际 2 列）:
//
//	✅ U+2705(21 处)  ❌ U+274C(3)  ⛔ U+26D4(3)  ⏳ U+23F3(1)  ➕ U+2795(1)
//
// 其余区间（注音/彝文/谚文扩展/竖排标点/唐古特文/女书/努沙文/CJK 扩展 G）同类一并补齐。
//
// 关于 U+FE0F(VS16) 为何不在此列: 它是 Mn(零宽) 类别，按 EAW 表应计 0 列，但源码中的
// 用法都是「窄基字符 + VS16」成对出现（⚠️/ℹ️/⛓️/☯️），基字符 1 列与 VS16 1 列相加
// 恰好等于终端 emoji 呈现的 2 列。若照标准把 VS16 改为 0 列，这些字符会由 2 列变 1 列，
// 反而与终端实际渲染不符 → 保持现状（近似但结果正确，勿"按标准"改）。
var eawWideExtra = [][2]rune{
	// 0x2300-0x2BFF: emoji/符号区的 W 属性散点
	{0x231A, 0x231B}, {0x2329, 0x232A}, {0x23E9, 0x23EC}, {0x23F0, 0x23F0},
	{0x23F3, 0x23F3}, {0x25FD, 0x25FE}, {0x2614, 0x2615}, {0x2648, 0x2653},
	{0x267F, 0x267F}, {0x2693, 0x2693}, {0x26AA, 0x26AB}, {0x26BD, 0x26BE},
	{0x26C4, 0x26C5}, {0x26CE, 0x26CE}, {0x26D4, 0x26D4}, {0x26EA, 0x26EA},
	{0x26F2, 0x26F3}, {0x26F5, 0x26F5}, {0x26FA, 0x26FA}, {0x26FD, 0x26FD},
	{0x2705, 0x2705}, {0x270A, 0x270B}, {0x2728, 0x2728}, {0x274C, 0x274C},
	{0x274E, 0x274E}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27B0, 0x27B0}, {0x27BF, 0x27BF}, {0x2B1B, 0x2B1C}, {0x2B50, 0x2B50},
	{0x2B55, 0x2B55},
	// 0x3100-0xA9FF: 注音/表意描述符/彝文/谚文扩展
	{0x3105, 0x312F}, {0x3190, 0x31E3}, {0x31EF, 0x31FF}, {0xA000, 0xA48C},
	{0xA490, 0xA4C6}, {0xA960, 0xA97C},
	// 0xFE10-0xFE6B: 竖排/小写全角标点（与已有 0xFE30-0xFE4F 相邻）
	{0xFE10, 0xFE19}, {0xFE50, 0xFE52}, {0xFE54, 0xFE66}, {0xFE68, 0xFE6B},
	// 增补平面: 唐古特文/女真/假名扩展/努沙文/CJK 扩展 G
	{0x16FE0, 0x16FE4}, {0x16FF0, 0x16FF1}, {0x17000, 0x187F7}, {0x18800, 0x18CD5},
	{0x18D00, 0x18D08}, {0x1AFF0, 0x1AFF3}, {0x1AFF5, 0x1AFFB}, {0x1AFFD, 0x1AFFE},
	{0x1B000, 0x1B122}, {0x1B132, 0x1B132}, {0x1B150, 0x1B152}, {0x1B155, 0x1B155},
	{0x1B164, 0x1B167}, {0x1B170, 0x1B2FB}, {0x30000, 0x3FFFD},
}

// eawWideExtraHit 二分查找 r 是否落在补充宽区间内（表按起点升序、互不重叠）。
func eawWideExtraHit(r rune) bool {
	lo, hi := 0, len(eawWideExtra)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		switch {
		case r < eawWideExtra[mid][0]:
			hi = mid - 1
		case r > eawWideExtra[mid][1]:
			lo = mid + 1
		default:
			return true
		}
	}
	return false
}

// runeWidth 返回单个 rune 的终端显示宽度（EastAsianWidth W/F 计 2 列，
// ambiguous 字符按 wcwidth 默认计 1 列）。
// 覆盖：谚文字母、部首/康熙部首、CJK标点、假名、谚文兼容字母、带圈CJK、
// CJK兼容、CJK扩展A、CJK、韩文音节、CJK兼容表意、CJK兼容形式、
// 全角（半角片假名除外）、全角符号、emoji/符号、增补平面扩展区。
func runeWidth(r rune) int {
	switch {
	case r == 0x26A1, // ⚡ 闪电（Emoji_Presentation，Windows 终端按 2 列渲染）
		r >= 0x1100 && r <= 0x11FF,                                  // 谚文字母 (Hangul Jamo)
		r >= 0x2E80 && r <= 0x2EFF,                                  // CJK部首补充
		r >= 0x2F00 && r <= 0x2FDF,                                  // 康熙部首
		r >= 0x2FF0 && r <= 0x2FFF,                                  // 表意文字描述
		r >= 0x3000 && r <= 0x303F,                                  // CJK标点
		r >= 0x3040 && r <= 0x30FF,                                  // 平假名/片假名
		r >= 0x3130 && r <= 0x318F,                                  // 谚文兼容字母
		r >= 0x3200 && r <= 0x32FF,                                  // 带圈CJK
		r >= 0x3300 && r <= 0x33FF,                                  // CJK兼容
		r >= 0x3400 && r <= 0x4DBF,                                  // CJK扩展A
		r >= 0x4E00 && r <= 0x9FFF,                                  // CJK统一表意
		r >= 0xAC00 && r <= 0xD7A3,                                  // 韩文音节
		r >= 0xF900 && r <= 0xFAFF,                                  // CJK兼容表意
		r >= 0xFE30 && r <= 0xFE4F,                                  // CJK兼容形式
		r >= 0xFF00 && r <= 0xFFEF && !(r >= 0xFF61 && r <= 0xFF9F), // 全角（半角片假名除外）
		r >= 0xFFE0 && r <= 0xFFE6,                                  // 全角符号
		r >= 0x1F000 && r <= 0x1FAFF,                                // 麻将/emoji/符号
		r >= 0x20000 && r <= 0x2FFFF:                                // CJK扩展B+（增补平面）
		return 2
	}
	if eawWideExtraHit(r) {
		return 2
	}
	return 1
}
