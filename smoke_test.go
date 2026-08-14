package main

// ─── 冒烟测试: 真连 DeepSeek API ─────────────────────────────
// 默认跳过。设置 FORGE_SMOKE=1 才运行:
//   FORGE_SMOKE=1 go test -run TestSmoke -v
// 使用临时工作目录, 不污染生产缓存。

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func smokeConfig(t *testing.T) *Config {
	t.Helper()
	if os.Getenv("FORGE_SMOKE") != "1" {
		t.Skip("set FORGE_SMOKE=1 to run smoke tests (real DeepSeek API)")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Skipf("no usable API config: %v", err)
	}
	return cfg
}

func TestSmoke_LLMClient(t *testing.T) {
	cfg := smokeConfig(t)
	l := NewLLMClient(cfg)
	defer l.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	msgs := []ChatMessage{
		{Role: "system", Content: "你是测试助手, 回答必须极短。"},
		{Role: "user", Content: "只回复两个字: 通过"},
	}
	var sb strings.Builder
	var gotErr error
	ch := l.ChatCompletionStream(ctx, msgs, nil, cfg.Model)
	for ev := range ch {
		switch ev.Type {
		case "content":
			sb.WriteString(ev.Content)
		case "error":
			gotErr = ev.Error
		}
	}
	if gotErr != nil {
		t.Fatalf("stream error: %v", gotErr)
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		t.Fatal("empty response from API")
	}
	t.Logf("LLM response: %q", out)
}

// runAgentSmoke runs RunStream with a timeout guard.
func runAgentSmoke(t *testing.T, cfg *Config, prompt string) *AgentRunner {
	t.Helper()
	cfg2 := *cfg
	cfg2.WorkDir = t.TempDir() // 隔离: 不碰生产缓存/临时目录
	a, err := NewAgentRunner(&cfg2)
	if err != nil {
		t.Fatalf("NewAgentRunner: %v", err)
	}
	t.Cleanup(a.Shutdown)

	done := make(chan struct{})
	go func() {
		select {
		case <-time.After(150 * time.Second):
			a.CancelCurrent()
		case <-done:
		}
	}()
	t.Cleanup(func() { close(done) })

	if err := a.RunStream(prompt); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	return a
}

func TestSmoke_RunStream_PlainAnswer(t *testing.T) {
	cfg := smokeConfig(t)
	a := runAgentSmoke(t, cfg, "用一句话回答: 1加1等于几? 不要调用工具。")

	if len(a.history) < 2 {
		t.Fatal("history too short")
	}
	last := a.history[len(a.history)-1]
	if last.Role != "assistant" || strings.TrimSpace(last.Content) == "" {
		t.Fatalf("last history entry not a final answer: %+v", last)
	}
	t.Logf("final answer: %s", last.Content)
}

func TestSmoke_RunStream_WithTool(t *testing.T) {
	cfg := smokeConfig(t)
	a := runAgentSmoke(t, cfg, "用 python 计算 6 乘 7, 把结果告诉我。")

	// 验证最后回答非空
	if len(a.history) < 2 {
		t.Fatal("history too short")
	}
	last := a.history[len(a.history)-1]
	if last.Role != "assistant" || strings.TrimSpace(last.Content) == "" {
		t.Fatalf("no final answer: %+v", last)
	}
	t.Logf("final answer: %s", last.Content)

	// 工具调用发生在 messages 工作台, 不落入 history; 用 stats 判断工具路径是否被实际执行
	a.stats.mu.RLock()
	toolOK, toolFail := a.stats.ToolOK, a.stats.ToolFail
	a.stats.mu.RUnlock()
	if toolOK == 0 && toolFail == 0 {
		t.Log("WARNING: model did not invoke forge tool — tool path not exercised")
	} else {
		t.Logf("tool executed: OK=%d Fail=%d", toolOK, toolFail)
		if toolOK == 0 {
			t.Errorf("all tool calls failed")
		}
	}
}
