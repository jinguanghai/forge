package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveMemoryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	v1 := []byte(`{"version":1,"note":"第一版"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	// 主文件与 .bak 都存在且一致
	mainData, err := os.ReadFile(filepath.Join(dir, "memory.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(mainData) != string(v1) {
		t.Fatalf("主文件内容不符: %s", mainData)
	}
	bakData, err := os.ReadFile(filepath.Join(dir, "memory.json.bak"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bakData) != string(v1) {
		t.Fatalf(".bak 内容不符: %s", bakData)
	}
	// LoadMemory 一致
	got, recovered, err := LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if recovered {
		t.Fatal("首次保存不应触发回退")
	}
	if string(got) != string(v1) {
		t.Fatalf("LoadMemory 内容不符: %s", got)
	}
}

func TestLoadMemoryFallsBackToBackup(t *testing.T) {
	dir := t.TempDir()
	v1 := []byte(`{"version":1,"note":"第一版"}`)
	v2 := []byte(`{"version":2,"note":"第二版"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemory(dir, v2); err != nil {
		t.Fatal(err)
	}
	// 保存 v2 后 .bak 应为 v1
	bakData, _ := os.ReadFile(filepath.Join(dir, "memory.json.bak"))
	if string(bakData) != string(v1) {
		t.Fatalf(".bak 应为 v1: %s", bakData)
	}
	// 模拟主文件写半截(崩溃残留)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{"version":2,`), 0644); err != nil {
		t.Fatal(err)
	}
	got, recovered, err := LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("应触发回退")
	}
	if string(got) != string(v1) {
		t.Fatalf("回退内容应为 v1: %s", got)
	}
}

func TestHealMemoryRecovers(t *testing.T) {
	dir := t.TempDir()
	v1 := []byte(`{"version":1,"note":"第一版"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	// 写坏主文件
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{broken`), 0644); err != nil {
		t.Fatal(err)
	}
	recovered, err := HealMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("应报告已恢复")
	}
	// 主文件已恢复为 v1
	mainData, _ := os.ReadFile(filepath.Join(dir, "memory.json"))
	if !json.Valid(mainData) || string(mainData) != string(v1) {
		t.Fatalf("自愈后主文件不符: %s", mainData)
	}
	// 正常文件再次 HealMemory → 不恢复
	recovered2, err := HealMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if recovered2 {
		t.Fatal("正常文件不应报告恢复")
	}
}

func TestSaveMemoryKeepsValidBackup(t *testing.T) {
	dir := t.TempDir()
	v1 := []byte(`{"version":1,"note":"第一版"}`)
	v2 := []byte(`{"version":2,"note":"第二版"}`)
	if err := SaveMemory(dir, v1); err != nil {
		t.Fatal(err)
	}
	if err := SaveMemory(dir, v2); err != nil {
		t.Fatal(err)
	}
	// 主文件损坏时 SaveMemory → 坏文件不应覆盖现有 .bak
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{"bad`), 0644); err != nil {
		t.Fatal(err)
	}
	v3 := []byte(`{"version":3,"note":"第三版"}`)
	if err := SaveMemory(dir, v3); err != nil {
		t.Fatal(err)
	}
	bakData, _ := os.ReadFile(filepath.Join(dir, "memory.json.bak"))
	if string(bakData) != string(v1) {
		t.Fatalf("坏文件不应覆盖 .bak: %s", bakData)
	}
	mainData, _ := os.ReadFile(filepath.Join(dir, "memory.json"))
	if string(mainData) != string(v3) {
		t.Fatalf("主文件应为 v3: %s", mainData)
	}
}

func TestLoadMemoryBackupOnly(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := LoadMemory(dir); err == nil {
		t.Fatal("空目录应报错")
	}
	// 只有 .bak 无主文件 → 回退成功
	if err := os.WriteFile(filepath.Join(dir, "memory.json.bak"), []byte(`{"version":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	got, recovered, err := LoadMemory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered {
		t.Fatal("应报告从 .bak 恢复")
	}
	if string(got) != `{"version":1}` {
		t.Fatalf("内容不符: %s", got)
	}
}

func TestSaveMemoryRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := SaveMemory(dir, []byte(`not json`)); err == nil {
		t.Fatal("非法 JSON 应被拒绝")
	}
}
