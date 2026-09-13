package main

// memory_write_sentinel_test.go — 写入路径哨兵正反用例
//
// 覆盖: ① 无主文件不误报 ② 无留痕基线只提示不报警 ③ 基线建立后通过
// ④ 基线幂等 ⑤ 旁路写入被抓 ⑥ SaveMemory 留痕含写入内容 sha
// ⑦ HealMemory 恢复不误报

import (
	"os"
	"path/filepath"
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
	if !strings.Contains(msg, "无 SaveMemory 留痕") {
		t.Fatalf("报警文案不符: %s", msg)
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
