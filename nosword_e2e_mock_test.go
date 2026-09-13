package main

// ── nosword_e2e_mock_test.go — 无剑模式端到端冒烟 (P0#1) ──
// 不依赖真机 API: 用 httptest 假冒 OpenAI 兼容流式端点, 真实走 AgentRunner.RunStream 主循环。
// 验证链路: LLM吐含错算式文本 → nswFeedbackText 死程序嗅探求值 → 注入 user 反馈 → continue
//           → 下一轮请求体里必须能看到「EXPR = VAL」稳定锚点。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func nswMockServer(replies []string) (*httptest.Server, func() []string) {
	var mu sync.Mutex
	var bodies []string
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := idx
		idx++
		mu.Unlock()
		if n >= len(replies) {
			n = len(replies) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		txt, _ := json.Marshal(replies[n])
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s},\"finish_reason\":null}]}\n\n", txt)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, bodies...)
	}
}

func nswNewTestAgent(t *testing.T, srvURL string) *AgentRunner {
	t.Helper()
	cfg := DefaultConfig()
	cfg.APIKey = "test-key"
	cfg.BaseURL = srvURL
	cfg.Model = "test-model"
	cfg.ModelFlash = "test-model"
	cfg.ModelPro = "test-model"
	cfg.ModelVision = ""
	cfg.MiniMaxAPIKey = ""
	cfg.CompactEnabled = false
	cfg.WorkDir = t.TempDir()
	cfg.CachePersistFile = filepath.Join(cfg.WorkDir, "cache.gob")
	a, err := NewAgentRunner(cfg)
	if err != nil {
		t.Fatalf("NewAgentRunner: %v", err)
	}
	a.SaveCheckpoint = false
	t.Cleanup(func() { a.Shutdown() })
	return a
}

const (
	nswReplyBad  = "先算一下 3*7 = 20，所以答案是 20。"
	nswReplyGood = "这是一段演示文本，没有别的内容。"
)

