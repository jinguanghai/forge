package main

// ── nosword_exit2_mock_test.go — 第二收尾出口(碎片全无效)端到端 (20260912 缺陷O) ──
//
// 场景: 流式响应携带 tool_calls 分片但 ID/name 全为空 → filterValidToolCalls 全滤掉
// → 走"降级为纯文本"的第二收尾出口 (agent.go 里 `if len(toolCallAccum) == 0` 之后那支)。
// 修复前该出口只接了虚报检测、没接无剑 → 这条路径上的算式错值静默漏过。
// 本测试同时验证分母埋点: frag 出口也必须落 nosword_probe 行。

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

// nswFragMockServer 每轮都回 "文本 + 无效 tool_call 碎片 + finish_reason=stop"。
// llm.go 对 finish_reason=stop 且累积了 tool_call 的情况会 merge 后发 tool_call_done,
// merge 出的条目 ID/name 为空 → filterValidToolCalls 全丢 → 第二收尾出口。
func nswFragMockServer(replies []string) (*httptest.Server, func() []string) {
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
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%s,\"tool_calls\":[{\"index\":0,\"id\":\"\",\"type\":\"function\",\"function\":{\"name\":\"\",\"arguments\":\"{\\\"lang\\\"\"}}]},\"finish_reason\":null}]}\n\n", txt)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, bodies...)
	}
}

// TestNSWExit2_FragDegraded 碎片全无效路径必须同样触发无剑干预
func TestNSWExit2_FragDegraded(t *testing.T) {
	saveGlobals(t)
	os.Setenv("FORGE_NOSWORD", "1")
	defer os.Unsetenv("FORGE_NOSWORD")

	srv, bodies := nswFragMockServer([]string{nswReplyBad, nswReplyGood})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	if err := a.RunStream("3乘7等于多少"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	reqs := bodies()
	t.Logf("请求次数=%d (期望 >=2)", len(reqs))
	if len(reqs) < 2 {
		t.Fatalf("第二收尾出口未接无剑: 只发了 %d 次请求, 错值 3*7=20 静默漏过", len(reqs))
	}
	if !strings.Contains(reqs[1], "【求值】") {
		t.Fatalf("第2次请求缺少求值反馈锚点; 尾部=%q", tail(reqs[1], 400))
	}
	if !strings.Contains(reqs[1], "3*7 = 21") {
		t.Fatalf("反馈未给出正确求值 3*7 = 21; 尾部=%q", tail(reqs[1], 400))
	}
	// 缓存铁律: 第2次请求必须以第1次为前缀 (append-only, 仅尾部追加消息)
	m0, m1 := messagesJSON(reqs[0]), messagesJSON(reqs[1])
	if m0 == "" || m1 == "" {
		t.Fatalf("messages 抠取失败")
	}
	// m0 尾部的 "]" 是数组闭合符, 第2次该位置是 "," (继续追加) → 比对时去掉闭合符
	m0body := strings.TrimSuffix(m0, "]")
	if !strings.HasPrefix(m1, m0body) {
		t.Fatalf("前缀被打断: len0=%d len1=%d, 公共前缀=%d", len(m0body), len(m1), commonPrefix(m1, m0body))
	}
	// 收尾历史 = 第2轮答复 (反馈注入轮不入 history)
	last := a.history[len(a.history)-1]
	if last.Role != "assistant" || last.Content != nswReplyGood {
		t.Fatalf("history 收尾异常: role=%s content=%q", last.Role, last.Content)
	}
	t.Logf("✅ 第二出口已接无剑: 反馈注入 + 前缀保持")
}

// TestNSWExit2_ProbeAudit 分母埋点: 收尾必须落 nosword_probe 行 (含 frag/plain 口径)
func TestNSWExit2_ProbeAudit(t *testing.T) {
	saveGlobals(t)
	os.Setenv("FORGE_NOSWORD", "1")
	defer os.Unsetenv("FORGE_NOSWORD")

	srv, _ := nswFragMockServer([]string{nswReplyBad, nswReplyGood})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)
	if err := a.RunStream("3乘7等于多少"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	path := filepath.Join(a.cfg.WorkDir, "gate_audit.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读审计文件失败 (%s): %v", path, err)
	}
	s := string(b)
	if !strings.Contains(s, `"event":"nosword_probe"`) && !strings.Contains(s, `"event": "nosword_probe"`) {
		t.Fatalf("缺 nosword_probe 分母行; 审计内容=%s", s)
	}
	if !strings.Contains(s, `"enabled":true`) && !strings.Contains(s, `"enabled": true`) {
		t.Errorf("probe 行缺 enabled=true 口径; 审计=%s", s)
	}
	if !strings.Contains(s, "nosword_probe") || !strings.Contains(s, "frag") {
		t.Errorf("probe 行未标 source=frag (第二出口口径); 审计=%s", s)
	}
	t.Logf("✅ 分母埋点已落盘: %d 字节", len(b))
}
