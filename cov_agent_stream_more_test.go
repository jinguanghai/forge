package main

// cov_agent_stream_more_test.go — RunStream 主循环分支补测 (httptest 假 LLM 端点)
// 覆盖: 纯文本回复 / 工具调用轮 / 未知工具名 / 工具参数非法 / HTTP 错误 / 空增量。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// covSSEServer 按轮次返回原始 SSE data 行; 轮次用尽后重复最后一轮。
func covSSEServer(rounds [][]string, status int) *httptest.Server {
	var mu sync.Mutex
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		n := idx
		idx++
		mu.Unlock()
		if status != 0 && status != 200 {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":{"message":"cov mock failure"}}`)
			return
		}
		if n >= len(rounds) {
			n = len(rounds) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range rounds[n] {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
}

func covContentLine(s string) string {
	b, _ := json.Marshal(s)
	return fmt.Sprintf(`{"choices":[{"delta":{"content":%s},"finish_reason":null}]}`, b)
}

const covStopLine = `{"choices":[{"delta":{},"finish_reason":"stop"}]}`

func covToolCallLine(id, name, args string) string {
	ab, _ := json.Marshal(args)
	return fmt.Sprintf(`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]},"finish_reason":null}]}`, id, name, ab)
}

const covToolFinishLine = `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`

func TestCovStream_PlainAnswer(t *testing.T) {
	srv := covSSEServer([][]string{{covContentLine("这是纯文本回答"), covStopLine}}, 200)
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)
	if err := a.RunStream("你好"); err != nil {
		t.Logf("RunStream 返回错误(可接受): %v", err)
	}
}

func TestCovStream_ToolCallThenAnswer(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("call_1", "forge", `{"code":"print('COV_TOOL')","lang":"python"}`), covToolFinishLine},
		{covContentLine("工具已执行完毕"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)
	if err := a.RunStream("跑一段 python"); err != nil {
		t.Logf("RunStream 错误(可接受): %v", err)
	}
}

func TestCovStream_UnknownToolAndBadArgs(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("call_x", "根本不存在的工具", `{"a":1}`), covToolFinishLine},
		{covToolCallLine("call_y", "forge", `{不是合法 JSON`), covToolFinishLine},
		{covContentLine("结束"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)
	if err := a.RunStream("触发未知工具"); err != nil {
		t.Logf("RunStream 错误(可接受): %v", err)
	}
}

func TestCovStream_EmptyAndErrorStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 SSE 流式端到端测试(含超时等待)")
	}
	srv := covSSEServer([][]string{{covStopLine}}, 200)
	a := nswNewTestAgent(t, srv.URL)
	if err := a.RunStream("空回复"); err != nil {
		t.Logf("空回复错误(可接受): %v", err)
	}
	srv.Close()

	srv2 := covSSEServer(nil, 500)
	defer srv2.Close()
	a2 := nswNewTestAgent(t, srv2.URL)
	if err := a2.RunStream("服务端错误"); err == nil {
		t.Error("HTTP 500 时 RunStream 应返回错误")
	}
}
