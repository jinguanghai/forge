// ══════════════════════════════════════════════════════════════
// agent_trim.go — 历史裁剪与摘要压缩: 硬裁剪 (trimHistory/trimTurnMessages) + 软压缩 (maybeCompact)。
// ══════════════════════════════════════════════════════════════

package main

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ─── History management ─────────────────────────────────────

// contextBudgetUsed 返回「对话预算」已用量: 固定头(system+记忆+折叠)之外的 token 估算。
//
// 为什么必须剔除固定头 (2026-10-02 修正, 主人报「重启显示剩7%、聊两轮变0%」):
//
//	estimateTokens(a.history) 把 system 提示 + 记忆锚点 + 折叠索引一并计入。实测真实
//	会话固定头 = 18749 token (system 3764 + 记忆 14687 + 折叠 298), 而默认阈值 20000 ——
//	即"刚启动就已用掉 93.7% 预算", 于是状态栏一开机就报「剩7%」, 聊两轮(约2000 token)
//	就报「剩0%」。旧口径量的是"记忆有多大", 不是"注意力还剩多少"。
//
//	剔除后: 刚启动 = 剩100% (符合直觉); 而固定头本就受保护不可压缩 (maybeCompact 明确
//	跳过 head, 记忆不得被压缩掉), 与"可压缩的对话预算"无关, 分母/分子口径一致。
//
// 单一数据源: maybeCompact 的触发判据与状态栏显示共用本函数, 故「显示 0%」与
// 「触发压缩」是同一时刻 —— 不会出现"显示还剩一半却已经压缩了"的自相矛盾。
//
// 副作用(有意): 单轮上下文上限从 threshold 升到 base+threshold (约 3.9 万, 1M 窗口
// 无压力); 换来的是压缩真的能压到线下 —— 旧口径下固定头占 93.7%, 压缩后仍超阈值
// (审计 47 次真实压缩中 44 次如此), 等于白付摘要费 + 周期性丢细节。
//
// 2026-10-08 修正(第二处口径分叉): 旧实现直接估算 history[head:], 而真正发出去的
// 请求已被 stripCrossTurnReasoning 剥掉跨轮 reasoning_content —— 分子把「不发出去的
// 思考段」算进了已用量, 实测约占分子 30%, 使「注意力剩余」系统性偏低, 并连带让
// maybeCompact 提前触发。现改为估算「与上线字节同形」的消息 (见下方 return 处注释)。
//
// fail-safe: headLen 未初始化(<=0)兜底 1 (至少保护 system, 与 trimHistory 同规);
// headLen 越界(>len(history), 异常形态)时 clamp 到 len, 防切片 panic。
func contextBudgetUsed(a *AgentRunner) int {
	if a == nil {
		return 0
	}
	head := a.headLen
	if head < 1 {
		head = 1
	}
	if head > len(a.history) {
		head = len(a.history)
	}
	// 口径对齐「实际发送」(2026-10-08 修正): 发出去的请求在 buildStreamMessages
	// (agent_stream_loop.go) 里经 stripCrossTurnReasoning 剥离了跨轮 assistant 的
	// reasoning_content (deepseek-harness §1.3 规则3: 服务端跨轮不要求回传, 保留还会
	// 破坏前缀缓存匹配)。不发送的 token 不占注意力 —— 分子必须同步剥离, 否则口径分叉:
	//   ① 显示系统性偏低 (每轮终答的思考段被计入分子);
	//   ② maybeCompact 共用本函数 → 压缩被提前触发 (白付摘要费 + 周期性丢细节)。
	// 有 tool_calls 的 assistant 消息 reasoning 仍回传 (§1.3 规则2), strip 不动它,
	// 故这部分照常计入 —— 口径精确到「真正上线的字节」。
	return estimateTokens(stripCrossTurnReasoning(a.history[head:]))
}

