package main

// memory_lessons_test.go — lessons 降重改造判据 (20261004)
//
// 背景: lessons 曾是单个 str 字段 (10352 字符 ≈ 占锚点注入 33%), 全量进 system 固定头;
// str 不可切分 → BM25 无从召回 → 只增不减。
// 改造: ①lessons 归动态字段, 由 RecallMemory 独立配额召回 (lessonsRecallTopK);
//      ②常驻硬约束迁至 lessons_core (锚点字段, 留在固定头)。
//
// 本哨兵钉住两条退化路径 (任一失守则改造回到原状):
//   ① lessons 不再是动态字段 → 重新全量进固定头;
//   ② lessons_core 被误归动态字段 → 常驻硬约束掉出固定头。

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ① lessons 必须是动态字段 (不进固定头) —— 否则降重失效。
func TestLessonsIsDynamicField(t *testing.T) {
	if !isDynamicMemoryField("lessons") {
		t.Fatal("lessons 不在 dynamicMemoryFields → 重新全量进固定头, 降重失效")
	}
	if isAnchorChange([]string{"lessons"}) {
		t.Fatal("lessons 被判为锚点改动 (应为动态字段)")
	}
	dir := t.TempDir()
	writeMemFile(t, dir, map[string]interface{}{"identity": "哨兵", "lessons": "一条教训"})
	if tail := buildMemoryTailText(dir); hasKey(tail, "lessons") {
		t.Fatalf("buildMemoryTailText 仍注入 lessons: %s", tail)
	}
}

// ② lessons_core 是锚点字段且真实进固定头 (常驻硬约束不能悄悄掉出)。
func TestLessonsCoreStaysInFixedHead(t *testing.T) {
	if isDynamicMemoryField("lessons_core") {
		t.Fatal("lessons_core 被误归动态字段 → 常驻硬约束掉出固定头")
	}
	if !isAnchorChange([]string{"lessons_core"}) {
		t.Fatal("lessons_core 未被判为锚点改动 → 改动不受配额护栏保护")
	}
	dir := t.TempDir()
	writeMemFile(t, dir, map[string]interface{}{
		"identity": "哨兵", "lessons_core": "常驻硬约束", "lessons": []interface{}{},
	})
	if tail := buildMemoryTailText(dir); !hasKey(tail, "lessons_core") {
		t.Fatalf("buildMemoryTailText 未保留 lessons_core: %s", tail)
	}
}

// ③ 旧格式兼容: lessons 仍是整段字符串时按行切分, 迁移前也能召回。
func TestRecallMemoryLessonsLegacyStr(t *testing.T) {
	dir := t.TempDir()
	writeMemFile(t, dir, map[string]interface{}{
		"lessons": "第一条 前缀缓存 教训。\n第二条 无关内容。",
	})
	block, n := RecallMemory(dir, "前缀缓存", 3)
	if n == 0 || !strings.Contains(block, "前缀缓存") {
		t.Fatalf("旧 str 格式 lessons 未被召回: n=%d\n%s", n, block)
	}
}

// ④ 真实数据形态: lessons 必须是结构化数组且每项带 ts 时间锚点
// (str 格式无法携带 ts → 条目只能靠正文日期, 无日期则 fail-closed 降权)。
func TestLessonsRealMemoryShape(t *testing.T) {
	data, err := os.ReadFile("memory.json")
	if err != nil {
		t.Skip("无 memory.json (非主仓库环境)")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("memory.json 不可解析: %v", err)
	}
	raw, ok := m["lessons"]
	if !ok {
		t.Skip("memory.json 无 lessons 段")
	}
	var arr []map[string]interface{}
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("lessons 不是结构化数组 (str 格式未迁移 → 无法携带 ts): %v", err)
	}
	if len(arr) == 0 {
		t.Fatal("lessons 数组为空 (教训丢失)")
	}
	for i, it := range arr {
		if _, ok := it["ts"]; !ok {
			t.Errorf("lessons[%d] 缺 ts 时间锚点: %v", i, it["title"])
		}
		if s, _ := it["content"].(string); s == "" {
			t.Errorf("lessons[%d] content 为空", i)
		}
	}
	if _, ok := m["lessons_core"]; !ok {
		t.Error("memory.json 缺 lessons_core (常驻硬约束段)")
	}
}
