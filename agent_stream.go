package main

// ─── 工具执行循环 ───────────────────────────────────────────────
// B3 批1: 自 RunStream 抽出 (原 agent.go:807-1007, 201 行)。
//
// 关键设计: 与 RunStream 共享的可变状态一律走指针。
// 这段逻辑原本内联在 RunStream 主循环内, 需读写 20+ 个外层变量。若用值字段
// 传状态, 忘记回写时编译器不会报错 —— 而 messages 漏回写会静默丢弃工具结果
// (消息序列错乱, 现有测试未必抓得到)。指针让"回写遗漏"这类缺陷在类型层面
// 不可能发生: 循环内的修改直接作用于 RunStream 的局部变量。
//
// 三个纯私有字段(consecutiveFails/unknownTools/lastBlockHash)在块外无任何
// 引用, 因此完全归本结构体所有, 无需回写。

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// maxRepeatedCalls 同一语义哈希的工具调用累计 N 次即判为重复循环。
// B3 批1: 原为 RunStream 内局部 const, 块外无引用, 随循环一并迁出。
const maxRepeatedCalls = 3

// toolLoop 承载一轮工具执行循环的全部上下文。
type toolLoop struct {
	agent *AgentRunner

	// ── 只读输入 ──
	runCtx         context.Context
	callHistory    map[string]int
	userContent    string
	turn           int
	taskAnchor     string
	maxLoopStrikes int
	escalateOnLoop func()
	finishStuck    func() error

	// ── 与 RunStream 共享的可变状态(指针, 防回写遗漏) ──
	messages      *[]ChatMessage
	curModel      *string
	loopStrikes   *int
	lastRawOutput *string

	// ── 本循环私有状态(块外无引用, 无需回写) ──
	consecutiveFails int
	unknownTools     int
	lastBlockHash    string
}

// runToolCalls 依次执行本轮的每个工具调用。
// 返回非 nil 表示应中止整个 RunStream 并返回该错误。
func (t *toolLoop) runToolCalls(toolCallAccum []ToolCall) error {
	for _, tc := range toolCallAccum {
		if err := t.executeOneToolCall(tc); err != nil {
			return err
		}
	}
	return nil
}

