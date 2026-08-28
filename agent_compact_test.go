package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ── 测试夹具: 假 DeepSeek API ────────────────────────────────
type fakeSummaryServer struct {
	mu      sync.Mutex
	summary string
	fail    bool // 返回 500
	calls   int
}

func (f *fakeSummaryServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		fail := f.fail
		sum := f.summary
		f.mu.Unlock()
		if fail {
			w.WriteHeader(500)
			w.Write([]byte("{\"error\":\"boom\"}"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": sum}},
			},
		})
	}
}

func newCompactRunner(t *testing.T, srv *httptest.Server, cfg *Config) *AgentRunner {
	t.Helper()
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = srv.URL
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "test-key"
	}
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	if cfg.ModelFlash == "" {
		cfg.ModelFlash = "test-flash"
	}
	if cfg.CompactTokenThreshold == 0 {
		cfg.CompactTokenThreshold = 500
	}
	if cfg.CompactMinTurns == 0 {
		cfg.CompactMinTurns = 3
	}
	// CompactEnabled 不在此强制, 由各测试显式决定 (修复: 无条件覆盖吞掉 Disabled 测试的 false)
	llm := NewLLMClient(cfg)
	llm.client = srv.Client()
	ctx, cancel := context.WithCancel(context.Background())
	return &AgentRunner{
		cfg:    cfg,
		llm:    llm,
		ctx:    ctx,
		cancel: cancel,
	}
}

func longMsg(n int) string {
	return strings.Repeat("这是一条用于撑大token估算的很长的对话内容。", n/20+1)[:n]
}

func buildHistory(system string, n int) []ChatMessage {
	h := []ChatMessage{{Role: "system", Content: system}}
	for i := 0; i < n; i++ {
		h = append(h,
			ChatMessage{Role: "user", Content: longMsg(200) + "问题" + string(rune('0'+i%10))},
			ChatMessage{Role: "assistant", Content: longMsg(300) + "回答" + string(rune('0'+i%10))},
		)
	}
	return h
}

// 1. 触发: 超阈值 → 摘要注入头部, 历史变短, system 保持头部, 冷却设置
func TestMaybeCompactTriggers(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "会话摘要: 任务A已完成, 任务B待办。"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYSTEM-RULES", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) >= before {
		t.Fatalf("压缩后历史未变短: before=%d after=%d", before, len(a.history))
	}
	if !strings.HasPrefix(a.history[1].Content, "【会话摘要】") {
		t.Fatalf("history[1] 不是摘要: %q", a.history[1].Content)
	}
	if a.history[0].Role != "system" {
		t.Fatalf("system 应保持头部, got %s", a.history[0].Role)
	}
	if a.compactCooldown != 3 {
		t.Fatalf("冷却未设置: got %d want 3", a.compactCooldown)
	}
}

// 2. 冷却: 压缩后 MIN_TURNS 轮内不再压缩, 冷却结束可再压
func TestMaybeCompactCooldown(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要X"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYS", 15)
	a.maybeCompact()
	after1 := len(a.history)
	for i := 0; i < 3; i++ {
		a.maybeCompact()
		if len(a.history) != after1 {
			t.Fatalf("冷却期第%d次调用历史不应变化", i+1)
		}
	}
	a.maybeCompact() // 冷却结束 (3次递减到0), 应能再次压缩
	if len(a.history) >= after1 {
		t.Fatalf("冷却结束后应能再次压缩")
	}
}

// 3. 开关关闭 → 不压缩
func TestMaybeCompactDisabled(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要X"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: false})
	a.history = buildHistory("SYS", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) != before {
		t.Fatalf("开关关闭时不应压缩")
	}
}

// 4. 低于阈值 → 不压缩
func TestMaybeCompactBelowThreshold(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要X"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true, CompactTokenThreshold: 1 << 20})
	a.history = buildHistory("SYS", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) != before {
		t.Fatalf("低于阈值不应压缩")
	}
}

