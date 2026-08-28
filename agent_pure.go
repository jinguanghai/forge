package main

import (
	"fmt"
	"strings"
	"unicode"
)

// abortUnknownTools 判定连续未知工具是否达到中止阈值。
// 返回 (是否中止, 中止消息)。副作用（append history/trim）由调用方执行。
func abortUnknownTools(unknownTools, maxFails int) (bool, string) {
	if unknownTools >= maxFails {
		return true, fmt.Sprintf("连续 %d 次调用未知工具（工具幻觉），已中止", unknownTools)
	}
	return false, ""
}

// parseErrMessage 构造参数解析失败回执消息。
func parseErrMessage(parseErr error) string {
	return fmt.Sprintf("参数解析失败: %v。请检查 JSON 格式。有效的参数: action(必需), code(必需), lang(可选), input(可选)。", parseErr)
}

// truncateDetail 截断错误详情到 maxLen（超长补省略号）。
func truncateDetail(detail string, maxLen int) string {
	if len(detail) > maxLen {
		return detail[:maxLen] + "..."
	}
	return detail
}

// consecutiveFailMessage 构造连续铸剑炉调用失败中止消息。
func consecutiveFailMessage(fails int) string {
	return fmt.Sprintf("连续 %d 次铸剑炉调用失败（非瞬时错误），已中止", fails)
}

// goalAnchor 构造带原始任务锚点的 tool 回执内容。
// 超长工具输出先进 pruneToolOutput 修剪,
// 防止大输出进入历史后每轮请求都背负重发 (缓存 miss 放大链)。
func goalAnchor(output, taskAnchor string, turn int) string {
	trimmed := pruneToolOutput(output, 6000, 3000, 1500)
	return trimmed + fmt.Sprintf("\n\n🎯 原始任务: %s\n📍 第%d轮", taskAnchor, turn+1)
}

// pruneToolOutput 工具结果修剪器 (借鉴 ToolResultPruner):
// 超阈值 (rune 计数) 保留 头head + 尾tail, 中间 ⏴ PRUNED ⏵ 标记截断。
// 确定性、无模型、replay-safe; 头尾之和覆盖全文时原样返回 (兜底)。
func pruneToolOutput(s string, threshold, head, tail int) string {
	runes := []rune(s)
	if len(runes) <= threshold || head+tail >= len(runes) {
		return s
	}
	var sb strings.Builder
	sb.WriteString(string(runes[:head]))
	sb.WriteString(fmt.Sprintf("\n\n⏴ PRUNED ⏵ (中间省略 %d rune)\n\n", len(runes)-head-tail))
	sb.WriteString(string(runes[len(runes)-tail:]))
	return sb.String()
}

// truncateAnchor 截断任务锚点到 150 rune（超长补省略号），
// 防止回执内容撑爆上下文窗口。150 rune 及以内原样返回。
func truncateAnchor(s string) string {
	runes := []rune(s)
	if len(runes) > 150 {
		return string(runes[:150]) + "..."
	}
	return s
}

// filterValidToolCalls 过滤不完整的 tool call 片段（ID 或函数名为空，
// 多为流式 delta 碎片），保持剩余调用的原始顺序。原地复用底层数组。
func filterValidToolCalls(calls []ToolCall) []ToolCall {
	valid := calls[:0]
	for _, tc := range calls {
		if tc.ID != "" && tc.Function.Name != "" {
			valid = append(valid, tc)
		}
	}
	return valid
}

// effectiveMaxFails 归一化最大失败次数: <=0 时用默认值 5（与原逻辑一致）。
func effectiveMaxFails(maxFails int) int {
	if maxFails <= 0 {
		return 5
	}
	return maxFails
}

// effectiveMaxStrikes 归一化循环拦截次数上限: <=0 时用默认值 4。
// 注意: 这是"无进展拦截"上限, 不是回合上限——真正推进的任务可无限跑。
func effectiveMaxStrikes(maxStrikes int) int {
	if maxStrikes <= 0 {
		return 4
	}
	return maxStrikes
}

// shouldAbortOnFails 判定连续失败是否达到中止阈值（consecutive >= max）。
func shouldAbortOnFails(consecutive, max int) bool {
	return consecutive >= max
}

// normalizeCode 去除代码的注释与全部空白（含行内空白），用于"语义等价"的循环检测。
// 语言无关启发式: 剥离 // 与 # 到行尾的注释, 并删除所有空白字符。
// 字符串字面量内的 # / // / 空格可能被误删——对循环检测而言只会造成不同代码的
// 碰撞(误拦, 可恢复: 拦截只是提示换策略), 不会放过"改注释/空格/换行"的循环变体
// (这正是旧实现被绕过的洞)。
func normalizeCode(code string) string {
	var sb strings.Builder
	for _, line := range strings.Split(code, "\n") {
		t := line
		if idx := strings.Index(t, "//"); idx >= 0 {
			t = t[:idx]
		}
		if idx := strings.Index(t, "#"); idx >= 0 {
			t = t[:idx]
		}
		t = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, t)
		if t == "" {
			continue
		}
		sb.WriteString(t)
	}
	return sb.String()
}