// ─── DG-2 压缩预算: 触发线 / 目标线 / 按 token 反推削减段 (2026-10-11 主人拍板) ───
//
// 实测基线 (源 = gate_audit.jsonl, 取 before_tokens >= 20000 的真实长会话压缩 188 次):
// 触发时全量上下文中位 40382 token (占 1M 窗口仅 4.0%) / 对话累积 308 token/分钟 /
// 相邻压缩间隔中位 65 分钟 / 压缩达标率 (压到剩 >=50%) 仅 53.2%, 且 23.9% 压完仍剩 <30%
// (= 马上再撞线)。根因 = 压缩量按条数取 (rest/3 夹 4~12), 与「超出多少 token」无关。

// compactSummaryReserve 摘要消息自身占用的 token 预留 (<=400 字摘要 + 【会话摘要】前缀)。
// 反推削减量时必须计入 —— 摘要也进对话预算, 漏算即"压完还差一截"。
const compactSummaryReserve = 420

// compactTriggerPercent 触发线百分比 (对话预算占用)。零值兜底 70 ——
// 测试夹具直接构造 Config 时不会走 DefaultConfig, 故不能依赖字段已被填。
func compactTriggerPercent(cfg *Config) int {
	p := 70
	if cfg != nil && cfg.CompactTriggerPercent > 0 {
		p = cfg.CompactTriggerPercent
	}
	if p > 100 {
		p = 100
	}
	return p
}

// compactTriggerTokens 触发线 (对话预算 token): 占用达 TriggerPercent% 即动手。
// 主人原话「注意力少于 30% 就动作」→ 默认 70% 占用即触发 (剩 30% 动手)。
func compactTriggerTokens(cfg *Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.CompactTokenThreshold * compactTriggerPercent(cfg) / 100
}

// compactTargetPercent 目标线百分比 (对话预算占用)。零值兜底 50。
func compactTargetPercent(cfg *Config) int {
	p := 50
	if cfg != nil && cfg.CompactTargetPercent > 0 {
		p = cfg.CompactTargetPercent
	}
	if p > 100 {
		p = 100
	}
	return p
}

// compactTargetTokens 目标线 (对话预算 token): 压完应 <= 此值 (一次压到位, 避免冷却一过又撞线)。
func compactTargetTokens(cfg *Config) int {
	if cfg == nil {
		return 0
	}
	return cfg.CompactTokenThreshold * compactTargetPercent(cfg) / 100
}

// compactCutRange 按 token 目标反推最旧段区间 [start,end)。
//
// 为什么不再用「条数夹 4~12」: 实测 compressed_msgs 分布 10(877)/12(474)/7(438)/4(123)
// 全部落在夹取区间, 与「超出多少 token」无关 —— 于是 23.9% 的压缩压完仍剩 <30%
// (白付摘要费 + 丢细节 + 打断前缀缓存)。现改为: 从固定头之后最旧端累积 token,
// 直到削减量 >= (est - 目标线 + 摘要预留)。
//
// 两条护栏 (防"一次压光"):
//   - 至少保留 1 条对话消息 (请求里只剩固定头+摘要 = 上下文断裂);
//   - 单次至多压掉对话的 3/4 —— 削减需求远超对话量时 (极小阈值/配置失衡) 不被一次吃光。
func compactCutRange(a *AgentRunner, est int) (int, int) {
	head := a.headLen
	if head < 1 {
		head = 1
	}
	if head >= len(a.history) {
		return head, head
	}
	rest := len(a.history) - head
	need := est - compactTargetTokens(a.cfg) + compactSummaryReserve
	if need < 1 {
		need = 1
	}
	maxCut := rest * 3 / 4
	if maxCut > rest-1 {
		maxCut = rest - 1
	}
	if maxCut < 1 {
		return head, head
	}
	acc, n := 0, 0
	for i := head; i < len(a.history)-1 && n < maxCut; i++ {
		acc += estimateTokens(a.history[i : i+1])
		n++
		if acc >= need {
			break
		}
	}
	if n < 1 {
		return head, head
	}
	return completePairs(a.history, head, head+n)
}

