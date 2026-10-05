package main

// memory_write_sentinel_test.go — 写入路径哨兵正反用例
//
// 覆盖: ① 无主文件不误报 ② 无留痕基线只提示不报警 ③ 基线建立后通过
// ④ 基线幂等 ⑤ 旁路写入被抓 ⑥ SaveMemory 留痕含写入内容 sha
// ⑦ HealMemory 恢复不误报 ⑧ 手工例外可见 ⑨ Python 侧 src 值域与 Go 同源
// ⑩ cmd_patch 的写入必须记 save (P4, 20261005)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestMemoryWriteSentinel_NoMainFile(t *testing.T) {
	dir := t.TempDir()
	ok, msg := memoryWriteSentinel(dir)
	if !ok {
		t.Fatalf("无主文件不应报警: %s", msg)
	}
}

func TestMemoryWriteSentinel_NoBaselineIsNotAlarm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{"a":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	ok, msg := memoryWriteSentinel(dir)
	if !ok {
		t.Fatalf("无留痕基线时应返回不判定, 实际报警: %s", msg)
	}
	if !strings.Contains(msg, "尚无留痕基线") {
		t.Fatalf("应提示尚无留痕基线: %s", msg)
	}
}

func TestMemoryWriteSentinel_BaselineThenOK(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{"a":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	ensureMemoryWriteBaseline(dir)
	ok, msg := memoryWriteSentinel(dir)
	if !ok {
		t.Fatalf("基线建立后应通过: %s", msg)
	}
	if !strings.Contains(msg, "有 SaveMemory 留痕") {
		t.Fatalf("应报告有留痕: %s", msg)
	}
}

func TestEnsureMemoryWriteBaseline_Idempotent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{"a":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	ensureMemoryWriteBaseline(dir)
	ensureMemoryWriteBaseline(dir)
	_, n, err := loadMemoryWriteSHAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("基线应幂等(仅 1 条), 实际 %d 条", n)
	}
}

