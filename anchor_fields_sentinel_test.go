package main

// anchor_fields_sentinel_test.go — 锚点字段清单防漂移哨兵 (20260913)
//
// 背景: anchorFields 白名单需人工维护, 与实际进固定头的字段集合脱节,
// 实测漏检 6 个锚点字段 (architecture/lessons/user_profile/swordless_roadmap/
// evolution_consensus/rescue), 且漏检字段连审计都不留痕 → 长期无人发现。
// 本哨兵把"清单必须覆盖真实字段"固化为死程序判定 (公理四/五)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 真实 memory.json 里的全部顶层字段必须被判为锚点改动 (动态字段除外)。
// 新增顶层字段时本测试自动覆盖 → 无需改测试即受保护 (fail-safe 语义验证)。
func TestAnchorSentinel_RealMemoryFieldsAllAnchored(t *testing.T) {
	wd, _ := os.Getwd()
	data, err := os.ReadFile(filepath.Join(wd, "memory.json"))
	if err != nil {
		t.Skip("无 memory.json (非主仓库环境), 跳过真实字段哨兵")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("memory.json 不可解析: %v", err)
	}
	if len(m) == 0 {
		t.Fatal("memory.json 顶层为空")
	}
	var missed []string
	anchored := 0
	for k := range m {
		if isDynamicMemoryField(k) {
			continue
		}
		anchored++
		if !isAnchorChange([]string{k}) {
			missed = append(missed, k)
		}
	}
	if len(missed) > 0 {
		t.Fatalf("锚点漏检 %d 个字段 (护栏不拦其改动, 且审计不留痕): %v", len(missed), missed)
	}
	if anchored == 0 {
		t.Fatal("真实记忆里零个锚点字段, 清单方向可能写反")
	}
	t.Logf("哨兵通过: %d 个锚点字段全部被护栏捕获, %d 个动态字段排除",
		anchored, len(m)-anchored)
}

// 纯动态字段组合 → 不算锚点改动 (否则片段写入会被误拦)。
func TestAnchorSentinel_DynamicOnlyNotAnchor(t *testing.T) {
	if isAnchorChange(append([]string{}, dynamicMemoryFields...)) {
		t.Fatalf("纯动态字段组合被判为锚点: %v", dynamicMemoryFields)
	}
}

// 空差异 / nil → 不算锚点。
func TestAnchorSentinel_EmptyNotAnchor(t *testing.T) {
	if isAnchorChange(nil) {
		t.Fatal("nil 字段列表被判为锚点")
	}
	if isAnchorChange([]string{}) {
		t.Fatal("空字段列表被判为锚点")
	}
}

// 未知新字段 (将来新增的顶层键) → 必须算锚点 (fail-safe, 原白名单实现在此 fail-open)。
func TestAnchorSentinel_UnknownFieldIsAnchor(t *testing.T) {
	if !isAnchorChange([]string{"brand_new_field_2099"}) {
		t.Fatal("未知新字段未被判为锚点 —— fail-safe 失效, 新增字段将绕过护栏")
	}
}

// 三处消费方共用同一份清单: 每个动态字段都必须被 buildMemoryTailText
// 与 stripDynamicMemory 同时剔除 (防清单再次分叉)。
func TestAnchorSentinel_AllConsumersDropAllDynamicFields(t *testing.T) {
	dir := t.TempDir()
	m := map[string]interface{}{"identity": "哨兵", "lessons": "教训"}
	for _, f := range dynamicMemoryFields {
		m[f] = map[string]interface{}{"probe": "哨兵探针值"}
	}
	raw, _ := json.Marshal(m)

	// ① buildMemoryTailText (走文件)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	tail := buildMemoryTailText(dir)
	if tail == "" {
		t.Fatal("buildMemoryTailText 返回空")
	}
	// ② stripDynamicMemory (走字节)
	stripped := string(stripDynamicMemory(raw))

	for _, f := range dynamicMemoryFields {
		if hasKey(tail, f) {
			t.Errorf("buildMemoryTailText 未剔除动态字段 %q", f)
		}
		if hasKey(stripped, f) {
			t.Errorf("stripDynamicMemory 未剔除动态字段 %q", f)
		}
	}
	// 锚点字段必须保留 (剔除过度 = 记忆丢失)
	for _, k := range []string{"identity", "lessons"} {
		if !hasKey(tail, k) {
			t.Errorf("buildMemoryTailText 误删锚点字段 %q", k)
		}
		if !hasKey(stripped, k) {
			t.Errorf("stripDynamicMemory 误删锚点字段 %q", k)
		}
	}
}

// 审计不再静默丢弃锚点改动: 改 lessons (原白名单漏检字段) 必须留痕。
func TestAnchorSentinel_AuditKeepsPreviouslyMissedField(t *testing.T) {
	wd := t.TempDir()
	base := map[string]interface{}{
		"identity": "x", "role": "y", "language": "zh", "working_dir": "wd",
		"gates": "python", "axioms": "a", "self_governance": map[string]interface{}{},
		"lessons": "旧教训",
	}
	d0, _ := json.Marshal(base)
	if err := SaveMemory(wd, d0); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	base["lessons"] = "新教训"
	d1, _ := json.Marshal(base)
	if err := SaveMemory(wd, d1); err == nil {
		t.Fatal("同日第 2 次锚点写入应被护栏拦截 (lessons 原属漏检字段)")
	}
	raw, err := os.ReadFile(anchorAuditPath(wd))
	if err != nil {
		t.Fatalf("审计文件缺失: %v", err)
	}
	if !containsT(string(raw), "lessons") {
		t.Fatalf("lessons 改动未入审计 (原实现静默丢弃): %s", string(raw))
	}
}