// 5. 摘要失败 → 降级不阻塞, 历史不变
func TestMaybeCompactFailFallback(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "", fail: true}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYS", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) != before {
		t.Fatalf("摘要失败应降级, 历史不变")
	}
}

// 6. Summarize 非流式: 正常返回摘要
func TestSummarizeNonStream(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "这是摘要正文"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, nil)
	got, err := a.llm.Summarize(context.Background(), []ChatMessage{{Role: "user", Content: "你好"}}, 400)
	if err != nil {
		t.Fatalf("Summarize err: %v", err)
	}
	if got != "这是摘要正文" {
		t.Fatalf("got %q want 摘要正文", got)
	}
}

// 7. 摘要调用走 Flash 模型 + Stream=false + thinking disabled
func TestSummarizeUsesFlashNonStream(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"摘要\"}}]}"))
	}))
	defer srv.Close()
	a := newCompactRunner(t, srv, nil)
	if _, err := a.llm.Summarize(context.Background(), []ChatMessage{{Role: "user", Content: "x"}}, 400); err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotBody["model"] != "test-flash" {
		t.Fatalf("摘要应走 Flash 模型, got %v", gotBody["model"])
	}
	if gotBody["stream"] != false {
		t.Fatalf("摘要应为非流式, got %v", gotBody["stream"])
	}
	if th, ok := gotBody["thinking"].(map[string]interface{}); !ok || th["type"] != "disabled" {
		t.Fatalf("摘要应关 thinking, got %v", gotBody["thinking"])
	}
}

// 8. trimHistory 挂钩: 超阈值+可压缩时, trimHistory 后历史含摘要 (端到端)
func TestTrimHistoryInvokesCompact(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "端到端摘要"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYS", 20)
	a.trimHistory()
	found := false
	for _, m := range a.history {
		if strings.HasPrefix(m.Content, "【会话摘要】") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("trimHistory 后应含摘要消息")
	}
}

// 9. 六西格玛 I1 证据: 摘要请求前缀 = 真实历史前缀 (缓存命中前置), 指令追加为最后一条
func TestSummarizePreservesPrefix(t *testing.T) {
	var gotMsgs []ChatMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMessage `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotMsgs = body.Messages
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"摘要"}}]}`))
	}))
	defer srv.Close()
	a := newCompactRunner(t, srv, nil)
	// 含 system + tool_calls 配对的真实历史段
	hist := []ChatMessage{
		{Role: "system", Content: "SYSTEM-RULES"},
		{Role: "user", Content: "任务1"},
		{Role: "assistant", Content: "思考", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "forge", Arguments: `{"code":"print(1)"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "输出1"},
		{Role: "user", Content: "任务2"},
		{Role: "assistant", Content: "回答2"},
	}
	if _, err := a.llm.Summarize(context.Background(), hist, 400); err != nil {
		t.Fatalf("Summarize err: %v", err)
	}
	if len(gotMsgs) != len(hist)+1 {
		t.Fatalf("摘要请求消息数 = %d, want %d (原样 + 1条指令)", len(gotMsgs), len(hist)+1)
	}
	// 前缀逐字节一致: 摘要请求前 len(hist) 条与传入历史 JSON 序列化完全一致 → KV 前缀缓存命中
	wantJSON, _ := json.Marshal(hist)
	gotJSON, _ := json.Marshal(gotMsgs[:len(hist)])
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("前缀不一致 (缓存不会命中)!\nwant=%s\ngot =%s", wantJSON, gotJSON)
	}
	// 最后一条是指令
	last := gotMsgs[len(gotMsgs)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "会话摘要器") {
		t.Fatalf("最后一条应为摘要指令: %+v", last)
	}
	// system 保持头部
	if gotMsgs[0].Role != "system" {
		t.Fatalf("摘要请求首条应为 system: %s", gotMsgs[0].Role)
	}
}
