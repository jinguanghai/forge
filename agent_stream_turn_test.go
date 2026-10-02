package main

// agent_stream_turn_test.go — B3 批2 单测镜像 (fractal F4)。
//
// 直接单测 streamTurn.request 的四个出口, 不依赖 RunStream 全链路:
//   ① 请求成功             → (retry=false, err=nil), 流式文本落在 assistantContent
//   ② 图片被服务端拒       → (retry=true, err=nil), messages 图片被剥离 + visionRetried 置位
//   ③ flash 失败且可升级   → (retry=true, err=nil), curModel 变 pro + escalated 置位
//   ④ 终态错误(无升级空间) → (retry=false, err), history 追加 user 且同内容不重复追加
// 手法: httptest 假 SSE 端点 (复用 cov_b3_branches_test.go 的 b3SSE2/b3Agent/b3Round),
// 确定性, 不触外网。这些用例与 cov_b3_branches_test.go 的全链路用例互为交叉验证:
// 全链路走 RunStream → 此处直接打 request(), 任一侧语义漂移即红。

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// newStreamTurn 构造直接单测用的 streamTurn (runCtx 用 Background)。
func newStreamTurn(a *AgentRunner, messages *[]ChatMessage, model *string, esc, vis *bool, userContent string) *streamTurn {
	return &streamTurn{
		agent:         a,
		runCtx:        context.Background(),
		userContent:   userContent,
		tools:         []json.RawMessage{ForgeToolSchema()},
		messages:      messages,
		curModel:      model,
		escalated:     esc,
		visionRetried: vis,
		toolCallAccum: []ToolCall{},
	}
}

// ① 成功出口: 不重试、不报错, 且流式文本确实写入本回合私有产物。
func TestStreamTurn_Success(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covContentLine("你好"), covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	messages := []ChatMessage{{Role: "user", Content: "hi"}}
	model := "test-model"
	esc, vis := false, false
	st := newStreamTurn(a, &messages, &model, &esc, &vis, "hi")

	retry, err := st.request()
	if err != nil || retry {
		t.Fatalf("成功路径应 (retry=false, err=nil), 实际 (retry=%v, err=%v)", retry, err)
	}
	if !strings.Contains(st.assistantContent.String(), "你好") {
		t.Fatalf("assistantContent 未收到流式文本: %q", st.assistantContent.String())
	}
	if esc || vis {
		t.Fatalf("成功路径不应置位 escalated/visionRetried: esc=%v vis=%v", esc, vis)
	}
}

// ② 图片被拒出口: 剥离全部图片并重试, 且优先于升级分支(escalated 不动)。
func TestStreamTurn_VisionRejectStripsImages(t *testing.T) {
	srv := b3SSE2([]b3Round{{
		status: 400,
		body:   `{"error":{"message":"messages[1].image[0]: You have uploaded an unsupported image"}}`,
	}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	messages := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "看图", Images: []ImagePart{{URL: "data:image/png;base64,AAAA"}}},
	}
	model := "test-model"
	esc, vis := false, false
	st := newStreamTurn(a, &messages, &model, &esc, &vis, "看图")

	retry, err := st.request()
	if err != nil || !retry {
		t.Fatalf("图片被拒应 (retry=true, err=nil), 实际 (retry=%v, err=%v)", retry, err)
	}
	if !vis {
		t.Fatal("visionRetried 未置位 —— 无此位会导致无限重试")
	}
	for i := range messages {
		if len(messages[i].Images) != 0 {
			t.Fatalf("messages[%d] 图片未剥离", i)
		}
	}
	if esc {
		t.Fatal("图片降级应优先于升级分支, escalated 不应被置位")
	}
}

// ③ 升级出口: flash 失败且 pro 可用 → 切模型重试。
func TestStreamTurn_EscalateToPro(t *testing.T) {
	srv := b3SSE2([]b3Round{{status: 400, body: `{"error":{"message":"bad request"}}`}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.ModelFlash = "test-model"
		c.ModelPro = "pro-x"
	})

	messages := []ChatMessage{{Role: "user", Content: "hi"}}
	model := "test-model"
	esc, vis := false, false
	st := newStreamTurn(a, &messages, &model, &esc, &vis, "hi")

	retry, err := st.request()
	if err != nil || !retry {
		t.Fatalf("可升级时应 (retry=true, err=nil), 实际 (retry=%v, err=%v)", retry, err)
	}
	if !esc {
		t.Fatal("escalated 未置位 —— 无此位会反复升级")
	}
	if model != "pro-x" {
		t.Fatalf("curModel 未切到 pro: %q", model)
	}
	if vis {
		t.Fatal("无图请求不应置位 visionRetried")
	}
}

// ④ 终态错误出口: 无升级空间 → 返回 LLM error, 并把本轮 user 消息补进 history;
// 同内容再次失败时不重复追加 (否则会形成 user,user → API 400)。
func TestStreamTurn_TerminalError(t *testing.T) {
	srv := b3SSE2([]b3Round{{status: 400, body: `{"error":{"message":"bad request"}}`}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil) // ModelFlash == ModelPro → 无升级空间

	messages := []ChatMessage{{Role: "user", Content: "hi"}}
	model := "test-model"
	esc, vis := false, false
	before := len(a.history)

	st := newStreamTurn(a, &messages, &model, &esc, &vis, "hi")
	retry, err := st.request()
	if retry {
		t.Fatal("终态错误不应 retry")
	}
	if err == nil || !strings.Contains(err.Error(), "LLM error") {
		t.Fatalf("错误形态不符: %v", err)
	}
	if len(a.history) != before+1 {
		t.Fatalf("history 应追加 1 条 user, 实际 %d -> %d", before, len(a.history))
	}
	if last := a.history[len(a.history)-1]; last.Role != "user" || last.Content != "hi" {
		t.Fatalf("追加的消息不符: %+v", last)
	}

	st2 := newStreamTurn(a, &messages, &model, &esc, &vis, "hi")
	if _, err2 := st2.request(); err2 == nil {
		t.Fatal("第二次仍应返回错误")
	}
	if len(a.history) != before+1 {
		t.Fatalf("同内容 user 消息被重复追加 (会形成 user,user): %d", len(a.history))
	}
}
