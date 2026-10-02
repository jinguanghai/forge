// ══════════════════════════════════════════════════════════════
// agent_stream_loop.go — 流式主循环 (processStream) 与消息装配 (buildStreamMessages)。
// ══════════════════════════════════════════════════════════════

package main

import (
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

// ─── Stream processing ─────────────────────────────────────

func (a *AgentRunner) processStream(
	ch <-chan StreamEvent,
	contentBuf *strings.Builder,
	reasoningBuf *strings.Builder,
	toolCalls *[]ToolCall,
	showReasoning bool,
	onFirstEvent func(),
) error {
	var tokenCount int64
	renderer := newStreamRenderer()
	reasonRenderer := newReasoningRenderer()
	// 工具代码流式显示: arguments 分片到达即逐行实时打印(高亮), 不等整段生成完。
	codeStreamer := &toolCodeStreamer{}
	firstEvent := true
	// 表达式化过滤器 (20260920 无剑二期): 标记可能跨分块到达, 未闭合部分暂存。
	exprF := &nswExprFilter{}
	drainExpr := func() {
		if !nswExprEnabled() {
			return
		}
		if out := exprF.flush(); out != "" {
			fmt.Print(renderer.feed(out))
		}
	}

	for ev := range ch {
		if firstEvent {
			firstEvent = false
			if onFirstEvent != nil {
				onFirstEvent()
			}
		}
		switch ev.Type {
		case "error":
			// 与其他出口一致地收尾: 已渲染内容必须 flush, 否则终端状态残留半行。
			drainExpr()
			fmt.Print(renderer.flush())
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
			}
			fmt.Println()
			if out := codeStreamer.flush(); out != "" {
				fmt.Fprint(os.Stderr, out)
			}
			a.stats.addToken(tokenCount)
			return ev.Error

		case "reasoning":
			reasoningBuf.WriteString(ev.Content)
			if showReasoning {
				fmt.Fprint(os.Stderr, reasonRenderer.feed(ev.Content))
			}

		case "content":
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
			}
			// Use the stream renderer for syntax highlighting
			shown := ev.Content
			if nswExprEnabled() {
				// 展示层替换 {{算式}} → 死程序求值结果; contentBuf 仍写原文 ——
				// 历史里保留标记格式, 模型下一轮看到自己写过的格式才会自维持。
				shown = exprF.feed(ev.Content)
			}
			fmt.Print(renderer.feed(shown))
			contentBuf.WriteString(ev.Content)
			tokenCount += int64(utf8.RuneCountInString(ev.Content))

		case "tool_call_delta":
			// 先收尾推理块: 否则推理边框要等到 tool_call_done 才闭合, 代码块会嵌在
			// 推理块的中间(实测 e2e 视觉穿插)。close 幂等, 重复调用返回空串。
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
			}
			// 累积给上层执行; 同时把 arguments 分片喂给流式显示器(边收边显示代码)。
			*toolCalls = append(*toolCalls, ev.ToolCalls...)
			for _, tc := range ev.ToolCalls {
				if out := codeStreamer.feed(tc.Function.Arguments, tc.Function.Name); out != "" {
					fmt.Fprint(os.Stderr, out)
				}
			}

		case "tool_call_done":
			// Replace accumulated deltas with merged result
			*toolCalls = ev.ToolCalls
			// Flush any remaining renderer state
			drainExpr()
			fmt.Print(renderer.flush())
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
			}
			fmt.Println()
			if out := codeStreamer.flush(); out != "" {
				fmt.Fprint(os.Stderr, out)
			}
			a.streamedCode = codeStreamer.code()
			a.stats.addToken(tokenCount)
			return nil

		case "cache_usage":
			// 会话级实时缓存命中率累计 (状态栏数据源)
			a.stats.addCache(ev.CacheHit, ev.CacheMiss)

		case "done":
			// Flush any remaining renderer state
			drainExpr()
			fmt.Print(renderer.flush())
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
			}
			fmt.Println()
			a.stats.addToken(tokenCount)
			if nswExprEnabled() {
				// 表达式化埋点 (20260920): 触发率不可测则无法判断价值, 先埋点
				nswExprAudit(a, exprF.marks, len(exprF.bad), exprF.saved)
			}
			if ev.Truncated {
				fmt.Fprintf(os.Stderr, "%s⚠️ 输出被截断: 达到 max_tokens=%d 上限, 回复不完整%s\n",
					ansi.yellow, a.cfg.MaxTokens, ansi.reset)
				if ev.ReasoningTokens > 0 {
					fmt.Fprintf(os.Stderr, "%s   (其中推理消耗 %d tokens; 需要完整输出可设 LLM_MAX_TOKENS 调大)%s\n",
						ansi.dim, ev.ReasoningTokens, ansi.reset)
				}
			}
			return nil
		}
	}

	drainExpr()
	fmt.Print(renderer.flush())
	a.stats.addToken(tokenCount)
	return nil
}

