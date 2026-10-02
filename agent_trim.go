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
	return estimateTokens(a.history[head:])
}

// maybeCompact 历史压缩:
// 在 trimHistory 硬裁剪之前调用 (每 turn 至多一次)。触发条件:
//
//	① 开关开启 (AGENT_COMPACT_ENABLED, 默认1)
//	② 对话部分 token 估算 ≥ AGENT_COMPACT_TOKEN_THRESHOLD (默认20000)
//	   注意口径是 contextBudgetUsed = 剔除固定头(system+记忆+折叠)之后的对话量,
//	   不是 estimateTokens(history) 全量 —— 见 contextBudgetUsed 注释 (2026-10-02 修正)
//	③ 距上次压缩 ≥ AGENT_COMPACT_MIN_TURNS 轮 (冷却防抖, 防前缀频繁变化)
//
// 动作: 取 system 之后的最旧段 (前1/3, 夹在4~12条), 调 LLM 摘要成 ≤400 字,
//
//	以 【会话摘要】 特殊消息放头部 (system 后), 丢弃被压缩段。
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
	if est < a.cfg.CompactTokenThreshold {
		return
	}
	// 最旧段: 固定头之后前 1/3, 夹在 4~12 条之间
	head := a.headLen // 跳过固定头 (system+记忆+折叠), v3.1 记忆不得被压缩掉
	if head < 1 {
		head = 1
	}
	rest := len(a.history) - head
	n := rest / 3
	if n < 4 {
		n = 4
	}
	if n > 12 {
		n = 12
	}
	if head+n >= len(a.history) {
		n = len(a.history) - head - 1
	}
	if n < 2 {
		return
	}
	// 配对安全选段 + system 前缀传给摘要器 (前缀缓存命中前置)
	start, end := completePairs(a.history, head, head+n)
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
