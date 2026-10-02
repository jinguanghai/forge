package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func parseSSEToStrings(t *testing.T, body string) ([]StreamEvent, error) {
	t.Helper()
	c := &LLMClient{cfg: &Config{}}
	ch := make(chan StreamEvent, 32)
	err := c.parseSSE(context.Background(), strings.NewReader(body), ch, "deepseek-flash")
	close(ch)
	var evs []StreamEvent
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs, err
}

func TestLLMStream_ParseSSE_NormalDone(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"你好\"},\"finish_reason\":\"\"}]}\n\n" +
		"data: [DONE]\n\n"
	evs, err := parseSSEToStrings(t, body)
	if err != nil {
		t.Fatalf("正常流不应报错: %v", err)
	}
	var gotContent, gotDone bool
	for _, ev := range evs {
		if ev.Type == "content" && ev.Content == "你好" {
			gotContent = true
		}
		if ev.Type == "done" {
			gotDone = true
		}
	}
	if !gotContent || !gotDone {
		t.Fatalf("期望 content+done, 实际 %+v", evs)
	}
}

// 双判据: 兼容端点可能不发 [DONE] 但发 finish_reason, 不得误判为截断。
func TestLLMStream_ParseSSE_FinishReasonWithoutDone(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"
	evs, err := parseSSEToStrings(t, body)
	if err != nil {
		t.Fatalf("有 finish_reason 即正常收尾, 不应报错: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("应至少产出 content/done 事件")
	}
}

// 无任何事件 + 无收尾证据 = 零输出截断 → 可重试。
func TestLLMStream_ParseSSE_TruncatedNoOutput(t *testing.T) {
	_, err := parseSSEToStrings(t, ": keep-alive\n\n")
	if !errors.Is(err, errStreamTruncated) {
		t.Fatalf("零输出截断应报 errStreamTruncated, 实际 %v", err)
	}
}

// 已输出半截 + 无收尾证据 = 不可重试 (重发会重复输出)。
func TestLLMStream_ParseSSE_TruncatedAfterOutput(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"半截\"}}]}\n\n"
	_, err := parseSSEToStrings(t, body)
	if !errors.Is(err, errPartialStream) {
		t.Fatalf("已输出后截断应报 errPartialStream, 实际 %v", err)
	}
}

func TestLLMStream_ParseSSE_SkipsMalformedAndNonData(t *testing.T) {
	body := "event: message\n" +
		"data: {not json}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n"
	evs, err := parseSSEToStrings(t, body)
	if err != nil {
		t.Fatalf("应跳过非法行而非报错: %v", err)
	}
	for _, ev := range evs {
		if ev.Type == "content" && ev.Content == "ok" {
			return
		}
	}
	t.Fatalf("应保留合法 chunk, 实际 %+v", evs)
}

func TestLLMStream_UsageReasoningTokens(t *testing.T) {
	if got := usageReasoningTokens(nil); got != 0 {
		t.Fatalf("nil usage 应为 0, 实际 %d", got)
	}
	if got := usageReasoningTokens(&Usage{}); got != 0 {
		t.Fatalf("无 detail 应为 0, 实际 %d", got)
	}
	u := &Usage{CompletionTokensDetail: &CompletionTokensDetail{ReasoningTokens: 42}}
	if got := usageReasoningTokens(u); got != 42 {
		t.Fatalf("应返回 42, 实际 %d", got)
	}
}

func TestLLMStream_SseStateZeroValue(t *testing.T) {
	var st sseState
	if st.sawDone || st.sawFinish || st.toolDone || st.sentEvents != 0 {
		t.Fatal("零值状态应全部为假/0")
	}
}