// TestNSWEndToEnd_On: 开开关 → 应发生"求值反馈注入 + 续生成"
func TestNSWEndToEnd_On(t *testing.T) {
	saveGlobals(t)
	os.Setenv("FORGE_NOSWORD", "1")
	defer os.Unsetenv("FORGE_NOSWORD")

	srv, bodies := nswMockServer([]string{nswReplyBad, nswReplyGood})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	if err := a.RunStream("3乘7等于多少"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	reqs := bodies()
	t.Logf("请求次数=%d", len(reqs))
	for i, b := range reqs {
		t.Logf("--- 请求%d 体长=%d ---", i+1, len(b))
	}
	if len(reqs) < 2 {
		t.Fatalf("无剑未触发: 只发了 %d 次请求 (期望 >=2)", len(reqs))
	}
	second := reqs[1]
	if !strings.Contains(second, "【求值】") {
		t.Fatalf("第2次请求缺少求值反馈锚点; 尾部=%q", tail(second, 400))
	}
	if !strings.Contains(second, "3*7 = 21") {
		t.Fatalf("反馈未给出正确求值 3*7 = 21; 尾部=%q", tail(second, 400))
	}
	// 缺陷P 端到端: 注入必须自带来源信封, 否则模型把死程序反馈误当用户发言
	// (实测连错三轮, 用户真实指令被劫持)。此处验证信封真的进了请求体, 非仅单测函数。
	if !strings.Contains(second, "非用户消息") {
		t.Fatalf("注入缺少来源信封 (缺陷P 复发); 尾部=%q", tail(second, 400))
	}
	if strings.Index(second, "非用户消息") > strings.Index(second, "【求值】") {
		t.Fatalf("信封必须排在反馈本体之前 (否则模型先读到的仍是裸反馈)")
	}
	// 反馈必须是 user 角色注入 (LLM 能看到并修正)
	if !strings.Contains(second, "\"role\":\"user\"") {
		t.Fatalf("反馈未以 user 消息注入")
	}
	// 缓存铁律: 第2次请求的 messages 必须以上一次为前缀 (append-only, 不打断前缀缓存)
	m0, m1 := messagesJSON(reqs[0]), messagesJSON(reqs[1])
	if m0 == "" || m1 == "" {
		t.Fatalf("messages 提取失败: %d / %d", len(m0), len(m1))
	}
	// m0 尾部的 "]" 是数组闭合符, 第2次请求该位置是 "," (继续追加元素) → 比对时去掉闭合符
	m0body := strings.TrimSuffix(m0, "]")
	if !strings.HasPrefix(m1, m0body) {
		t.Fatalf("前缀被打断: len0=%d len1=%d, 公共前缀=%d", len(m0body), len(m1), commonPrefix(m1, m0body))
	}
	t.Logf("✅ 缓存前缀 append-only: %d 字节逐字节恒定 (仅尾部追加 2 条消息)", len(m0body))

	// 收尾历史 = 第2轮答复 (反馈注入轮不入 history)
	last := a.history[len(a.history)-1]
	if last.Role != "assistant" || last.Content != nswReplyGood {
		t.Fatalf("history 收尾异常: role=%s content=%q", last.Role, last.Content)
	}
	t.Logf("✅ 无剑闭环: 第1轮含错算式 → 注入「3*7 = 21」→ 第2轮收尾")
}

// TestNSWEndToEnd_Off: 关开关 → 行为与现状完全一致 (1 次请求, 零注入)
func TestNSWEndToEnd_Off(t *testing.T) {
	saveGlobals(t)
	os.Unsetenv("FORGE_NOSWORD")

	srv, bodies := nswMockServer([]string{nswReplyBad, nswReplyGood})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	if err := a.RunStream("3乘7等于多少"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	reqs := bodies()
	if len(reqs) != 1 {
		t.Fatalf("开关关闭仍有额外请求: %d 次", len(reqs))
	}
	if strings.Contains(reqs[0], "【求值】") {
		t.Fatalf("开关关闭仍注入反馈")
	}
	if strings.Contains(reqs[0], "非用户消息") {
		t.Fatalf("开关关闭仍注入信封 (零注入契约被破坏)")
	}
	last := a.history[len(a.history)-1]
	if last.Content != nswReplyBad {
		t.Fatalf("history 收尾异常: %q", last.Content)
	}
	t.Logf("✅ 默认关: 1 次请求, 零注入, 行为与现状一致")
}

// TestNSWEndToEnd_RoundCap: LLM 反复吐算式 → 注入上限 2 次后必须收尾 (防死循环)
func TestNSWEndToEnd_RoundCap(t *testing.T) {
	saveGlobals(t)
	os.Setenv("FORGE_NOSWORD", "1")
	defer os.Unsetenv("FORGE_NOSWORD")

	// 每一轮都吐含算式文本 → 若上限失效将无限请求
	srv, bodies := nswMockServer([]string{
		"2+2 = 5。", "2+2 = 5。", "2+2 = 5。", "2+2 = 5。",
	})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	if err := a.RunStream("算一下"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	reqs := bodies()
	t.Logf("请求次数=%d (期望 3 = 首轮 + 2 次注入上限)", len(reqs))
	if len(reqs) != 3 {
		t.Fatalf("注入上限失效: %d 次请求", len(reqs))
	}
	for i, b := range reqs[1:] {
		if !strings.Contains(b, "2+2 = 4") {
			t.Fatalf("第%d次注入缺正确锚点", i+2)
		}
	}
	last := a.history[len(a.history)-1]
	if !strings.Contains(last.Content, "2+2 = 5") {
		t.Fatalf("history 收尾异常: %q", last.Content)
	}
	t.Logf("✅ 上限兜底: 恰好 3 次请求后收尾, 不无限转")
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// messagesJSON 从请求体里抠出 messages 数组原文 (括号配平)。
func messagesJSON(body string) string {
	i := strings.Index(body, `"messages":[`)
	if i < 0 {
		return ""
	}
	start := i + len(`"messages":`)
	depth := 0
	for j := start; j < len(body); j++ {
		switch body[j] {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return body[start : j+1]
			}
		}
	}
	return ""
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
