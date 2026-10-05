package main

// ─── 回合收尾出口 (本回合无有效工具调用) ─────────────────────────
// B3 批3: 自 RunStream 抽出 (原 agent.go:700-729 与 737-766, 两段逐字节重复)。
//
// 为什么必须合并: 两个出口在语义上是同一件事 —— "本回合没有可执行的工具调用,
// 该收尾了"。此前是两段 30 行的重复代码, 唯一差异是 nswProbeAudit 的 source
// 标签 ("plain" = 模型直接给纯文本 / "frag" = 流式碎片全无效降级为纯文本)。
// 20260912 缺陷O 已经证明两处不对称的代价: "碎片无效"路径上的算式错值会静默
// 漏过 —— 当时靠手工补一份拷贝修复。合并成单一入口后, "只改一处"在结构上不
// 可能发生, 对称性不再依赖人的记性。
//
// 指针契约 (同 toolLoop / streamTurn): messages / verifyStrikes / nswRounds
// 都是 RunStream 的局部变量。用指针持有, 使"收尾逻辑改了局部副本却没写回"
// 这类缺陷在类型层面不可能发生 (messages 漏写回 = 反馈丢失, 模型收不到)。

import "time"

// turnFinalizer 承载收尾判定所需的全部共享状态。
type turnFinalizer struct {
	agent       *AgentRunner
	messages    *[]ChatMessage
	userContent string
	turnStart   time.Time

	// ── 跨轮累计计数(指针, 防回写遗漏) ──
	verifyStrikes *int // 虚报强干预计数(上限 maxVerifyStrikes)
	nswRounds     *int // 无剑干预轮数(上限 maxNSWRounds)

	// nswIntervene 无剑干预入口 (由 RunStream 注入其闭包)。
	// 以函数字段注入而非直接调用, 是为了让本文件不依赖该闭包的实现位置 ——
	// 若后续把它也迁出 RunStream, 只需改注入点。
	nswIntervene func(asst string) string
}

// finalize 处理"本回合无有效工具调用"的收尾。
//
// 返回 true  = 已向 messages 注入反馈, 调用方应 continue 让模型修正重试;
// 返回 false = 已正常收尾(history/trim/stats 已落), 调用方应 return nil。
//
// reasoning 是本回合的 reasoning_content (原为 st.reasoningBuf.String())。
// source 只进审计: "plain"(纯文本回复) / "frag"(碎片全无效降级)。
func (f *turnFinalizer) finalize(asst, reasoning, source string) bool {
	// 度量铁律埋点 (20261004): 必须早于下面两个"注入反馈后 continue"的分支 ——
	// 被虚报检测/无剑反馈拦下的那版回复, 恰恰是"声称完成 + 统计结论"最集中的形态,
	// 放在 return 之后等于把最该看的样本丢掉 (短路盲区)。与 nswProbeAudit 的位置
	// 差异是有意的: 那个只该记"最终收尾", 故留在原位。
	metricClaimAudit(f.agent, asst, *f.messages)

	// ── 无剑求值感知 (FORGE_NOSWORD=1): 死程序嗅探 asst 中的求值锚点,
	// 命中则把稳定反馈作为 user 消息注入, continue 让 LLM 看到反馈后
	// 修正继续生成 (公理二: 判据由死程序把守, LLM 据此自然调整)。──
	if fb := f.nswIntervene(asst); fb != "" {
		*f.messages = append(*f.messages,
			ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoning},
			ChatMessage{Role: "user", Content: fb},
		)
		return true
	}

	// 虚报检测 gate (增强版, 无剑闭环): 完成态声称+无工具证据 -> 注入真实状态证据, 强制模型修正
	if verifyClaimEnabled() && *f.verifyStrikes < maxVerifyStrikes && detectUnverifiedClaim(asst, *f.messages) {
		(*f.verifyStrikes)++
		*f.messages = append(*f.messages,
			ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoning},
			ChatMessage{Role: "user", Content: verifyInterventionMsg(runVerification())},
		)
		return true
	}

	// ── 正常收尾: 带已取得进展结束本轮 (返回 nil = 正常完成路径,
	// 检查点照常保存, 非交互模式不触发 os.Exit(1)) ──
	nswProbeAudit(f.agent, asst, *f.nswRounds, source)
	f.agent.history = append(f.agent.history,
		ChatMessage{Role: "user", Content: f.userContent},
		ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoning},
	)
	f.agent.trimHistory()
	f.agent.stats.addTurn(time.Since(f.turnStart))
	return false
}