func TestMemoryWriteSentinel_DetectsBypass(t *testing.T) {
	t.Setenv("FORGE_ANCHOR_GUARD", "0")
	dir := t.TempDir()
	v1 := []byte(`{"key_findings":[],"last_updated":"v1"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	if ok, msg := memoryWriteSentinel(dir); !ok {
		t.Fatalf("SaveMemory 后应通过: %s", msg)
	}
	bypass := []byte(`{"key_findings":[],"last_updated":"bypass"}`)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), bypass, 0644); err != nil {
		t.Fatal(err)
	}
	ok, msg := memoryWriteSentinel(dir)
	if ok {
		t.Fatalf("旁路写入应被抓到, 实际通过: %s", msg)
	}
	if !strings.Contains(msg, "无写入留痕") {
		t.Fatalf("报警文案不符: %s", msg)
	}
}

// TestMemoryWriteSentinel_ManualExemptIsVisible 手工例外路径不得静默通过。
//
// 20261003: 留痕条目新增 src 字段区分写入路径。批准的例外 (manual-exempt) 仍有留痕,
// 故不判旁路 —— 但必须显式可见并单列计数, 否则「例外」与「正常」混为一谈。
func TestMemoryWriteSentinel_ManualExemptIsVisible(t *testing.T) {
	dir := t.TempDir()
	v1 := []byte(`{"key_findings":[],"last_updated":"manual"}`)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), v1, 0644); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(MemoryWriteEntry{
		Time: "2026-10-03T00:00:00+08:00", SHA: sysHashPrefix(v1), Size: len(v1),
		Src: memoryWriteSrcManual, Reason: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, memoryWritesFileName), append(line, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	ok, msg := memoryWriteSentinel(dir)
	if !ok {
		t.Fatalf("有留痕的例外路径不应判旁路: %s", msg)
	}
	if !strings.Contains(msg, "手工例外") {
		t.Fatalf("手工例外未被显式标注 (会与正常路径混淆): %s", msg)
	}
	if got := countMemoryWriteSrc(dir, memoryWriteSrcManual); got != 1 {
		t.Fatalf("手工例外计数 = %d; 期望 1", got)
	}
	// 反例: 正常路径 (src=save) 不得被标成手工例外
	d2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(d2, "memory.json"), v1, 0644); err != nil {
		t.Fatal(err)
	}
	line2, _ := json.Marshal(MemoryWriteEntry{
		Time: "2026-10-03T00:00:00+08:00", SHA: sysHashPrefix(v1), Size: len(v1),
		Src: memoryWriteSrcSave, Reason: "fixture",
	})
	if err := os.WriteFile(filepath.Join(d2, memoryWritesFileName), append(line2, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if _, msg2 := memoryWriteSentinel(d2); strings.Contains(msg2, "手工例外路径") {
		t.Fatalf("正常路径被误标为手工例外: %s", msg2)
	}
}

func TestSaveMemory_RecordsWriteSHA(t *testing.T) {
	t.Setenv("FORGE_ANCHOR_GUARD", "0")
	dir := t.TempDir()
	v1 := []byte(`{"key_findings":[],"last_updated":"v1"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	set, n, err := loadMemoryWriteSHAs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应留痕 1 条, 实际 %d", n)
	}
	if !set[sysHashPrefix(v1)] {
		t.Fatalf("留痕应含写入内容 sha: %s", sysHashPrefix(v1))
	}
}

func TestMemoryWriteSentinel_HealMemoryNoFalsePositive(t *testing.T) {
	t.Setenv("FORGE_ANCHOR_GUARD", "0")
	dir := t.TempDir()
	v1 := []byte(`{"key_findings":[],"last_updated":"v1"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{broken`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := HealMemory(dir); err != nil {
		t.Fatal(err)
	}
	ok, msg := memoryWriteSentinel(dir)
	if !ok {
		t.Fatalf("自愈恢复为曾留痕版本, 不应误报: %s", msg)
	}
}

// TestPythonWriteSrcValuesMatchGo: 留痕条目 src 的取值必须落在 Go 侧认识的值域内。
//
// 背景 (20261005 P4): cmd_patch 曾把合规写入标成 manual-exempt —— 与"手工绕过
// SaveMemory"在标记上无法区分, 哨兵据此常驻黄标、manual_exempt 计数虚增。
// 根因是【合规路径自己打例外标记】(与 20261003 修掉的 guard_off 硬编码同型)。
// 判据: Python 侧 SRC_* 常量值 ⊆ Go 侧值域, 且 SRC_SAVE 必须存在 ——
// 否则合规路径只能退回去打例外标记。
func TestPythonWriteSrcValuesMatchGo(t *testing.T) {
	raw, err := os.ReadFile(pyMemoryWritePath)
	if err != nil {
		t.Skipf("出口工具不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	re := regexp.MustCompile(`(?m)^SRC_\w+\s*=\s*"([^"]+)"`)
	got := re.FindAllStringSubmatch(string(raw), -1)
	if len(got) == 0 {
		t.Fatalf("%s 未找到任何 SRC_* 常量 (判据锚点丢失)", pyMemoryWritePath)
	}
	allowed := map[string]bool{
		memoryWriteSrcSave:   true,
		memoryWriteSrcBase:   true,
		memoryWriteSrcManual: true,
		"exempt":             true, // 审计条目 src: 事后认领 (Go 侧无对应常量, Python 独有)
	}
	var vals []string
	seenSave := false
	for _, m := range got {
		v := m[1]
		vals = append(vals, v)
		if !allowed[v] {
			t.Errorf("%s 的 src 取值 %q 不在 Go 侧值域内 —— 消费者按未知标记处理 = 静默失真",
				pyMemoryWritePath, v)
		}
		if v == memoryWriteSrcSave {
			seenSave = true
		}
	}
	if !seenSave {
		t.Fatalf("%s 未定义 SRC_SAVE=%q —— 合规路径只能打例外标记 (P4 缺陷原形); 现有: %v",
			pyMemoryWritePath, memoryWriteSrcSave, vals)
	}
}

// TestPythonPatchRecordsSaveSrc: cmd_patch 的写入留痕 src 必须是 save (结构断言)。
//
// 与行为断言分工: 行为断言在 memory_write.py selftest 的 patch_records_save 向量
// (真跑一次 patch 再读留痕) —— 它抓"语义漂移但写法没变"; 本结构断言抓
// "代码被改回旧写法" (变异自检条目 memwrite_patch_marks_exempt 即打穿此处)。
func TestPythonPatchRecordsSaveSrc(t *testing.T) {
	raw, err := os.ReadFile(pyMemoryWritePath)
	if err != nil {
		t.Skipf("出口工具不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	src := string(raw)
	i := strings.Index(src, "def cmd_patch(")
	if i < 0 {
		t.Fatalf("%s 未找到 cmd_patch (判据锚点丢失)", pyMemoryWritePath)
	}
	body := src[i:]
	if j := strings.Index(body[1:], "\ndef "); j >= 0 {
		body = body[:j+1]
	}
	if !strings.Contains(body, "record_write(") {
		t.Fatalf("cmd_patch 内未找到 record_write —— 判据锚点丢失")
	}
	if strings.Contains(body, "SRC_MANUAL") {
		t.Fatalf("cmd_patch 把合规写入标成手工例外 (P4 缺陷原形): %s", pyMemoryWritePath)
	}
	if !strings.Contains(body, "SRC_SAVE") {
		t.Fatalf("cmd_patch 的写入留痕未用 SRC_SAVE: %s", pyMemoryWritePath)
	}
}
