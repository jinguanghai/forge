package main

// cov_gaps3_more_test.go — 覆盖率缺口补测 第三批 (跨平台, 20261001)
//
// 覆盖 DSH 动态尾部同步 syncDynamicTails 的四条分支:
// 记忆稳定段变化 / 待办变化 / 折叠索引变化 / cfg 缺失短路。
// 全部落在 t.TempDir 的 memory.json, 不触碰真实记忆文件。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMemFile(t *testing.T, wd string, mem map[string]interface{}) {
	t.Helper()
	b, err := json.Marshal(mem)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wd, "memory.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCovGap3_SyncDynamicTails_Branches(t *testing.T) {
	agent, cfg, _ := newHandleCmdAgent(t)
	wd := t.TempDir()
	cfg.WorkDir = wd // agent.cfg 与 cfg 同一指针
	agent.history = nil
	agent.lastMemText, agent.lastActiveTask, agent.lastFoldedText = "", "", ""

	items := func(name, status, summary string) []FoldedItem {
		return []FoldedItem{{Name: name, Status: status, Summary: summary}}
	}
	base := func(identity, task string, its []FoldedItem) map[string]interface{} {
		m := map[string]interface{}{
			"identity":    identity,
			"active_task": task,
		}
		if its != nil {
			m["folded_memory"] = map[string]interface{}{"items": its}
		}
		return m
	}

	// ① 首轮同步: 只建立基线, 绝不 append (否则每会话首轮都白烧 token)
	writeMemFile(t, wd, base("甲", "任务A", nil))
	agent.syncDynamicTails()
	if len(agent.history) != 0 {
		t.Fatalf("首次同步不得追加消息, 实际 %d 条", len(agent.history))
	}
	if agent.lastMemText == "" || agent.lastActiveTask != "任务A" {
		t.Fatalf("基线未建立: mem=%q task=%q", agent.lastMemText, agent.lastActiveTask)
	}

	// ② 稳定段变化 → 【记忆已更新】(只带差异字段)
	writeMemFile(t, wd, base("乙", "任务A", nil))
	agent.syncDynamicTails()
	if len(agent.history) != 1 {
		t.Fatalf("稳定段变化应追加 1 条, 实际 %d 条", len(agent.history))
	}
	got := agent.history[0].Content
	if !strings.Contains(got, "【记忆已更新】") {
		t.Fatalf("应带更新标记: %q", got)
	}
	if !strings.Contains(got, "乙") {
		t.Fatalf("差异应包含新值: %q", got)
	}
	if strings.Contains(got, "active_task") || strings.Contains(got, "folded_memory") {
		t.Fatalf("动态字段不得混入稳定段 diff: %q", got)
	}

	// ③ 待办变化 → 【待办更新】
	writeMemFile(t, wd, base("乙", "任务B", nil))
	agent.syncDynamicTails()
	if len(agent.history) != 2 || !strings.Contains(agent.history[1].Content, "【待办更新】") {
		t.Fatalf("待办变化应追加待办消息, 实际 %q", agent.history[len(agent.history)-1].Content)
	}

	// ④ 折叠索引: 首次出现只设基线, 再变化才追加
	writeMemFile(t, wd, base("乙", "任务B", items("旧档", "done", "摘要")))
	agent.syncDynamicTails()
	if len(agent.history) != 2 {
		t.Fatalf("折叠索引首次出现不得追加 (无基线可比), 实际 %d 条", len(agent.history))
	}
	writeMemFile(t, wd, base("乙", "任务B", items("新档", "doing", "新摘要")))
	agent.syncDynamicTails()
	if len(agent.history) != 3 || !strings.Contains(agent.history[2].Content, "【档案索引已更新】") {
		t.Fatalf("折叠索引变化应追加, 实际 %q", agent.history[len(agent.history)-1].Content)
	}

	// ⑤ 无变化 → 不追加 (缓存前缀稳定性的前提)
	before := len(agent.history)
	agent.syncDynamicTails()
	if len(agent.history) != before {
		t.Fatalf("无变化不得追加, 实际 %d -> %d", before, len(agent.history))
	}

	// ⑥ cfg 缺失短路: 不得 panic
	(&AgentRunner{}).syncDynamicTails()

	// ⑦ memory.json 缺失/损坏: 一律静默, 不得 panic 也不得追加
	empty := t.TempDir()
	cfg.WorkDir = empty
	agent.lastMemText, agent.lastActiveTask, agent.lastFoldedText = "", "", ""
	n := len(agent.history)
	agent.syncDynamicTails()
	if len(agent.history) != n {
		t.Fatalf("记忆缺失时不得追加, 实际 %d -> %d", n, len(agent.history))
	}
	if err := os.WriteFile(filepath.Join(empty, "memory.json"), []byte("{坏 json"), 0644); err != nil {
		t.Fatal(err)
	}
	agent.syncDynamicTails()
}
