// 终端字符显示宽度（EastAsianWidth）—— 跨平台公共函数。
package main

// runeWidth 返回单个 rune 的终端显示宽度（EastAsianWidth W/F 计 2 列，
// ambiguous 字符按 wcwidth 默认计 1 列）。
// 覆盖：谚文字母、部首/康熙部首、CJK标点、假名、谚文兼容字母、带圈CJK、
// CJK兼容、CJK扩展A、CJK、韩文音节、CJK兼容表意、CJK兼容形式、
// 全角（半角片假名除外）、全角符号、emoji/符号、增补平面扩展区。
func runeWidth(r rune) int {
	switch {
	case r == 0x26A1, // ⚡ 闪电（Emoji_Presentation，Windows 终端按 2 列渲染）
		r >= 0x1100 && r <= 0x11FF, // 谚文字母 (Hangul Jamo)
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
	return 1
}
