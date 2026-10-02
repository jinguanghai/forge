package main

// cov_guard_review_more_test.go — 守卫二级裁决补测 (20260920)
//
// guard_review.go 显式声明 guardReviewLLM 接口并注明"便于测试注入", 但此前全仓库
// 零测试使用它 → reviewCriticalAction / AgentRunner.ReviewCritical 零覆盖。
// 本文件把该设计意图兑现为死程序判定: 复核链路的 fail-closed 语义必须可验证 ——
// 调用失败 / 超时 / 输出不可解析 一律拒绝, 绝不默认放行 (安全默认拒绝)。
//
// 两层覆盖:
//   ① reviewCriticalAction 走注入接口 (stub), 穷举裁决分支;
//   ② AgentRunner.ReviewCritical 走真实 LLMClient + httptest (AgentRunner.llm 是
//      具体类型 *LLMClient, 无法注入接口), 验证模型选择经请求体落到线上。

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// stubGuardLLM 注入式假客户端: 按脚本吐流事件, 记录被请求的模型名与调用次数。
type stubGuardLLM struct {
	events   []StreamEvent
	gotModel string
	calls    int
}

func (s *stubGuardLLM) ChatCompletionStream(ctx context.Context, messages []ChatMessage, tools []json.RawMessage, model ...string) <-chan StreamEvent {
	s.calls++
	if len(model) > 0 {
		s.gotModel = model[0]
	}
	ch := make(chan StreamEvent, len(s.events)+1)
	for _, e := range s.events {
		ch <- e
	}
	close(ch)
	return ch
}

func guardStubContent(s string) []StreamEvent {
	return []StreamEvent{{Type: "content", Content: s}}
}

// 裁决 allow: 唯一放行路径, 理由必须原样带回。
func TestReviewCriticalAction_Allow(t *testing.T) {
	llm := &stubGuardLLM{events: guardStubContent(`{"verdict":"allow","reason":"正当防御演练"}`)}
	allowed, reason, err := reviewCriticalAction(llm, "m-flash", "看一下我的防火墙规则", "attack", "nmap")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !allowed {
		t.Fatalf("allow 裁决应放行")
	}
	if reason != "正当防御演练" {
		t.Fatalf("理由未原样带回: %q", reason)
	}
	if llm.calls != 1 {
		t.Fatalf("复核应恰好调用一次, 实际 %d", llm.calls)
	}
}

// 裁决 deny: 拒绝路径 + 理由。
func TestReviewCriticalAction_Deny(t *testing.T) {
	llm := &stubGuardLLM{events: guardStubContent(`{"verdict":"deny","reason":"越权删除他人数据"}`)}
	allowed, reason, err := reviewCriticalAction(llm, "m-flash", "删掉对方服务器", "destroy", "rm -rf")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if allowed {
		t.Fatalf("deny 裁决不得放行")
	}
	if reason != "越权删除他人数据" {
		t.Fatalf("理由不符: %q", reason)
	}
}

// fail-closed 之一: 输出不可解析 → 拒绝 + 报错 (绝不默认放行)。
func TestReviewCriticalAction_UnparsableIsDenied(t *testing.T) {
	llm := &stubGuardLLM{events: guardStubContent("我觉得应该没问题, 但我不确定")}
	allowed, reason, err := reviewCriticalAction(llm, "m-flash", "x", "attack", "hit")
	if err == nil {
		t.Fatalf("不可解析必须报错 (fail-closed)")
	}
	if allowed {
		t.Fatalf("不可解析时不得放行")
	}
	if reason != "" {
		t.Fatalf("不可解析时不应给出理由: %q", reason)
	}
	if !strings.Contains(err.Error(), "不可解析") {
		t.Fatalf("错误信息应指明不可解析: %v", err)
	}
}

// fail-closed 之二: 流内 error 事件 → 拒绝 + 报错。
func TestReviewCriticalAction_StreamErrorIsDenied(t *testing.T) {
	llm := &stubGuardLLM{events: []StreamEvent{
		{Type: "content", Content: `{"verdict":"allow"`},
		{Type: "error", Error: errors.New("connection reset")},
	}}
	allowed, _, err := reviewCriticalAction(llm, "m-flash", "x", "attack", "hit")
	if err == nil {
		t.Fatalf("流内错误必须报错")
	}
	if allowed {
		t.Fatalf("流内错误时不得放行")
	}
	if !strings.Contains(err.Error(), "复核请求失败") {
		t.Fatalf("错误信息应标注复核请求失败: %v", err)
	}
}

// 流式分片: 裁决 JSON 被拆成多块 content 也必须能拼出结论。
func TestReviewCriticalAction_ChunkedContent(t *testing.T) {
	llm := &stubGuardLLM{events: []StreamEvent{
		{Type: "content", Content: `{"verdict":`},
		{Type: "reasoning", Content: "（推理过程不应进入裁决文本）"},
		{Type: "content", Content: `"allow","reason":"`},
		{Type: "content", Content: `分片拼接"}`},
	}}
	allowed, reason, err := reviewCriticalAction(llm, "m-flash", "x", "attack", "hit")
	if err != nil {
		t.Fatalf("分片内容应可解析: %v", err)
	}
	if !allowed || reason != "分片拼接" {
		t.Fatalf("分片解析结果不符: allowed=%v reason=%q", allowed, reason)
	}
}

// AgentRunner 包装层: 走真实 LLMClient + httptest, 验证
//
//	① ModelFlash 优先 / 为空回退 cfg.Model 的选择结果落到请求体 model 字段;
//	② 端到端裁决文本经 SSE 解析后仍能正确放行。
func TestReviewCritical_ModelSelectionEndToEnd(t *testing.T) {
	saveGlobals(t)
	oldCache := cacheStatPath
	cacheStatPath = filepath.Join(t.TempDir(), "cache_stats.jsonl")
	t.Cleanup(func() { cacheStatPath = oldCache })

	var mu sync.Mutex
	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var req chatRequest
		_ = json.Unmarshal(b, &req)
		mu.Lock()
		models = append(models, req.Model)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sseLine(`{"choices":[{"delta":{"content":"{\"verdict\":\"allow\",\"reason\":\"经复核放行\"}"},"finish_reason":null}]}`))
		io.WriteString(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	c := newClientWithServer(t, srv)

	// ① ModelFlash 非空 → 用 ModelFlash
	a := &AgentRunner{llm: c, cfg: &Config{Model: "test-model", ModelFlash: "flash-lite"}}
	allowed, reason, err := a.ReviewCritical("查一下我的防火墙规则", "attack", "nmap")
	if err != nil {
		t.Fatalf("复核不应报错: %v", err)
	}
	if !allowed || reason != "经复核放行" {
		t.Fatalf("裁决结果不符: allowed=%v reason=%q", allowed, reason)
	}

	// ② ModelFlash 为空 → 回退 cfg.Model
	b := &AgentRunner{llm: c, cfg: &Config{Model: "test-model"}}
	if _, _, err := b.ReviewCritical("x", "k", "h"); err != nil {
		t.Fatalf("复核不应报错: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(models) != 2 {
		t.Fatalf("应发起 2 次复核请求, 实际 %d (%v)", len(models), models)
	}
	if models[0] != "flash-lite" {
		t.Fatalf("ModelFlash 非空时应使用 ModelFlash, 实际请求模型 %q", models[0])
	}
	if models[1] != "test-model" {
		t.Fatalf("ModelFlash 为空时应回退 cfg.Model, 实际请求模型 %q", models[1])
	}
}