// repeatReminder 构造重复调用检测的逐级提醒消息。
// 三级梯度: 温和(首次超限) → 详细(次数+代码摘要) → 强提醒(禁止继续相同代码)。
// 摘要基于 normalizeCode 后的"语义等价"串, 截 80 rune——揭示这些代码归一化后
// 其实是同一个, 堵死"改注释/改空白就绕过"的幻觉路径。
// 分界语义: maxRepeatedCalls=3 时 count=4 起才拦截; 拦截1~2次(count 4~5)温和,
// 3~5次(count 6~8)详细, 6次以上(count>8)强提醒。
func repeatReminder(count int, code string) string {
	summary := normalizeCode(code)
	runes := []rune(summary)
	if len(runes) > 80 {
		summary = string(runes[:80]) + "..."
	}
	switch {
	case count <= 5:
		return fmt.Sprintf("重复调用检测: 相同代码已执行 %d 次。请先分析上次执行结果, 确认无进展后再决定下一步——不要直接重试相同代码。", count)
	case count <= 8:
		return fmt.Sprintf("⚠️ 重复调用检测(第%d次): 相同代码已连续执行 %d 次且无进展。\n相同代码(语义等价摘要): %s\n请立即换一种完全不同的实现方案; 若暂无替代思路, 先向用户报告当前卡点与已尝试的方案。", count, count, summary)
	default:
		return fmt.Sprintf("🚫 重复调用检测(第%d次): 相同代码已执行 %d 次, 任务持续无进展。禁止继续执行相同/相似代码。立即换完全不同的方案; 无法推进则向用户报告卡点与已取得的全部结果。\n相同代码(语义等价摘要): %s", count, count, summary)
	}
}

// stuckInterventionMsg 循环拦截干预指令: 不中止任务, 而是要求模型立即
// 换策略继续推进, 或确认无法完成时总结收尾。连续多次拦截仍无进展才会自动收尾。
func stuckInterventionMsg(strikes int) string {
	return fmt.Sprintf("⚠️ 循环拦截(第%d次): 你已连续多轮执行相同/相似代码且输出无变化, 任务没有推进。立即停止当前思路——"+
		"1) 换一种完全不同的实现/算法/工具组合继续推进任务; "+
		"2) 卡住的地方绕开它, 用替代方案完成其余部分; "+
		"3) 若确认任务确实无法完成, 直接输出最终总结(做了什么/卡在哪/已取得的全部结果), 禁止再重复执行相同代码。",
		strikes)
}

// stuckExitMessage 自动收尾总结: 连续多次拦截仍无进展时, 带已取得的进展
// 结束本轮任务(不是失败中止, 是带产出的收尾)。
func stuckExitMessage(strikes int, progress string) string {
	if len(progress) > 600 {
		progress = progress[:600] + "..."
	}
	return fmt.Sprintf("⚠️ 自动收尾: 连续 %d 次循环拦截后任务仍无进展, 已停止执行以避免无限循环。\n"+
		"任务未完整完成。以下为已取得的进展(最后一次执行结果):\n%s\n\n"+
		"请基于以上结果继续推进, 或改用全新方案重新开始。", strikes, progress)
}

// classifyTransient reports whether a forge Build failure is environmental
// (timeout, missing tool) rather than a genuine code bug. Non-transient
// failures count toward consecutive-fail aborts; transient ones are not
// penalized because they are often environmental.
// 注意: 非零退出(exit status)不算瞬态——那是程序真实运行失败, 必须计入失败,
// 否则模型反复执行同样失败的代码也不会触发任何中止(旧漏洞)。
func classifyTransient(result *ForgeGateResult) bool {
	if result == nil {
		return false
	}
	stage := result.Stage
	errLower := strings.ToLower(result.Error)
	// compile/check stage failures are real code bugs in the LLM output → count fully.
	// execute stage timeouts or missing tools are environmental → no penalty.
	if stage == "execute" && (strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "not found") ||
		strings.Contains(errLower, "找不到")) {
		return true
	}
	if stage == "compile" && strings.Contains(errLower, "not found") {
		return true
	}
	return false
}

// stripCrossTurnReasoning 跨 user turn 清理 history 中 assistant 消息的
// reasoning_content (deepseek-harness §1.3 规则3): 服务端跨轮不要求回传
// reasoning; 保留会膨胀前缀并破坏字节级缓存匹配 (reasoning 内容非确定性 →
// 每次采样不同 → 前缀断裂 → 缓存全 miss)。仅清理无 tool_calls 的 assistant
// 消息: 工具循环内 (含 assistant(tool_calls)+reasoning) 不受影响, 仍按
// §1.3 规则2 回传。返回新切片, 不修改入参。
func stripCrossTurnReasoning(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role == "assistant" && len(out[i].ToolCalls) == 0 && out[i].ReasoningContent != "" {
			out[i].ReasoningContent = ""
		}
	}
	return out
}

// completePairs 扩展 [start,end) 区间保证 assistant(tool_calls)↔tool 响应配对不被切断
// 摘要请求消息必须配对完整, 否则 API 400。
// 返回调整后的 start,end; start 不低于 1 (保护 history[0]=system)。
func completePairs(msgs []ChatMessage, start, end int) (int, int) {
	n := len(msgs)
	if start < 1 {
		start = 1
	}
	if end > n {
		end = n
	}
	if start >= end {
		return start, end
	}
	// ① start 处是 tool 消息 (其 assistant 配对在 start 前) → 前移纳入
	for start > 1 && msgs[start].Role == "tool" {
		start--
	}
	// ② 段内 assistant 带 tool_calls → 向后收齐所有 tool 响应
	for i := start; i < end; i++ {
		if msgs[i].Role != "assistant" || len(msgs[i].ToolCalls) == 0 {
			continue
		}
		ids := make(map[string]bool, len(msgs[i].ToolCalls))
		for _, tc := range msgs[i].ToolCalls {
			ids[tc.ID] = true
		}
		j := i + 1
		for j < n && len(ids) > 0 {
			if msgs[j].Role == "tool" && ids[msgs[j].ToolCallID] {
				delete(ids, msgs[j].ToolCallID)
			}
			j++
			if j > end {
				end = j
			}
		}
	}
	// ③ end 处是 tool 消息 (防御: 收进段内)
	for end < n && msgs[end].Role == "tool" {
		end++
	}
	return start, end
}
