package main

// forge_emoji_test.go — gate 图标渲染的哨兵。
//
// 图标是给人看的标记; 未知 gate 必须回落到通用图标而非空白,
// 否则输出里会出现"缺一块"的静默异常。

import (
	"testing"
)

func TestForgeEmoji_AsciiLangEmoji(t *testing.T) {
	if got := asciiLangEmoji("go"); got != "[Go]" {
		t.Errorf("asciiLangEmoji(\"go\") = %q, 期望 \"[Go]\"", got)
	}
	// 未知 gate 必须有兜底标记(空串会让输出缺一块)
	if got := asciiLangEmoji("zzz"); got != "[TOOL]" {
		t.Errorf("未知 gate 应回落 [TOOL], 得到 %q", got)
	}
}

func TestForgeEmoji_LangEmoji(t *testing.T) {
	got := langEmoji("go")
	if got == "" {
		t.Fatal("langEmoji(\"go\") 为空")
	}
	// noEmoji 是包级 bool 开关(FORGE_NO_EMOJI); 此处只确认它存在且可读,
	// 不固化其取值 —— 取值随环境变化。
	_ = noEmoji
}

func TestForgeEmoji_Stable(t *testing.T) {
	// 同输入必须同输出: 图标会进入 gate 输出的固定头, 不稳定会打断缓存前缀
	for _, lang := range []string{"go", "python", "sh", "node", "math", "zzz"} {
		if a, b := langEmoji(lang), langEmoji(lang); a != b {
			t.Errorf("langEmoji(%q) 不稳定: %q != %q", lang, a, b)
		}
		if a, b := asciiLangEmoji(lang), asciiLangEmoji(lang); a != b {
			t.Errorf("asciiLangEmoji(%q) 不稳定: %q != %q", lang, a, b)
		}
	}
}
