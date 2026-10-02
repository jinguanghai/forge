package main

// ─── 单轮流式请求 ───────────────────────────────────────────────
// B3 批2: 自 RunStream 抽出 (原 agent.go:680-721, 42 行)。
//
// 关键设计: 原内联代码里的两个 continue(图片被服务端拒降级重试 / 失败升级
// 模型重试)在此改写为 retry 返回值。外层用 continue 承接 —— for 的 post
// 语句(turn++)与循环头 runCtx 取消检查照常执行, 与原内联 continue 语义等价。
//
// 状态传递沿用批1 的指针契约: messages/curModel/escalated/visionRetried 一律
// 走指针, "回写遗漏"在类型层面不可能发生。assistantContent/reasoningBuf 是
// 本回合私有产物(strings.Builder 不可复制), 故由本结构体持有, 外层经 &st.xxx 取用。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// streamTurn 承载一次流式请求及其传输层错误处理所需的全部状态。
type streamTurn struct {
	agent  *AgentRunner
	runCtx context.Context

	// ── 只读输入 ──
	userContent string
	tools       []json.RawMessage

	// ── 与 RunStream 共享的可变状态(指针, 防回写遗漏) ──
	messages      *[]ChatMessage
	curModel      *string
	escalated     *bool
	visionRetried *bool

	// ── 本回合私有产物(每轮新建, 无需 Reset) ──
	assistantContent strings.Builder
	reasoningBuf     strings.Builder
	toolCallAccum    []ToolCall
}

// request 发起一次流式请求并处理传输层错误。
//
// retry=true 表示外层应 continue 重进循环(图片降级 / 升级模型重试);
// err != nil 表示应中止 RunStream; 两者皆非即请求成功, 可继续处理流式结果。
func (s *streamTurn) request() (retry bool, err error) {
	a := s.agent

	// Streaming call — spinner spins until the first token arrives.
	spin := startSpinner("铸剑炉思考中…")
	ch := a.llm.ChatCompletionStream(s.runCtx, *s.messages, s.tools, *s.curModel)
	streamErr := a.processStream(ch, &s.assistantContent, &s.reasoningBuf, &s.toolCallAccum, a.cfg.ShowReasoning, spin.stop)
	spin.stop()

	if streamErr == nil {
		return false, nil
	}

	// ── 图片被服务端拒 (unsupported image 400) → 降级纯文本重试一次 ──
	// 服务端偶发对某张图判不支持 (格式/分辨率/数量/解码), 重试升级模型无用;
	// 剥离全部图片重发文本 (text-only fallback), 不让整体请求失败。
	if !*s.visionRetried && isImageUnsupportedError(streamErr) && len(*s.messages) > 0 {
		*s.visionRetried = true
		for i := range *s.messages {
			(*s.messages)[i].Images = nil
		}
		fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "🖼"), dim("服务端拒绝图片 (unsupported image), 已剥离图片降级为纯文本重试"))
		return true, nil // 重进循环, 用纯文本重新请求
	}
	// 失败升级: 当前是 flash 且未升级过 → 升级到 pro 重试一次
	if !*s.escalated && *s.curModel == a.cfg.ModelFlash && a.cfg.ModelPro != "" && a.cfg.ModelPro != a.cfg.ModelFlash {
		*s.escalated = true
		*s.curModel = a.cfg.ModelPro
		a.stats.setModel(*s.curModel)
		return true, nil // 重进循环, 用 pro 重新请求
	}
	// Avoid consecutive user messages: if a retry re-enters with the same
	// input, appending again would produce user,user — some APIs reject
	// that with a 400. 比较基准必须与线上发送一致 (userContent, 含召回块)。
	if len(a.history) == 0 || a.history[len(a.history)-1].Role != "user" || a.history[len(a.history)-1].Content != s.userContent {
		a.history = append(a.history, ChatMessage{Role: "user", Content: s.userContent})
	}
	a.trimHistory()
	if errors.Is(streamErr, context.Canceled) {
		return false, context.Canceled
	}
	logEvent(EvError, "LLM error", nil)
	return false, fmt.Errorf("LLM error: %w", streamErr)
}
