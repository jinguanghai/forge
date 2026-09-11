package main

// ─── 冒烟测试: 真连 DeepSeek API ─────────────────────────────
// 默认跳过。设置 FORGE_SMOKE=1 才运行:
//   FORGE_SMOKE=1 go test -run TestSmoke -v
// 使用临时工作目录, 不污染生产缓存。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// TestSmoke_CacheReplayHit 验证 v3.0 核心缓存铁律:
// 历史回放必须与线上请求逐字节一致 → 第二轮请求的前缀(第一轮完整 prompt)应命中。
// 旧版把 userContent(含 BM25 召回块)发上线、却把 plain input 落历史 → 第二轮
// 回放 [user(input)] ≠ 线上 [user(recalled+input)], 首 token 即断裂 → 只有
// system 命中, 历史永不命中。本测试断言 hit2 >= prompt1, 直接量化该修复。
func TestSmoke_CacheReplayHit(t *testing.T) {
	cfg := smokeConfig(t)

	// ① 隔离缓存统计文件, 不污染生产 cache_stats.jsonl
	oldPath := cacheStatPath
	tmp := t.TempDir()
	cacheStatPath = filepath.Join(tmp, "cache_stats.jsonl")
	defer func() { cacheStatPath = oldPath }()

	// ② 播种 memory.json: 含独特令牌 + 近期日期 → BM25 必召回 (recalled 非空)
	mem := `{"identity":"cache-replay-test","key_findings":[{"title":"缓存验证经验","content":"DeepSeek 前缀缓存 qxzmarker 令牌 20260815 必须逐字节回放才能命中","keywords":["缓存"]}]}`
	if err := os.WriteFile(filepath.Join(tmp, "memory.json"), []byte(mem), 0644); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	cfg2 := *cfg
	cfg2.WorkDir = tmp
	a, err := NewAgentRunner(&cfg2)
	if err != nil {
		t.Fatalf("NewAgentRunner: %v", err)
	}
	defer a.Shutdown()

	done := make(chan struct{})
	go func() {
		select {
		case <-time.After(150 * time.Second):
			a.CancelCurrent()
		case <-done:
		}
	}()
	t.Cleanup(func() { close(done) })

	// ③ 两轮顺序任务: 第一轮触发召回注入, 第二轮回放历史
	prompt1 := "关于 qxzmarker 令牌的缓存经验是什么? 用一句话回答, 不要调用工具。"
	if err := a.RunStream(prompt1); err != nil {
		t.Fatalf("run1: %v", err)
	}
	// 历史第一条用户消息必须与线上发送一致 (含召回块)
	if len(a.history) < 2 || a.history[1].Role != "user" || !strings.Contains(a.history[1].Content, "qxzmarker") {
		t.Fatalf("history[1] 未存 userContent(含召回块): %+v", a.history[1])
	}
	if err := a.RunStream("好, 已了解。请确认你记住了这条缓存经验, 用一句话回答, 不要调用工具。"); err != nil {
		t.Fatalf("run2: %v", err)
	}

	// ④ 解析两轮 usage: 第二轮 hit 必须 >= 第一轮完整 prompt (回放前缀命中)
	type stat struct{ hit, miss int }
	var stats []stat
	data, rerr := os.ReadFile(cacheStatPath)
	if rerr != nil {
		t.Fatalf("cache stats not written: %v", rerr)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var s CacheStat
		if json.Unmarshal([]byte(line), &s) != nil {
			continue
		}
		stats = append(stats, stat{s.Hit, s.Miss})
	}
	if len(stats) < 2 {
		t.Fatalf("应至少 2 条 usage 记录, got %d: %s", len(stats), string(data))
	}
	first, last := stats[0], stats[len(stats)-1]
	prompt1Tokens := first.hit + first.miss
	t.Logf("round1: hit=%d miss=%d (prompt=%d) | round2: hit=%d miss=%d", first.hit, first.miss, prompt1Tokens, last.hit, last.miss)
	// 判别器1 (主): 第二轮命中必须严格大于第一轮 —— 回放 [user(召回块+输入)]
	// 若与线上不一致(旧版), 第二轮命中 ≈ 仅 system 部分 (≈first.hit); 修复后
	// 命中延伸进 user1 640+ token。
	if last.hit <= first.hit {
		t.Errorf("第二轮回放前缀未命中: hit2=%d <= hit1=%d —— 历史回放与线上请求不一致 (v3.0 铁律被破坏)", last.hit, first.hit)
	}
	// 判别器2 (块对齐容差): DeepSeek 前缀缓存按块存储, 上一请求末尾不足一整块的
	// token 不可复用 → hit2 应 ≥ prompt1 减去最后一个块 (实测 2048=16×128)。
	if last.hit < prompt1Tokens-128 {
		t.Errorf("第二轮回放命中不足: hit2=%d < prompt1-128=%d (提示: 是否前缀断裂?)", last.hit, prompt1Tokens-128)
	}
}
