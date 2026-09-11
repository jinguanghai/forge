package main

import "testing"

func TestVoiceSpeak(t *testing.T) {
	if err := voiceSpeak("你好，我是铸剑炉，语音集成测试，欢迎使用。"); err != nil {
		t.Fatalf("voiceSpeak err: %v", err)
	}
	t.Log("voiceSpeak ok (合成+播放已提交)")
}

func TestIsValidVoice(t *testing.T) {
	if !isValidVoice("zh-CN-XiaoxiaoNeural") {
		t.Fatal("应识别默认声线")
	}
	if isValidVoice("not-a-voice") {
		t.Fatal("不应识别伪声线")
	}
}

func TestSanitize(t *testing.T) {
	got := sanitizeForSpeech("**加粗** `code`\n# 标题\n- 列表项\n> 引用\nhttps://x.com 结束")
	for _, bad := range []string{"**", "`", "#", "-", ">", "https://"} {
		if containsStr(got, bad) {
			t.Fatalf("清洗残留 %q in %q", bad, got)
		}
	}
	t.Log("清洗结果:", got)
}

func containsStr(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
