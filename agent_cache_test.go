package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMem(t *testing.T, dir string, lastUpdated string, lessons string) {
	m := map[string]any{
		"identity":     "测试",
		"last_updated": lastUpdated,
		"lessons":      lessons,
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func hasKey(txt, key string) bool {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(txt), &m); err != nil {
		return false
	}
	_, ok := m[key]
	return ok
}

func TestMemTailDropsLastUpdated(t *testing.T) {
	dir := t.TempDir()
	writeMem(t, dir, "20260824", "教训A")
	txt := buildMemoryTailText(dir)
	if txt == "" {
		t.Fatal("记忆文本为空")
	}
	if hasKey(txt, "last_updated") {
		t.Fatalf("buildMemoryTailText 未剔除 last_updated: %s", txt)
	}
}

func TestFixedHeadCached(t *testing.T) {
	dir := t.TempDir()
	writeMem(t, dir, "20260824", "教训A")
	cfg := &Config{WorkDir: dir}
	a := &AgentRunner{cfg: cfg}
	h1 := a.buildFixedHead()
	if !a.headCached {
		t.Fatal("heading 未缓存")
	}
	writeMem(t, dir, "20260825", "教训B(新增)")
	h2 := a.buildFixedHead()
	if len(h1) != len(h2) {
		t.Fatalf("固定头长度变化: %d -> %d", len(h1), len(h2))
	}
	for i := range h1 {
		if h1[i].Content != h2[i].Content {
			t.Fatalf("固定头第%d条内容变化: 前缀断裂!\nold=%q\nnew=%q", i, h1[i].Content, h2[i].Content)
		}
	}
}

func TestSyncDynamicTailsAppendsOnly(t *testing.T) {
	dir := t.TempDir()
	writeMem(t, dir, "20260824", "教训A")
	cfg := &Config{WorkDir: dir}
	a := &AgentRunner{cfg: cfg}
	a.headCached = false
	a.buildFixedHead()
	a.lastMemText = a.initialMemText
	a.lastActiveTask = ""
	a.lastFoldedText = a.initialFoldedText
	a.history = append([]ChatMessage{}, a.buildFixedHead()...)
	before := len(a.history)
	beforeHead := make([]string, len(a.history))
	for i, m := range a.history {
		beforeHead[i] = m.Content
	}
	writeMem(t, dir, "20260825", "教训B(新增)")
	a.syncDynamicTails()
	if len(a.history) != before+1 {
		t.Fatalf("期望尾部追加1条, 实际 %d -> %d", before, len(a.history))
	}
	for i := range beforeHead {
		if a.history[i].Content != beforeHead[i] {
			t.Fatalf("第%d条头部被修改! 前缀可能断裂", i)
		}
	}
	last := a.history[len(a.history)-1]
	if last.Role != "user" || !containsT(last.Content, "记忆已更新") {
		t.Fatalf("尾部追加消息异常: %q", last.Content)
	}
}
func containsT(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─── 缓存修复回归测试 (20260826): trimTurnMessages 阈值与前缀保持 ───

func mkFixedHead() []ChatMessage {
	return []ChatMessage{
		{Role: "system", Content: "SYSTEM-HEAD"},
		{Role: "user", Content: "【记忆】"},
		{Role: "user", Content: "【折叠】"},
	}
}

func mkToolRounds(n int) []ChatMessage {
	msgs := make([]ChatMessage, 0, n*2)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs, ChatMessage{
			Role:      "assistant",
			ToolCalls: []ToolCall{{ID: id, Function: FunctionCall{Name: "forge"}}},
		})
		msgs = append(msgs, ChatMessage{Role: "tool", ToolCallID: id, Content: fmt.Sprintf("out_%d", i)})
	}
	return msgs
}

func TestTrimTurnMessages_BreaksHeadAtOldThreshold(t *testing.T) {
	msgs := append(mkFixedHead(), mkToolRounds(62)...)
	if len(msgs) != 127 {
		t.Fatalf("构造长度异常: %d", len(msgs))
	}
	headLen := 3
	beforeFirst := msgs[headLen].ToolCalls[0].ID
	out := trimTurnMessages(msgs, 60)
	if len(out) >= len(msgs) {
		t.Fatalf("旧阈值60未触发裁剪: len=%d", len(out))
	}
	afterFirst := out[headLen].ToolCalls[0].ID
	if afterFirst == beforeFirst {
		t.Fatalf("裁剪后固定头后第一条未变, 前缀未断裂?")
	}
	t.Logf("旧阈值60触发: 固定头后第一条 %s -> %s, 前缀断裂(后续全 miss)", beforeFirst, afterFirst)
}

func TestTrimTurnMessages_PreservesHeadAt300(t *testing.T) {
	msgs := append(mkFixedHead(), mkToolRounds(62)...)
	before := make([]string, len(msgs))
	for i, m := range msgs {
		if len(m.ToolCalls) > 0 {
			before[i] = m.ToolCalls[0].ID
		} else {
			before[i] = m.Content
		}
	}
	out := trimTurnMessages(msgs, 300)
	if len(out) != len(msgs) {
		t.Fatalf("阈值300不应触发裁剪: len=%d -> %d", len(msgs), len(out))
	}
	for i := range out {
		var cur string
		if len(out[i].ToolCalls) > 0 {
			cur = out[i].ToolCalls[0].ID
		} else {
			cur = out[i].Content
		}
		if cur != before[i] {
			t.Fatalf("第%d条前缀被改变: %q -> %q (前缀断裂)", i, before[i], cur)
		}
	}
	t.Logf("阈值300下 %d 条消息原样保留, 前缀完全稳定", len(out))
}

func TestPruneToolOutput_6000Threshold(t *testing.T) {
	long := strings.Repeat("X", 9000)
	got := pruneToolOutput(long, 6000, 3000, 1500)
	if len([]rune(got)) > 6200 {
		t.Fatalf("修剪后仍超阈值: %d rune", len([]rune(got)))
	}
	if !strings.HasPrefix(string([]rune(got)[:20]), "XXXXXXXXXXXXXXXX") {
		t.Fatalf("头部未保留")
	}
	if !strings.HasSuffix(string([]rune(got)[len([]rune(got))-20:]), "XXXXXXXXXXXXXXXXXX") {
		t.Fatalf("尾部未保留")
	}
	if !strings.Contains(got, "PRUNED") {
		t.Fatalf("缺少 PRUNED 标记")
	}
	t.Logf("pruneToolOutput 6000 截断生效: 9000 -> %d rune, 头尾保留", len([]rune(got)))
}
