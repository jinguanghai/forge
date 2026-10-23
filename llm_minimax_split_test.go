package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// llm_minimax_split_test.go — MiniMax 思维链分离 (reasoning_split) 哨兵。
//
// 根因 (20261008 实测): MiniMax-M3 默认把思维链塞进 delta.content, 且带 <think>...</think>
// 标签, 而 reasoning_content 恒空 —— 正文被思维链污染。实测同一道题:
//
//	不下发 reasoning_split: content = "<think>...</think>\n\n2.4小时" (61 字符, 思考混正文)
//	下发   reasoning_split: content = "2.4小时（或2小时24分钟）" (15 字符), reasoning_content = 431 字符
//
// 后果: 「只给最终答案」类指令遵循任务生产参数组 0/6 全灭 (加该字段后同组 6/6)。
// 修法: 仅 MiniMax 端点下发 reasoning_split:true, DeepSeek 请求体字节级不变
// (未知字段 + 前缀缓存纪律: DeepSeek 不认此字段, 不得混入)。
// 20261011: MiniMax 改作故障兜底后本字段仍在 (兜底路径同样需要干净正文)。
//
// 本哨兵钉两件事 (缺一即退化为"改回去也全绿"):
//  1. MiniMax 端点请求体必须含 "reasoning_split":true (断言原始 JSON 字节, 非解码后 bool)
//  2. DeepSeek 端点请求体必须完全不含该字段 (omitempty 生效, 非 false 值)
func TestDoStream_MiniMaxReasoningSplit(t *testing.T) {
	saveGlobals(t)
	mmCh := make(chan string, 1)
	mmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mmCh <- string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer mmSrv.Close()
	dsCh := make(chan string, 1)
	dsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		dsCh <- string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer dsSrv.Close()

	cfg := &Config{
		APIKey: "ds-key", BaseURL: dsSrv.URL, Model: "deepseek-flash",
		MiniMaxAPIKey: "mm-key", MiniMaxBaseURL: mmSrv.URL, MiniMaxModel: "MiniMax-M3",
		RequestTimeout: 10 * time.Second, ShowReasoning: true,
	}
	c := NewLLMClient(cfg)
	msgs := []ChatMessage{{Role: "user", Content: "hi"}}

	// 20261011 起 MiniMax 只在故障兜底路径出现, 故直接取兜底端点发送
	mmEp, ok := c.failoverEndpoint("deepseek-flash", msgs)
	if !ok {
		t.Fatal("MiniMax 三字段齐备应可兜底")
	}
	if err := c.doStreamTo(context.Background(), msgs, nil, make(chan StreamEvent, 32), mmEp.Model, mmEp); err != nil {
		t.Fatalf("MiniMax doStreamTo error: %v", err)
	}
	mmRaw := <-mmCh
	if !strings.Contains(mmRaw, `"reasoning_split":true`) {
		t.Fatalf("MiniMax 请求体缺 reasoning_split:true (思维链会污染正文), body=%s", mmRaw)
	}
	if !strings.Contains(mmRaw, `"MiniMax-M3"`) {
		t.Fatalf("MiniMax 端点模型未锚定, body=%s", mmRaw)
	}

	if err := c.doStream(context.Background(), msgs, nil, make(chan StreamEvent, 32), "deepseek-flash"); err != nil {
		t.Fatalf("DeepSeek doStream error: %v", err)
	}
	dsRaw := <-dsCh
	if strings.Contains(dsRaw, "reasoning_split") {
		t.Fatalf("DeepSeek 端点不得下发 MiniMax 专属字段, body=%s", dsRaw)
	}
}
