package main

import "fmt"

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

// repeatCallMessage 构造重复调用检测回执消息。
func repeatCallMessage(callHash string, count int) string {
	return fmt.Sprintf("重复调用检测: 相同的代码已执行 %d 次。请改变策略。", count)
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
func goalAnchor(output, taskAnchor string, turn int) string {
	return output + fmt.Sprintf("\n\n🎯 原始任务: %s\n📍 第%d轮", taskAnchor, turn+1)
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

// shouldAbortOnFails 判定连续失败是否达到中止阈值（consecutive >= max）。
func shouldAbortOnFails(consecutive, max int) bool {
	return consecutive >= max
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
