package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// 端到端诊断: 真实截图 → discoverImagesInDir → ChatCompletionStream(vision) → 诊断三栏
func TestVisionDirDiag(t *testing.T) {
	if os.Getenv("FORGE_E2E") != "1" {
		t.Skip("FORGE_E2E!=1: 跳过真实 vision 目录 API")
	}
	dir := os.ExpandEnv(`${USERPROFILE}\Pictures\Screenshots`)
	parts, err := discoverImagesInDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) == 0 {
		t.Fatal("目录内无图片")
	}
	t.Logf("目录识别到 %d 张图片, 类型: %s", len(parts), parts[0].URL[5:20])

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ShowReasoning = false // 禁用thinking, 快速输出content, 验证诊断prompt
	cfg.MaxTokens = 65536
	llm := NewLLMClient(cfg)
	defer llm.Shutdown()

	prompt := "你是视觉诊断专家。请对以下图片做三件事：\n" +
		"① 识别 — 完整转述看到的内容(文字/布局/对象/颜色/层级);\n" +
		"② 诊断 — 系统性列出问题点并分级 🔴高/🟡中/🟢低;\n" +
		"③ 建议 — 每个问题给一句可立即执行的改法。\n" +
		"只依据图中真实存在的元素判断,不编造;若图片正常无明显问题,明确说'未发现明显问题'。"

	msgs := []ChatMessage{
		{Role: "system", Content: "你是视觉诊断专家."},
		{Role: "user", Content: prompt, Images: parts},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		t.Fatal("诊断输出为空")
	}
	hasIdentify := strings.Contains(out, "识别") || strings.Contains(out, "①")
	hasDiag := strings.Contains(out, "诊断") || strings.Contains(out, "②") || strings.Contains(out, "🟡")
	hasSuggest := strings.Contains(out, "建议") || strings.Contains(out, "③")
	t.Logf("诊断结果(%d字): %s", len(out), out)
	if !hasIdentify || !hasDiag {
		t.Fatalf("诊断输出缺少三栏结构: 识别=%v 诊断=%v 建议=%v", hasIdentify, hasDiag, hasSuggest)
	}
}