// maybeCompact 历史压缩:
// 在 trimHistory 硬裁剪之前调用 (每 turn 至多一次)。触发条件:
//
//	① 开关开启 (AGENT_COMPACT_ENABLED, 默认1)
//	② 对话部分 token 估算 >= 触发线 = CompactTokenThreshold x TriggerPercent%
//	   (DG-2 2026-10-11 主人拍板: 「注意力少于 30% 就动作」→ 默认 70% 即动手。
//	    旧实现用满 100% 才压, 实测触发时全量上下文仅占 1M 窗口 4.0%, 压得太晚)
//	   口径是 contextBudgetUsed = 剔除固定头(system+记忆+折叠)之后的对话量,
//	   不是 estimateTokens(history) 全量 —— 见 contextBudgetUsed 注释 (2026-10-02 修正)
//	③ 距上次压缩 >= AGENT_COMPACT_MIN_TURNS 轮 (冷却防抖, 防前缀频繁变化)
//
// 动作: 从最旧端起按「削减到目标线所需 token」反推条数 (见 compactCutRange), 调 LLM
// 摘要成 <=400 字, 以 【会话摘要】 特殊消息放头部 (system 后), 丢弃被压缩段。
// 压后未达目标线 -> 额外落 compact_undershoot 审计行 (判据侧空白已补)。
//
// 失败兜底: 摘要出错/为空 → 不阻塞, 直接走原裁剪。
func (a *AgentRunner) maybeCompact() {
	if a.cfg == nil || !a.cfg.CompactEnabled {
		return
	}
	if a.compactCooldown > 0 {
		a.compactCooldown--
		return
	}
	if len(a.history) < 8 {
		return // 太短不值得压 (system + <7 条)
	}
	// 口径: 只算对话部分 (固定头不可压缩, 计入即"刚开机就 93% 满") —— 与状态栏
	// 「注意力剩余」共用 contextBudgetUsed, 显示与触发同源 (2026-10-02 修正)
	est := contextBudgetUsed(a)
	if est < compactTriggerTokens(a.cfg) {
		return
	}
	head := a.headLen // 跳过固定头 (system+记忆+折叠), v3.1 记忆不得被压缩掉
	if head < 1 {
		head = 1
	}
	start, end := compactCutRange(a, est)
	if end-start < 1 {
		return // 无可压段 (配对边界收拢后为空)
	}
	n := end - start
	// 配对安全选段 + system 前缀传给摘要器 (前缀缓存命中前置)
	oldest := append([]ChatMessage{a.history[0]}, a.history[start:end]...) // [0]=system 保持前缀
	beforeTokens := estimateTokens(a.history)
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	summary, err := a.llm.Summarize(ctx, oldest, 400)
	if err != nil || strings.TrimSpace(summary) == "" {
		appendAuditLine(a, map[string]interface{}{
			"event": "compact_failed", "ts": time.Now().Format(time.RFC3339Nano),
			"err": fmt.Sprint(err), "est_tokens": beforeTokens,
		})
		return // 降级: 不阻塞, 走原裁剪
	}
	summaryMsg := ChatMessage{Role: "user", Content: "【会话摘要】" + strings.TrimSpace(summary)}
	restMsgs := append([]ChatMessage{}, a.history[end:]...)
	newHist := make([]ChatMessage, 0, len(restMsgs)+head+1)
	newHist = append(newHist, a.history[:head]...) // 固定头整体保留 (缓存友好)
	newHist = append(newHist, summaryMsg)
	newHist = append(newHist, restMsgs...)
	afterTokens := estimateTokens(newHist)
	a.history = newHist
	a.compactCooldown = a.cfg.CompactMinTurns
	target := compactTargetTokens(a.cfg)
	convAfter := contextBudgetUsed(a) // 与触发同源口径: 对话部分 (剔除固定头)
	appendAuditLine(a, map[string]interface{}{
		"event":           "compact",
		"ts":              time.Now().Format(time.RFC3339Nano),
		"compressed_msgs": n,
		"summary_len":     utf8.RuneCountInString(summary),
		"before_tokens":   beforeTokens,
		"after_tokens":    afterTokens,
		"saved_tokens":    beforeTokens - afterTokens,
		"saved_pct":       fmt.Sprintf("%.1f%%", float64(beforeTokens-afterTokens)/float64(beforeTokens)*100),
	})
	// DG-2 判据落点 (2026-10-11): 压后必须到达目标线, 否则落 compact_undershoot 审计行。
	// 动机 = 判据侧空白: 此前没有任何判据盯「压后是否达标」, 抖动只能事后统计看见
	// (实测 1936 次压缩里 23.9% 压完仍剩 <30% → 冷却一过必再撞线)。
	if convAfter > target {
		appendAuditLine(a, map[string]interface{}{
			"event":             "compact_undershoot",
			"ts":                time.Now().Format(time.RFC3339Nano),
			"conv_after_tokens": convAfter,
			"target_tokens":     target,
			"overshoot_tokens":  convAfter - target,
			"compressed_msgs":   n,
		})
	}
}