// ─── 生命分形单元: buildStreamMessages ──────────────────────
// 每个 agent 生命周期都始于"装配消息":
//
//	输入 input ──→ 清理历史 ──→ 召回记忆 ──→ 挂识图 ──→ 输出 messages/userContent/images
//
// 与 RunStream 主循环共享同一"输入→处理→输出"模式 (自相似小循环, 分形层级的一级)。
func (a *AgentRunner) buildStreamMessages(input string) ([]ChatMessage, string, []ImagePart) {
	messages := make([]ChatMessage, len(a.history)+1)
	copy(messages, a.history)
	// 跨 user turn 清理 reasoning_content (deepseek-harness §1.3 规则3):
	// 见 stripCrossTurnReasoning 注释。仅清理无 tool_calls 的 assistant,
	// 工具循环内 (messages 局部变量) 不受影响。
	cleaned := stripCrossTurnReasoning(messages[:len(a.history)])
	copy(messages, cleaned)
	// v2.2: BM25 动态召回既往经验注入当前用户轮次(不进 system → 缓存前缀恒定)。
	// v3.0 缓存铁律 (deepseek-harness 回放逐字节一致): userContent 是"线上真实发送
	// 的用户轮次"。历史落盘必须原样存 userContent —— 若存 plain input, 下一轮回放
	// 的历史 [user(input)] 与上一轮线上 [user(recalled+input)] 首 token 即不同,
	// 导致 system 之后的全部历史前缀断裂 → 跨轮/跨重启全量 miss (实测: 命中恒等于
	// system 长度, 历史永不命中)。wire ≡ f(history) 后, 回放天然命中。
	userContent := input
	if a.cfg != nil {
		if recalled, _ := RecallMemory(a.cfg.WorkDir, input, 5); recalled != "" {
			userContent = recalled + "\n" + input
		}
	}
	userMsg := ChatMessage{Role: "user", Content: userContent}
	// ── 识图 (vision): 输入含图片路径 → 读文件 base64 挂当轮请求 ──
	// 图片只在当轮内存 (json:"-" 不落盘 history → 重放/压缩路径安全)。
	images, imgErr := detectImages(input, a.cfg.WorkDir)
	// vision.go 契约: "路径存在而读取失败"必须报错, 不得静默丢图。
	// 此前此处丢弃 error → 用户以为已发图, 实际按纯文本处理且无任何提示。
	if imgErr != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "🖼"), dim(fmt.Sprintf("图片读取失败, 本轮按纯文本处理: %v", imgErr)))
	}
	if len(images) > 0 {
		// 负能力门控 (DSH): 无视觉模型 (ModelVision 空) → 禁止挂图, 降级 text-only,
		// 避免把图发给非视觉模型被服务端 400。有视觉模型才挂图。
		if visionCapable(a.cfg) {
			userMsg.Images = images
			fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.blue, "🖼"), dim(fmt.Sprintf("识图 %d 张 → %s", len(images), a.cfg.ModelVision)))
		} else {
			fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "🖼"), dim(fmt.Sprintf("检出 %d 张图但未配置视觉模型, 已降级为纯文本", len(images))))
		}
	}
	messages[len(a.history)] = userMsg
	return messages, userContent, images
}
