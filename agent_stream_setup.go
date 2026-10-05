package main

// ─── 一轮任务的状态、装配与主循环 ───────────────────────────────
// B3 批5: 自 RunStream 整体迁出 (原 agent.go:547-793)。
//
// 为什么连主循环一起搬: 装配(前置检查/建消息)、状态(模型路由/拦截计数/无剑
// 状态)、主循环三者共享同一组可变变量。只搬其中一段, 剩下的仍要在 RunStream
// 里声明这些变量并逐个传给子组件 —— 迁移成本不减, RunStream 依然超 F3 的
// 150 行。整体迁移后 RunStream 只剩 defer 骨架(生命周期必须挂在它自己栈上,
// 子函数返回即触发, 不可外迁), 而这里每个函数都在限额内。
//
// 闭包 → 方法 (本批的语义核心): 原 escalateOnLoop / finishStuck / nswIntervene
// 三个闭包, 其捕获的局部变量(escalated / curModel / loopStrikes / lastRawOutput
// / nswRounds / messages / verifyStrikes)全部提升为 runState 字段。闭包捕获与
// 结构体字段本就同构, 提升后:
//   1. "改的是副本却没写回" 这类缺陷在类型层面不可能发生 —— 前几批靠指针契约
//      逐个防的缺陷类别, 这里由结构消除;
//   2. 子组件(turnFinalizer / progressTracker)仍按原契约持指针, 指向本结构体
//      字段的地址, 契约不变。
//
// 行为不变: 迁移只改变状态的存放位置, 不改变任何判定条件、执行顺序与副作用。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// runState 承载一轮任务的全部共享状态。
//
// 字段分三类: 只读输入(构造后不变) / 跨轮可变状态 / 子组件。
// 子组件持本结构体字段的指针 —— rs 分配在堆上, 字段地址稳定, 故 &rs.messages
// 之类的指针在 runState 整个生命周期内有效。
type runState struct {
	agent  *AgentRunner
	runCtx context.Context

	// ── 只读输入 (构造后不变) ──
	userContent string // 本轮用户消息(含召回块), 与线上发送内容逐字节一致
	tools       []json.RawMessage
	taskAnchor  string    // 原始任务锚点, 注入每条工具结果防目标漂移
	turnStart   time.Time // 本轮开始时刻(统计用)

	// ── 跨轮可变状态 ──
	messages       []ChatMessage
	callHistory    map[string]int // 工具调用哈希 -> 次数, 循环拦截(Detector A)用
	curModel       string         // 当前模型(可被升级/识图改写)
	escalated      bool           // 是否已从 flash 升级到 pro
	visionRetried  bool           // 图片被服务端拒(unsupported image)后降级纯文本, 只截一次
	loopStrikes    int            // 已拦截次数(跨轮累计, 成功不清零 —— 与 consecutiveFails 不同)
	lastRawOutput  string         // 本回合最后一次执行的原始输出(成功/失败都算)
	verifyStrikes  int            // 虚报强干预计数(上限 maxVerifyStrikes)
	nswRounds      int            // 无剑干预轮数(上限 maxNSWRounds)
	maxLoopStrikes int
	maxNSWRounds   int

	// ── 子组件 (构造一次, 每轮复用) ──
	fin  *turnFinalizer
	prog *progressTracker
}