func (a *AgentRunner) trimHistory() {
	a.maybeCompact() // 六西格玛立项20260815: 硬裁剪前先尝试摘要压缩 (背景保留+token节省)
	maxHist := a.cfg.MaxHistoryMessages
	if maxHist <= 0 {
		maxHist = 40
	}

	// 缓存友好裁剪: 从尾部成对删除完整轮次 [user, assistant],
	// 保持头部前缀 [system, u1, a1, ...] 稳定 —— DeepSeek 前缀缓存按 token 前缀
	// 完全匹配, 旧实现从头部删 history[1] 会使前缀在 system 之后断裂 → 后续请求
	// 全量 miss (实测: 前缀断裂命中率 0%)。
	headLen := a.headLen
	if headLen < 1 {
		headLen = 1 // 至少保护 system
	}
	for len(a.history) > maxHist && len(a.history) > headLen {
		n := len(a.history)
		if n <= headLen {
			break // 只剩固定头 (system+记忆+折叠), 不再删
		}
		// 尾部完整轮次 [user, assistant] 成对删
		if a.history[n-1].Role == "assistant" && n-2 >= headLen && a.history[n-2].Role == "user" {
			a.history = a.history[:n-2]
			continue
		}
		// 尾部孤立 user (失败/中止路径 append 的) 或异常形态: 删单条。
		// 孤立 user 前无配对 assistant, 删掉不破坏 API 的 user→assistant 配对。
		if n-1 >= headLen {
			a.history = a.history[:n-1]
		}
	}
}

// trimTurnMessages drops the oldest complete tool-call turns from msgs until
// len(msgs) <= maxTotal. It never breaks the assistant(tool_calls) ↔ tool
// response pairing, and never removes msgs[0] (system/history head).
func trimTurnMessages(msgs []ChatMessage, maxTotal int) []ChatMessage {
	for len(msgs) > maxTotal {
		cutStart, cutEnd := -1, -1
		for i := 1; i < len(msgs); i++ {
			if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
				cutStart = i
				cutEnd = i + 1
				ids := make(map[string]bool, len(msgs[i].ToolCalls))
				for _, tc := range msgs[i].ToolCalls {
					ids[tc.ID] = true
				}
				for j := i + 1; j < len(msgs); j++ {
					if msgs[j].Role == "tool" && ids[msgs[j].ToolCallID] && j+1 > cutEnd {
						cutEnd = j + 1
					}
				}
				break
			}
		}
		if cutStart < 0 {
			break // nothing safe to trim
		}
		msgs = append(msgs[:cutStart], msgs[cutEnd:]...)
	}
	return msgs
}
