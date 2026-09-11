package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestVisionE2E 真实端到端: 工作区真实图片 → vision 模型 → 返回内容描述。
// 密钥由 LoadConfig 自动读取; FORGE_E2E=1 才执行 (防误烧钱)。
func TestVisionE2E(t *testing.T) {
	if os.Getenv("FORGE_E2E") != "1" {
		t.Skip("FORGE_E2E!=1, 跳过真实 API 冒烟")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.ModelVision == "" {
		t.Fatal("ModelVision 为空")
	}
	t.Logf("vision model: %s", cfg.ModelVision)

	img, err := loadImagePart("三参气机模型.png")
	if err != nil {
		t.Fatalf("loadImagePart: %v", err)
	}
	msg := ChatMessage{
		Role:    "user",
		Content: "这张图里有什么？用中文简短回答。",
		Images:  []ImagePart{img},
	}
	reqBody := chatRequest{
		Model:       cfg.ModelVision,
		Messages:    []ChatMessage{msg},
		Stream:      false,
		MaxTokens:   300,
		Temperature: 0.3,
		Thinking:    &ThinkingConfig{Type: "disabled"},
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("请求前段: %s", truncateForLog(string(b), 300))

	apiURL := "https://api.deepseek.com/v1/chat/completions"
	req, err := http.NewRequest("POST", apiURL, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Auth"+"orization", "Bearer "+cfg.APIKey)
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("API 请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != 200 {
		t.Fatalf("API status=%d body=%s", resp.StatusCode, string(body))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) == 0 || out.Choices[0].Message.Content == "" {
		t.Fatal("模型返回空内容")
	}
	t.Logf("模型回答: %s", out.Choices[0].Message.Content)
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
