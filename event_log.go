package main

// event_log.go — 行为事件日志 (借鉴 exo Event Log, 单机精简版)
//
// 设计: .forge\events.jsonl 追加式 JSON Lines, 永不覆盖。
//   - 8 种事件: turn_started / tool_called / tool_result / self_modified
//              / self_restart / memory_updated / error / guard_blocked
//   - 事件日志是"证据链"不是"记忆": 不进 memory.json, 不占 LLM 上下文。
//   - 自改/重启/回退都有据可查, 为将来进化路线打地基。
//
// 纪律: logEvent 永不 panic、永不阻塞主流程 (失败静默)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 事件类型常量
const (
	EvTurnStarted  = "turn_started"   // 新一轮任务开始
	EvToolCalled   = "tool_called"    // 工具调用(铸剑炉 gate)
	EvToolResult   = "tool_result"    // 工具返回
	EvSelfModified = "self_modified"  // 自改源码
	EvSelfRestart  = "self_restart"   // 自重启(含升级替换)
	EvMemoryUpdate = "memory_updated" // 记忆写入
	EvError        = "error"          // 任务/工具错误
	EvGuardBlocked = "guard_blocked"  // 输入护栏阻断
	EvApproved     = "approved"       // 三期 I2: 危险操作经主人批准执行
	EvGoalUpdate   = "goal_update"    // 三期 I5: 目标状态变更 (create/pause/resume/blocked/complete/clear)
)

// Event 一条事件记录
type Event struct {
	Ts      string      `json:"ts"`
	Type    string      `json:"type"`
	Detail  string      `json:"detail,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Session string      `json:"session,omitempty"` // 三期 I1: 所属会话; 空 = legacy 全局事件
}

var (
	evMu         sync.Mutex
	eventsPath   string
	eventSession string // 三期 I1: 当前事件会话 (空 = legacy)
)

// setEventSession 设置当前事件归属会话; 会话切换时必须调用, 否则事件串味。
func setEventSession(id string) {
	evMu.Lock()
	defer evMu.Unlock()
	eventSession = id
}

// initEventLog 初始化事件日志路径 (在 main 中调用一次)
func initEventLog(workDir string) {
	dir := filepath.Join(workDir, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	eventsPath = filepath.Join(dir, "events.jsonl")
}

// logEvent 追加一条事件。失败静默, 永不 panic。
func logEvent(typ, detail string, data interface{}) {
	if eventsPath == "" {
		return
	}
	ev := Event{
		Ts:      time.Now().Format(time.RFC3339),
		Type:    typ,
		Detail:  truncateCN(detail, 200),
		Data:    data,
		Session: eventSession,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	evMu.Lock()
	defer evMu.Unlock()
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// lastEvents 返回最后 n 条事件 (n<=0 返回全部)
func lastEvents(n int) []Event {
	if eventsPath == "" {
		return nil
	}
	evMu.Lock()
	defer evMu.Unlock()
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]Event, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(l), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}
