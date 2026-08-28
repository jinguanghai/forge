package main

// goal.go — 持久化目标状态机 (借鉴 DSH goal 服务单机精简版)
//
// 状态机: active → paused → active / blocked → completed
//   create   : 创建目标 → active
//   pause    : active → paused
//   resume   : paused → active
//   blocked  : active/paused → blocked (带原因)
//   complete : active/paused/blocked → completed
//
// 持久化: .forge/sessions/<id>/goal.json (当前会话隔离);
// legacy 模式(无当前会话) → .forge/goal.json。
// 原子写 (tmp+rename), 重启后可查 —— 与 DSH goal 跨会话持久化对齐。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Goal 一个持久化目标
type Goal struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"` // active | paused | completed | blocked
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	BlockedReason string `json:"blocked_reason,omitempty"`
}

// goalStatus 合法状态
const (
	GoalActive    = "active"
	GoalPaused    = "paused"
	GoalCompleted = "completed"
	GoalBlocked   = "blocked"
)

// goalPath 当前目标的持久化路径 (按当前会话隔离)
func goalPath(workDir string) string {
	if id := currentSession(); id != "" {
		return filepath.Join(sessionDir(workDir, id), "goal.json")
	}
	return filepath.Join(workDir, ".forge", "goal.json")
}

// loadGoal 读取当前目标; 不存在返回 (nil, nil)。
func loadGoal(workDir string) (*Goal, error) {
	if workDir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(goalPath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var g Goal
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("goal.json 损坏: %v", err)
	}
	return &g, nil
}

// saveGoal 原子写目标 (tmp+rename)
func saveGoal(workDir string, g *Goal) error {
	path := goalPath(workDir)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// createGoal 创建目标 (若已有未完成目标则报错, 防误覆盖)
func createGoal(workDir, title string) (*Goal, error) {
	title = stripRecallPrefix(title)
	runes := []rune(title)
	if len(runes) > 80 {
		title = string(runes[:80]) + "…"
	}
	existing, err := loadGoal(workDir)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.Status != GoalCompleted {
		return nil, fmt.Errorf("已有未完成目标: %s (先 /goal complete 或 /goal clear)", existing.Title)
	}
	now := time.Now().Format(time.RFC3339)
	g := &Goal{
		ID:        "g" + time.Now().Format("20060102_150405"),
		Title:     title,
		Status:    GoalActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := saveGoal(workDir, g); err != nil {
		return nil, err
	}
	logEvent(EvGoalUpdate, "create", map[string]string{"status": g.Status, "title": g.Title})
	return g, nil
}

// setGoalStatus 状态迁移; 非法迁移返回错误。blocked 需 reason。
func setGoalStatus(workDir, status, reason string) (*Goal, error) {
	g, err := loadGoal(workDir)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("当前无目标 (先 /goal <标题> 创建)")
	}
	switch status {
	case GoalPaused:
		if g.Status != GoalActive {
			return nil, fmt.Errorf("非法迁移: %s → paused", g.Status)
		}
	case GoalActive: // resume
		if g.Status != GoalPaused {
			return nil, fmt.Errorf("非法迁移: %s → active (仅 paused 可 resume)", g.Status)
		}
	case GoalCompleted:
		if g.Status == GoalCompleted {
			return nil, fmt.Errorf("目标已是 completed")
		}
	case GoalBlocked:
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return nil, fmt.Errorf("blocked 需要原因: /goal blocked <原因>")
		}
		if len([]rune(reason)) > 200 {
			reason = string([]rune(reason)[:200]) + "…"
		}
		g.BlockedReason = reason
	default:
		return nil, fmt.Errorf("未知状态: %s", status)
	}
	g.Status = status
	g.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := saveGoal(workDir, g); err != nil {
		return nil, err
	}
	logEvent(EvGoalUpdate, status, map[string]string{"title": g.Title, "reason": g.BlockedReason})
	return g, nil
}

// clearGoal 清除目标 (completed 或强制)
func clearGoal(workDir string) error {
	path := goalPath(workDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	logEvent(EvGoalUpdate, "clear", nil)
	return nil
}

// goalStatusLabel 状态的中文标签 (终端展示)
func goalStatusLabel(status string) string {
	switch status {
	case GoalActive:
		return "🟢 进行中 (active)"
	case GoalPaused:
		return "⏸ 已暂停 (paused)"
	case GoalCompleted:
		return "✅ 已完成 (completed)"
	case GoalBlocked:
		return "⛔ 已阻塞 (blocked)"
	}
	return status
}
