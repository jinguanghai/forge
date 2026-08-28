package main

// goal_test.go — 三期 DMAIC I5 目标状态机 + 会话统计测试

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGoalLifecycle(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	// 创建
	g, err := createGoal(wd, "完成三期 DMAIC")
	if err != nil {
		t.Fatalf("createGoal: %v", err)
	}
	if g.Status != GoalActive {
		t.Fatalf("初始状态应为 active, got %s", g.Status)
	}
	if _, err := os.Stat(filepath.Join(wd, ".forge", "goal.json")); err != nil {
		t.Fatalf("goal.json 未落盘: %v", err)
	}

	// 重复创建应报错 (已有未完成目标)
	if _, err := createGoal(wd, "第二个"); err == nil {
		t.Fatal("已有未完成目标时应拒绝创建")
	}

	// pause → resume
	if _, err := setGoalStatus(wd, GoalPaused, ""); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := setGoalStatus(wd, GoalPaused, ""); err == nil {
		t.Fatal("paused → paused 应非法")
	}
	if _, err := setGoalStatus(wd, GoalActive, ""); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// blocked 需要原因
	if _, err := setGoalStatus(wd, GoalBlocked, ""); err == nil {
		t.Fatal("blocked 无原因应报错")
	}
	g2, err := setGoalStatus(wd, GoalBlocked, "外部依赖未就绪")
	if err != nil {
		t.Fatalf("blocked: %v", err)
	}
	if g2.BlockedReason != "外部依赖未就绪" {
		t.Fatalf("blocked reason: %q", g2.BlockedReason)
	}

	// complete
	if _, err := setGoalStatus(wd, GoalCompleted, ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// completed 后可再创建
	if _, err := createGoal(wd, "新目标"); err != nil {
		t.Fatalf("completed 后创建应成功: %v", err)
	}
}

func TestGoalSessionIsolation(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	// 会话 A 建目标
	setCurrentSession("sA")
	if _, err := createGoal(wd, "A 的目标"); err != nil {
		t.Fatal(err)
	}
	// 会话 B 无目标
	setCurrentSession("sB")
	g, err := loadGoal(wd)
	if err != nil || g != nil {
		t.Fatalf("会话 B 不应看到 A 的目标: g=%+v err=%v", g, err)
	}
	// 回 A 仍在
	setCurrentSession("sA")
	g2, err := loadGoal(wd)
	if err != nil || g2 == nil || g2.Title != "A 的目标" {
		t.Fatalf("会话 A 目标丢失: %+v err=%v", g2, err)
	}
}

func TestCollectSessionStats(t *testing.T) {
	resetSessionState()
	defer resetSessionState()
	wd := t.TempDir()

	// 构造事件日志: 会话 sA 2 成功 1 失败; legacy 1 条
	dir := filepath.Join(wd, ".forge")
	os.MkdirAll(dir, 0755)
	os.MkdirAll(sessionDir(wd, "sA"), 0755)
	writeEv := func(path, sess, lang string, ok bool) {
		ev := Event{Ts: "2026-08-17T00:00:00Z", Type: EvToolResult, Detail: lang, Session: sess,
			Data: map[string]interface{}{"ok": ok}}
		b, _ := json.Marshal(ev)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal(err)
		}
		f.Write(append(b, '\n'))
		f.Close()
	}
	sAPath := filepath.Join(sessionDir(wd, "sA"), "events.jsonl")
	writeEv(sAPath, "sA", "math", true)
	writeEv(sAPath, "sA", "math", true)
	writeEv(sAPath, "sA", "logic", false)
	writeEv(filepath.Join(dir, "events.jsonl"), "", "python", true)

	// 会话 A: 只统计 sA 的 3 条
	setCurrentSession("sA")
	ss := collectSessionStats(wd)
	if ss.Total != 3 || ss.OK != 2 || ss.Fail != 1 {
		t.Fatalf("sA 统计: total=%d ok=%d fail=%d, want 3/2/1", ss.Total, ss.OK, ss.Fail)
	}
	if len(ss.Gates) != 2 {
		t.Fatalf("sA 应统计 2 个 gate, got %d", len(ss.Gates))
	}
	// math 在前 (调用数降序)
	if ss.Gates[0].Lang != "math" || ss.Gates[0].Calls != 2 {
		t.Fatalf("math 应排首位: %+v", ss.Gates[0])
	}

	// legacy: 只统计旧路径的 legacy 事件 (sA 的事件归属 sA 会话)
	setCurrentSession("")
	ss2 := collectSessionStats(wd)
	if ss2.Total != 1 || ss2.Gates[0].Lang != "python" {
		t.Fatalf("legacy 应只统计旧路径 1 条 python, got total=%d %+v", ss2.Total, ss2.Gates)
	}
}