// newRunState 完成前置检查、消息装配与全部跨轮状态初始化。
//
// 对应原 RunStream 的 547-686 行。任何返回 error 的情形都发生在主循环开始前
// (取消 / 装配失败), 此时尚未产生副作用, 直接上抛即可。
func (a *AgentRunner) newRunState(input string, runCtx context.Context) (*runState, error) {
	select {
	case <-runCtx.Done():
		return nil, runCtx.Err()
	default:
	}

	turnStart := time.Now()
	logEvent(EvTurnStarted, turnDetail(input), nil)
	// 躯壳自检: 每轮增量扫描错误事件, 新问题超阈值才 1 句话提示
	maybePrintHealthHint(a.cfg.WorkDir)

	// v3.1 (DSH project 机制): 记忆/待办/档案索引 diff 追加, 前缀缓存恒定
	a.syncDynamicTails()

	// 固定头一致性断言 (死程序判定前端截断, 不靠 LLM 事后复盘)
	// headCached 机制下 buildFixedHead 恒返回首值缓存, 长度/内容应恒等于 a.headLen。
	// 一旦未来改动破坏"固定头恒定"导致前缀漂移, 此断言在判定边界当场告警 (前置拦截)。
	if !a.verifyHeadInvariant() {
		fmt.Fprintf(os.Stderr, "%s 固定头一致性断言失败: headLen=%d, 实际=%d (前缀缓存将断裂, 需修复 buildFixedHead/headLen 同步)\n",
			color(ansi.red, "💥"), a.headLen, len(a.buildFixedHead()))
		recordCacheStat(a.cfg.Model, 0, 0, 0, currentSystemHash, true)
	}

	// Build messages: system + history + current user message
	messages, userContent, images := a.buildStreamMessages(input)

	// ── 动态模型路由 ──
	// 简单任务→flash(便宜)，复杂任务→pro(强)。失败时自动升级：
	// flash 连续失败 1 次后升级到 pro 重试，防止 flash 能力不足导致任务失败。
	// 2026-09-11 V4.1 起 Flash==Pro(同一规范名 deepseek-flash)，升级分支自动短路。
	rs := &runState{
		agent:  a,
		runCtx: runCtx,

		userContent: userContent,
		tools:       []json.RawMessage{ForgeToolSchema()},
		taskAnchor:  truncateAnchor(input),
		turnStart:   turnStart,

		messages:    messages,
		callHistory: make(map[string]int), // hash -> count

		curModel:       pickModel(a.cfg, input),
		maxLoopStrikes: effectiveMaxStrikes(a.cfg.MaxLoopStrikes),
		maxNSWRounds:   2, // 防 LLM 反复生成锚点导致死循环
	}

	// 识图 → 强制视觉模型 (deepseek-flash: V4.1-Flash 是唯一支持图像理解的模型);
	// 视觉模型 ≠ ModelFlash, 循环拦截升级逻辑不会把它误升级成 pro。
	if len(images) > 0 && a.cfg.ModelVision != "" {
		rs.curModel = a.cfg.ModelVision
	}
	a.stats.setModel(rs.curModel)

	// 收尾出口(agent_stream_final.go)与无进展检测(agent_stream_progress.go)
	// 各持本结构体字段的指针 —— 地址稳定, 回写遗漏在类型层面不可能发生。
	rs.fin = &turnFinalizer{
		agent:         a,
		messages:      &rs.messages,
		userContent:   rs.userContent,
		turnStart:     rs.turnStart,
		verifyStrikes: &rs.verifyStrikes,
		nswRounds:     &rs.nswRounds,
		nswIntervene:  rs.nswIntervene,
	}
	rs.prog = &progressTracker{
		maxLoopStrikes: rs.maxLoopStrikes,
		escalateOnLoop: rs.escalateOnLoop,
		loopStrikes:    &rs.loopStrikes,
		lastRawOutput:  &rs.lastRawOutput,
	}
	return rs, nil
}

// escalateOnLoop 拦截时升级模型: flash → pro, 让更强模型打破僵局 (只升级一次)。
// 原为 RunStream 内闭包, 捕获 escalated / curModel / a.cfg。
func (rs *runState) escalateOnLoop() {
	if !rs.escalated && rs.curModel == rs.agent.cfg.ModelFlash &&
		rs.agent.cfg.ModelPro != "" && rs.agent.cfg.ModelPro != rs.agent.cfg.ModelFlash {
		rs.escalated = true
		rs.curModel = rs.agent.cfg.ModelPro
		rs.agent.stats.setModel(rs.curModel)
		fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⬆"), dim("检测到循环, 升级到 "+rs.curModel+" 打破僵局"))
	}
}

// finishStuck 自动收尾: 带已取得进展结束本轮。
// 返回 nil = 正常完成路径(检查点照常保存, 非交互模式不触发 os.Exit(1))。
// 原为 RunStream 内闭包, 捕获 loopStrikes / lastRawOutput / userContent / turnStart。
func (rs *runState) finishStuck() error {
	final := stuckExitMessage(rs.loopStrikes, rs.lastRawOutput)
	fmt.Fprintf(os.Stderr, "\n%s %s\n\n", color(ansi.yellow, "⚠"), final)
	rs.agent.history = append(rs.agent.history,
		ChatMessage{Role: "user", Content: rs.userContent},
		ChatMessage{Role: "assistant", Content: final},
	)
	rs.agent.trimHistory()
	rs.agent.stats.addTurn(time.Since(rs.turnStart))
	logEvent(EvError, "循环自动收尾", map[string]int{"strikes": rs.loopStrikes})
	return nil
}

