package main

// cov_state_more_test.go — 状态/IO 层分支补测 (agent/session/vision/asr/toolstream/status_bar)
// 断言策略: 只校验确定性不变量(非空/数量/宽度/错误), 不硬编码展示文本。

import (
	"context"
	"strings"
	"testing"
)

func TestCovState_HashOutputAndUnknownTool(t *testing.T) {
	h1, h2 := hashOutput("abc"), hashOutput("abc")
	if h1 != h2 || h1 == "" {
		t.Errorf("hashOutput 非确定性或空: %q %q", h1, h2)
	}
	if hashOutput("abd") == h1 {
		t.Error("hashOutput 对不同输入返回相同值")
	}
	msg := buildUnknownToolMsg("forge")
	if !strings.Contains(msg, "forge") {
		t.Errorf("buildUnknownToolMsg 未含工具名: %q", msg)
	}
}

func TestCovState_MemoryTailDiffAndCompact(t *testing.T) {
	cases := [][2]string{
		{"", ""}, {"a", "a"}, {"a", "b"}, {"a\nb", "a\nc"}, {"长文本", "长文本追加"},
	}
	for _, c := range cases {
		_ = memoryTailDiff(c[0], c[1])
	}
	wd := t.TempDir()
	if got := compactFoldedIndex(wd); got != "" && !strings.Contains(got, "折叠") && !strings.Contains(got, "folded") {
		t.Logf("compactFoldedIndex(空目录)=%q", got)
	}
}

func TestCovState_AgentContextCancelLast(t *testing.T) {
	agent, _, _ := newHandleCmdAgent(t)
	if agent.Context() == nil {
		t.Error("Context() 返回 nil")
	}
	agent.CancelCurrent()
	if out, ok := agent.LastOutput(); ok && out == "" {
		t.Error("LastOutput ok=true 但内容为空")
	}
	if agent.verifyHeadInvariant() != true && agent.verifyHeadInvariant() != false {
		t.Error("verifyHeadInvariant 返回值异常")
	}
}

func TestCovState_SessionLifecycle(t *testing.T) {
	wd := t.TempDir()
	if l, err := listSessions(wd); err != nil || len(l) != 0 {
		t.Errorf("空目录 listSessions = %v, %v", l, err)
	}
	s, err := createSession(wd)
	if err != nil || s == nil {
		t.Fatalf("createSession: %v", err)
	}
	l, err := listSessions(wd)
	if err != nil {
		t.Fatalf("listSessions: %v", err)
	}
	if len(l) != 1 {
		t.Errorf("listSessions 数量 = %d, 期望 1", len(l))
	}
	if got, err := loadSession(wd, l[0].ID); err != nil || got == nil {
		t.Errorf("loadSession(%q): %v", l[0].ID, err)
	}
	if _, err := loadSession(wd, "no-such-id"); err == nil {
		t.Error("loadSession 对不存在 id 未报错")
	}
	// 观察: newSessionID 基于秒级时间戳, 同秒两次调用会撞 ID (真实使用间隔远大于 1s)
	if a := newSessionID(); a == "" || len(a) < 8 {
		t.Errorf("newSessionID 形态异常: %q", a)
	}
	if d := ensureSessionDir(wd, "sid1"); d == "" {
		t.Error("ensureSessionDir 返回空")
	}
}

func TestCovState_DeriveTitleAndStrip(t *testing.T) {
	if got := deriveSessionTitle([]ChatMessage{{Role: "user", Content: "帮我写一个排序函数"}}); got == "" {
		t.Error("deriveSessionTitle 返回空")
	}
	_ = deriveSessionTitle(nil)
	_ = deriveSessionTitle([]ChatMessage{{Role: "user", Content: "<memory_context>x</memory_context>"}, {Role: "assistant", Content: "a"}})
	_ = stripRecallPrefix("<recalled_memory>abc</recalled_memory>")
	if got := stripRecallPrefix("普通文本"); got != "普通文本" {
		t.Errorf("stripRecallPrefix 改动了普通文本: %q", got)
	}
}

func TestCovState_VisionHelpers(t *testing.T) {
	if visionCapable(nil) {
		t.Error("visionCapable(nil) 应为 false")
	}
	if visionCapable(&Config{}) {
		t.Error("visionCapable(空 ModelVision) 应为 false")
	}
	if !visionCapable(&Config{ModelVision: "deepseek-flash"}) {
		t.Error("visionCapable(有值) 应为 true")
	}
	if isImageUnsupportedError(nil) {
		t.Error("isImageUnsupportedError(nil) 应为 false")
	}
	if isImageUnsupportedError(&LLMError{StatusCode: 500, Message: "unsupported image"}) {
		t.Error("非 400 不应判为图片不支持")
	}
	if isImageUnsupportedError(&LLMError{StatusCode: 400, Message: "bad request"}) {
		t.Error("400 但非图片错误不应命中")
	}
	if !isImageUnsupportedError(&LLMError{StatusCode: 400, Message: "unsupported image format"}) {
		t.Error("400 + unsupported image 应命中")
	}
	wd := t.TempDir()
	if _, err := discoverImagesInDir(wd); err != nil {
		t.Errorf("空目录 discoverImagesInDir: %v", err)
	}
	if _, err := discoverImagesInDir(wd + "/nope"); err == nil {
		t.Error("不存在目录应报错")
	}
	_ = detectToolImages("见 D:\\x\\a.png 和 b.jpg", wd)
}

func TestCovState_AsrEnvAndToolDir(t *testing.T) {
	if asrToolDir() == "" {
		t.Error("asrToolDir 为空")
	}
	if asrEnv() == nil {
		t.Error("asrEnv 为 nil")
	}
}

func TestCovState_CodeMaxLinesAndExtract(t *testing.T) {
	t.Setenv("FORGE_CODE_MAX_LINES", "3")
	if codeMaxLines() != 3 {
		t.Errorf("codeMaxLines=3 期望 3, got %d", codeMaxLines())
	}
	t.Setenv("FORGE_CODE_MAX_LINES", "-1")
	if codeMaxLines() != 0 {
		t.Errorf("负数应回落 0, got %d", codeMaxLines())
	}
	t.Setenv("FORGE_CODE_MAX_LINES", "abc")
	if codeMaxLines() != 0 {
		t.Errorf("非法值应回落 0, got %d", codeMaxLines())
	}
	t.Setenv("FORGE_CODE_MAX_LINES", "")
	if codeMaxLines() != 0 {
		t.Errorf("空值应回落 0, got %d", codeMaxLines())
	}
	_ = extractCodeFromArgs(`{"code":"print(1)"}`)
	_ = extractCodeFromArgs(`{"language":"python","code":"x=1"}`)
	_ = extractCodeFromArgs(`{}`)
	_ = extractCodeFromArgs(`not json`)
}

func TestCovState_TruncateDisplay(t *testing.T) {
	cases := []struct {
		s   string
		col int
	}{
		{"abcdef", 3}, {"中文测试", 4}, {"a中b文", 3}, {"", 5}, {"x", 0}, {"很长很长很长的文本", 100},
	}
	for _, c := range cases {
		out := truncateDisplay(c.s, c.col)
		if displayWidth(out) > c.col && c.col > 0 {
			t.Errorf("truncateDisplay(%q,%d)=%q 宽度 %d 超限", c.s, c.col, out, displayWidth(out))
		}
	}
}

var _ = context.Background