// executeOneToolCall 处理单个工具调用: 前置守卫(工具名/参数/重复检测) → 执行 →
// 结果汇报 → 输出落盘与识图接续。
//
// B3 批1: 原为 runToolCalls 循环体内联代码。原循环体内的 continue(守卫命中/
// 拦截命中)在此等价改写为 return nil —— 两者语义相同(结束本次迭代, 继续下一个)。
func (t *toolLoop) executeOneToolCall(tc ToolCall) error {
	if tc.Function.Name != ForgeToolName {
		// Unknown tool -> return error to LLM. A single hallucinated tool
		// name is not a code failure, but a model that keeps inventing tools
		// must terminate: otherwise the agent loops forever.
		t.unknownTools++
		errMsg := buildUnknownToolMsg(tc.Function.Name)
		*t.messages = append(*t.messages, ChatMessage{
			Role: "tool", ToolCallID: tc.ID, Content: errMsg,
		})
		maxFails := effectiveMaxFails(t.agent.cfg.MaxConsecutiveFails)
		if abort, _ := abortUnknownTools(t.unknownTools, maxFails); abort {
			t.agent.history = append(t.agent.history, ChatMessage{Role: "user", Content: t.userContent})
			t.agent.trimHistory()
			logEvent(EvError, "未知工具幻觉", map[string]int{"count": t.unknownTools})
			return fmt.Errorf("连续 %d 次调用未知工具（工具幻觉），已中止", t.unknownTools)
		}
		return nil
	}

	// Parse params
	params, parseErr := parseForgeParams(tc.Function.Arguments)
	if parseErr != nil {
		*t.messages = append(*t.messages, ChatMessage{
			Role: "tool", ToolCallID: tc.ID,
			Content: fmt.Sprintf("参数解析失败: %v。请检查 JSON 格式。有效的参数: action(必需), code(必需), lang(可选), input(可选)。", parseErr),
		})
		// Don't increment — JSON formatting glitch, not a code logic failure
		return nil
	}

	// Detect repeated calls (loop detection, 语义归一化哈希)
	callHash := hashCall(params.Code, params.Lang, params.Input)
	blocked, callCount := checkRepeatedCall(callHash, t.callHistory, maxRepeatedCalls)
	if blocked {
		*t.messages = append(*t.messages, ChatMessage{
			Role: "tool", ToolCallID: tc.ID,
			Content: repeatReminder(callCount, params.Code),
		})
		// 按"语义家族"计拦截(同一归一化哈希只计一次), 避免升级 pro 后
		// 的验证性重跑被连续计分误杀。拦截≠失败中止, 只是干预。
		if callHash != t.lastBlockHash {
			t.lastBlockHash = callHash
			*t.loopStrikes++
			t.escalateOnLoop()
			fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⚠"), dim(fmt.Sprintf("重复调用拦截 %d/%d, 已要求换策略", *t.loopStrikes, t.maxLoopStrikes)))
			if *t.loopStrikes >= t.maxLoopStrikes {
				return t.finishStuck()
			}
		}
		return nil
	}

	// Execute — display code with syntax highlighting (anti-hallucination)
	// 流式显示已完整打印过同一段代码 → 不再整块重打(否则同段代码显示两遍)。
	// 流式未产出(无 code 字段/解码失败/被上限截断)时仍走整块兜底。
	streamed := t.agent.streamedCode
	t.agent.streamedCode = ""
	if streamed != params.Code {
		displayToolCode(params.Code, params.Lang)
	}
	fmt.Fprintf(os.Stderr, "  %s %s\n", dim("⚙"), dim("执行中…"))
	toolStart := time.Now()
	logEvent(EvToolCalled, params.Lang, map[string]string{"code": truncateCN(params.Code, 300), "lang": params.Lang})
	// 把本次 run 的 ctx 交给 Forge: 第一次 Ctrl+C 即可中断正在执行的工具
	t.agent.forge.SetRunCtx(t.runCtx)
	output, result, execErr := t.agent.forge.Build(params.Code, params.Lang, params.Input)
	t.agent.forge.SetRunCtx(nil)
	// 用户按 Ctrl+C 取消了本次 run: 立即结束, 不计为工具失败
	// (否则取消会被当成失败计分, 进而误判"连续失败中止")
	if cerr := t.runCtx.Err(); cerr != nil {
		fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⏹"), dim("已取消本次执行"))
		return cerr
	}
	toolDuration := time.Since(toolStart)
	*t.lastRawOutput = output // 供无进展检测: 成功与失败的输出都算
	toolOK := execErr == nil && result != nil && result.OK && result.ExitCode == 0
	logEvent(EvToolResult, params.Lang, map[string]interface{}{"ok": toolOK, "duration_ms": toolDuration.Milliseconds()})

	// 失败判定含 ExitCode: "打印后非零退出"(OK=true 但退出码非0)是真实运行
	// 失败, 必须计分——否则模型反复执行同样失败的代码永不触发中止(旧漏洞)。
	if err := t.reportToolOutcome(params, output, result, execErr, toolDuration); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Remember last tool output for /last
	t.agent.lastOutMu.Lock()
	t.agent.lastOut = output
	t.agent.lastOutMu.Unlock()

	// Goal anchor
	// 大输出落盘引用: 超阈值(12000 rune)时头尾摘要+全文落盘, 否则走 pruneToolOutput
	warnedOutput := goalAnchorRef(output, t.taskAnchor, t.turn, t.agent.cfg.WorkDir)

	*t.messages = append(*t.messages, ChatMessage{
		Role: "tool", ToolCallID: tc.ID, Content: warnedOutput,
	})
	// ── 工具结果识图接续器 (browser 截图落盘后模型需要"看到"图) ──
	// 工具执行产出图片文件(如 browser gate screenshot 落盘 .png)后, 模型拿到
	// 的是文本路径, 看不到图。复用 detectToolImages 从工具输出提取真实存在的
	// 图片 → base64 挂一条带 Images 的 user 辅助消息 → 切视觉模型。图片仅在
	// 当轮 *t.messages 内存 (Images json:"-" 不落盘 history), 重放/缓存/压缩安全。
	toolImgs := detectToolImages(output, t.agent.cfg.WorkDir)
	if len(toolImgs) > 0 && visionCapable(t.agent.cfg) {
		imgMsg := ChatMessage{
			Role:    "user",
			Content: "工具结果包含图片文件, 已作为截图注入, 请结合截图进行分析:",
			Images:  toolImgs,
		}
		*t.messages = append(*t.messages, imgMsg)
		if t.agent.cfg.ModelVision != "" {
			*t.curModel = t.agent.cfg.ModelVision
			t.agent.stats.setModel(*t.curModel)
		}
		fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.blue, "🖼"), dim(fmt.Sprintf("工具截图识图 %d 张 → %s", len(toolImgs), t.agent.cfg.ModelVision)))
	}
	return nil
}