// nswIntervene 无剑干预统一入口 (20260912 缺陷O): 收尾出口有两处(纯文本回复 /
// 工具碎片全无效降级为纯文本), 此前只有第一处接了无剑 —— 两处不对称会让
// "碎片无效"路径上的算式错值静默漏过。收进同一方法保证两处行为逐字节一致。
// 返回非空 = 应把 asst+fb 追加进 messages 并 continue。
//
// ── 无剑求值感知 (FORGE_NOSWORD=1): 死程序嗅探求值锚点, 反馈注入让 LLM 修正继续生成 ──
func (rs *runState) nswIntervene(asst string) string {
	if rs.nswRounds >= rs.maxNSWRounds {
		return ""
	}
	// 拒绝权 (20260920 无剑二期): 标记内不是纯算式 → 死程序拒绝执行并回告模型。
	// 独立开关 FORGE_NSW_EXPR, 不受 FORGE_NOSWORD 限制。
	if nswExprEnabled() {
		if fb := nswExprRejectText(asst); fb != "" {
			rs.nswRounds++
			return autoInjectEnvelope("算式求值校验", fb)
		}
	}
	if !nswEnabled() {
		return ""
	}
	// 复述过滤 (20260911 缺陷M): 原文已含同形正确结论的锚点不再反馈, 切断
	// "引用算式 -> 反馈 -> 再引用"的自激循环; 写错/未给结论的照常反馈。
	fb, fresh, total := nswFeedbackTextFresh(asst)
	if fb == "" {
		return ""
	}
	rs.nswRounds++
	nswAudit(rs.agent, asst, fresh, total)
	// 缺陷P (20260912): 注入必须自带来源信封, 否则模型把死程序反馈误当用户发言。
	return autoInjectEnvelope("算式求值校验", fb)
}

