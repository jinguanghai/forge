package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 端到端: 真实截图目录 → discoverImagesInDir → ChatCompletionStream(vision) → 识别内容
func TestVisionDirE2E(t *testing.T) {
	if os.Getenv("FORGE_E2E") != "1" {
		t.Skip("FORGE_E2E!=1: 跳过真实 vision 目录 API")
	}
	dir := os.ExpandEnv(`${USERPROFILE}\Pictures\Screenshots`)
	parts, err := discoverImagesInDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) == 0 {
		t.Fatal("目录内无图片(应至少1张 屏幕截图(33).png)")
	}
	for _, p := range parts {
		if !strings.HasPrefix(p.URL, "data:image/") {
			t.Fatalf("URL 不是 data image: %s", p.URL[:20])
		}
	}
	t.Logf("目录识别到 %d 张图片, 类型: %s", len(parts), parts[0].URL[5:20])

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	llm := NewLLMClient(cfg)
	defer llm.Shutdown()

	msgs := []ChatMessage{
		{Role: "system", Content: "你是图片识别助手, 请准确转述图片内容。"},
		{Role: "user", Content: "请识别这张图片", Images: parts},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ch := llm.ChatCompletionStream(ctx, msgs, nil, cfg.ModelVision)

	var sb strings.Builder
	for ev := range ch {
		if strings.Contains(ev.Type, "content") {
			sb.WriteString(ev.Content)
		}
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		t.Fatal("识别输出为空")
	}
	t.Logf("识别结果(%d字): %s", len(out), out)
}