// reportToolOutcome 汇报一次工具执行结果: 失败计分/中止判定/成功统计/输出展示。
// 返回非 nil 表示连续失败达阈值, 应中止整个 RunStream。
func (t *toolLoop) reportToolOutcome(params ForgeParams, output string, result *ForgeGateResult, execErr error, toolDuration time.Duration) error {
	if execErr != nil || (result != nil && (!result.OK || result.ExitCode != 0)) {
		// Classify failure severity. Transient errors (timeouts, tool-not-found,
		// parse failures) get a lighter penalty — they're often environmental.
		isTransient := classifyTransient(result)
		if isTransient {
			t.consecutiveFails += 0 // don't penalize environmental failures
		} else {
			t.consecutiveFails++
		}
		t.agent.stats.addToolFail(toolDuration)

		errDetail := ""
		if result != nil && result.Error != "" {
			errDetail = result.Error
		} else if execErr != nil {
			errDetail = execErr.Error()
		}

		stage := "?"
		if result != nil {
			stage = result.Stage
		}

		maxFails := effectiveMaxFails(t.agent.cfg.MaxConsecutiveFails)
		if shouldAbortOnFails(t.consecutiveFails, maxFails) {
			fmt.Fprintf(os.Stderr, "  %s %d consecutive failures (non-transient), aborting\n",
				color(ansi.red, "✗"), t.consecutiveFails)
			t.agent.history = append(t.agent.history, ChatMessage{Role: "user", Content: t.userContent})
			t.agent.trimHistory()
			logEvent(EvError, "连续失败中止", map[string]int{"count": t.consecutiveFails})
			return fmt.Errorf("连续 %d 次铸剑炉调用失败（非瞬时错误），已中止", t.consecutiveFails)
		}

		if len(errDetail) > 150 {
			errDetail = errDetail[:150] + "..."
		}
		fmt.Fprintf(os.Stderr, "  %s %s %s · %s\n",
			langEmoji(params.Lang),
			color(ansi.red, "✗ FAIL"),
			color(ansi.yellow, fmt.Sprintf("[%s]", stage)),
			dim(fmt.Sprintf("%dms", toolDuration.Milliseconds())))

		if errDetail != "" {
			fmt.Fprintf(os.Stderr, "  %s %s\n", dim("  ->"), color(ansi.red, errDetail))
		}
	} else {
		t.consecutiveFails = 0
		t.agent.stats.addToolOK(toolDuration)

		outLen := 0
		if result != nil {
			outLen = len(result.Stdout)
		}
		fmt.Fprintf(os.Stderr, "  %s %s %s · %s · %s\n",
			langEmoji(params.Lang),
			color(ansi.green, "✓ OK"),
			dim(fmt.Sprintf("%s", strings.ToUpper(params.Lang))),
			dim(fmt.Sprintf("%dms", toolDuration.Milliseconds())),
			dim(formatSize(outLen)))

		// Show output lines (anti-hallucination: user sees actual compiler output)
		outStr := strings.TrimSpace(output)
		if outStr != "" {
			outLines := strings.Split(outStr, "\n")
			// 默认全量显示(六西格玛控制阶段: maxShow<=0 即不截断), 与
			// displayToolCode 的 codeMaxLines 同口径; 需要截断时设环境变量。
			showOut := codeMaxLines()
			if showOut <= 0 || showOut > len(outLines) {
				showOut = len(outLines)
			}
			maxCols := codeMaxCols()
			for i := 0; i < showOut; i++ {
				ol := outLines[i]
				if maxCols > 0 {
					if r := []rune(ol); len(r) > maxCols {
						ol = string(r[:maxCols]) + "..."
					}
				}
				fmt.Fprintf(os.Stderr, "  %s %s\n", dim("│"), ol)
			}
			if len(outLines) > showOut {
				fmt.Fprintf(os.Stderr, "  %s %s\n", dim("╰─"), dim(fmt.Sprintf("... +%d more lines", len(outLines)-showOut)))
			}
		}
	}
	return nil
}