// runTurns 主循环: 无限推进直到任务完成 / 无进展收尾 / 连续失败中止 / 上下文取消。
// 对应原 RunStream 的 687-793 行, 逐语句迁移, 仅将局部变量引用改为 rs 字段。
//
// 回合数限制已取消：for{...} 永不因轮数退出。防"死循环"的安全网:
//  1. 循环拦截(Detector A/B) — 语义归一化重复调用 + 连续相同输出, 拦截 N 次
//     (默认4)仍无进展 → 带已取得进展自动收尾, 不无限转;
//  2. consecutiveFails — 连续铸剑炉调用失败中止 (默认5次, 非瞬态);
//  3. unknownTools     — 未知工具幻觉中止;
//  4. rs.runCtx.Done() — 上下文取消 (Ctrl+C / 超时)。
//
// 真正的任务永远跑到完成; 只有确认无进展/失败/幻觉/取消时才终止。
func (rs *runState) runTurns() error {
	a := rs.agent
	runCtx := rs.runCtx

	for turn := 0; ; turn++ {
		select {
		case <-runCtx.Done():
			return runCtx.Err()
		default:
		}

		// ── 单轮流式请求 + 传输层错误处理 ──
		// B3 批2: 提取为 streamTurn.request (见 agent_stream_turn.go)。
		// stRetry 承接原内联的两个 continue(图片降级 / 升级模型重试):
		// for 的 post 语句(turn++)与循环头取消检查照常执行, 语义等价。
		st := &streamTurn{
			agent:         a,
			runCtx:        runCtx,
			userContent:   rs.userContent,
			tools:         rs.tools,
			messages:      &rs.messages,
			curModel:      &rs.curModel,
			escalated:     &rs.escalated,
			visionRetried: &rs.visionRetried,
			toolCallAccum: []ToolCall{},
		}
		stRetry, stErr := st.request()
		if stErr != nil {
			return stErr
		}
		if stRetry {
			continue
		}
		toolCallAccum := st.toolCallAccum

		// No tool calls -> final response
		if len(toolCallAccum) == 0 {
			asst := extractAssistantText(&st.assistantContent, &st.reasoningBuf)
			// B3 批3: 无剑干预 / 虚报检测 / 正常收尾 合并为单一收尾入口
			// (agent_stream_final.go)。两处重复块的不对称已由结构消除。
			if rs.fin.finalize(asst, st.reasoningBuf.String(), "plain") {
				continue
			}
			return nil
		}

		// Filter out unmerged stream fragments: a raw delta slice (which happens when
		// finish_reason was stop/length instead of tool_calls) contains per-chunk entries
		// with empty ID or empty function name. Treating those as independent tool calls
		// makes the agent execute partial arguments and produces tool responses whose
		// tool_call_id cannot be matched back to assistant.tool_calls → API 400.
		toolCallAccum = filterValidToolCalls(toolCallAccum)
		if len(toolCallAccum) == 0 {
			// All fragments were invalid — treat as a plain (non-tool) response
			asst := extractAssistantText(&st.assistantContent, &st.reasoningBuf)
			// B3 批3: 与纯文本出口共用同一收尾入口, "frag" 仅作审计区分。
			if rs.fin.finalize(asst, st.reasoningBuf.String(), "frag") {
				continue
			}
			return nil
		}

		// Add assistant message with tool calls (text stripped — 生成与执行分离原则)
		// Per axiom 2: LLM text alongside tool calls is premature analysis that crowds out
		// the tool call itself. Strip it. The LLM will analyze AFTER seeing the result.
		//
		// 剥离回执 + 埋点 (20261004): 剥离此前既不可见也不可测 —— 主人看到的文字
		// 与模型历史不一致却零提示(会被误读为"模型说过这话"), 剥离次数/字符数也
		// 零留痕。stripAudit 一次完成两件事: 落审计 + 返回被剥离字符数(判与显同源)。
		if n := stripAudit(a, st.assistantContent.String(), len(toolCallAccum), turn); n > 0 {
			fmt.Fprintf(os.Stderr, "  %s %s\n", dim("✂"),
				dim(fmt.Sprintf("已剥离工具轮文字 %d 字 (仅你可见, 模型下一轮看不到)", n)))
		}
		rs.messages = append(rs.messages, ChatMessage{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: st.reasoningBuf.String(),
			ToolCalls:        toolCallAccum,
		})

		// Execute each tool call (B3 批1: 提取为 toolLoop.runToolCalls, 见 agent_stream.go)
		if tlErr := (&toolLoop{
			agent:          a,
			runCtx:         runCtx,
			callHistory:    rs.callHistory,
			userContent:    rs.userContent,
			turn:           turn,
			taskAnchor:     rs.taskAnchor,
			maxLoopStrikes: rs.maxLoopStrikes,
			escalateOnLoop: rs.escalateOnLoop,
			finishStuck:    rs.finishStuck,
			messages:       &rs.messages,
			curModel:       &rs.curModel,
			loopStrikes:    &rs.loopStrikes,
			lastRawOutput:  &rs.lastRawOutput,
		}).runToolCalls(toolCallAccum); tlErr != nil {
			return tlErr
		}

		// ── 无进展检测 (Detector B): 连续相同输出(不同代码但结果不变) → 拦截干预 ──
		// 拦截只是提示换策略 + 升级模型, 不中止任务; 连续多次仍无进展才自动收尾。
		// B3 批4: 提取为 progressTracker.check (见 agent_stream_progress.go)。
		intervene, stuck := rs.prog.check()
		if stuck {
			return rs.finishStuck()
		}
		if intervene != "" {
			rs.messages = append(rs.messages, ChatMessage{Role: "user", Content: intervene})
		}

		// Trim accumulated turn messages: a long task can grow `messages` without
		// bound (assistant+tool pairs per turn). Drop the oldest COMPLETE turns so
		// the assistant(tool_calls) ↔ tool response pairing is never broken.
		// messages[0] (system / history head) is preserved.
		// B3 批4: 阈值与理由见 maxTurnMessages (agent_stream_progress.go)。
		rs.messages = trimTurnMessagesIfNeeded(rs.messages)
	}
}
