// ══════════════════════════════════════════════════════════════
// privacy.go — 对话原文落盘总开关 (隐私优先)。
//
// 三条落盘路径共用同一判据, 防止"改一处漏两处"的半修:
//   ① .forge/checkpoint.json  (agent_history.go  saveCheckpoint)
//   ② .forge/history          (main_interactive.go runInteractiveTurn)
//   ③ .forge/events.jsonl     (agent_stream_setup.go EvTurnStarted detail)
//
// 默认 false = 用户原文一律不落盘:
//   ① 每轮结束不自动写 checkpoint (会话恢复需手动)
//   ② 输入历史不追加 (上箭头历史不再累积)
//   ③ turn_started 事件 detail 脱敏为 <redacted> (事件链仍在, 原文不留)
//
// 设 FORGE_PERSIST_CONVO=1 可恢复旧行为 (会话恢复 / 历史 / 事件含原文)。
// 开关语义: 隐私是默认, 落盘是显式选择 —— 与"豁免必须显式且可度量"同构。
// ══════════════════════════════════════════════════════════════

package main

import "os"

// persistConversation 报告是否允许把对话原文落盘。
// 默认 false(隐私优先); FORGE_PERSIST_CONVO=1 才开启。
func persistConversation() bool {
	return os.Getenv("FORGE_PERSIST_CONVO") == "1"
}

// turnDetail 返回 EvTurnStarted 事件的 detail 字段。
// 不允许落盘时只留占位符, 不写用户输入原文 (事件本身仍记录, 供统计)。
func turnDetail(input string) string {
	if persistConversation() {
		return input
	}
	return "<redacted>"
}
