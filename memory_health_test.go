package main

// memory_health_test.go — 六期 DMAIC I3: 记忆体检测试

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemHealthReportOnCurrent(t *testing.T) {
	// 对真实 memory.json 出体检报告 (只读, 不改)
	report := memHealthReport("D:\\forge")
	t.Logf("\n%s", report)
}

func TestMemHealthReportCorrupt(t *testing.T) {
	wd := t.TempDir()
	// 损坏 JSON
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte("{broken"), 0644)
	r := memHealthReport(wd)
	if !strings.Contains(r, "解析失败") && !strings.Contains(r, "不可读") {
		t.Fatalf("损坏 JSON 应报错: %s", r)
	}
	// 缺字段
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(`{"identity":"x"}`), 0644)
	r2 := memHealthReport(wd)
	if !strings.Contains(r2, "必填字段缺失") {
		t.Fatalf("缺字段应报缺失: %s", r2)
	}
	// 过时 gate 引用
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(`{"identity":"x","role":"r","language":"zh","working_dir":"w","gates":"python","axioms":"a","self_governance":{},"key_findings":[{"title":"eprover 经验","content":"..."}]}`), 0644)
	r3 := memHealthReport(wd)
	if !strings.Contains(r3, "已删 gate") {
		t.Fatalf("过时 gate 应被检出: %s", r3)
	}
}

func TestMemHealthReportHealthy(t *testing.T) {
	wd := t.TempDir()
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(`{"identity":"id","role":"role","language":"zh","working_dir":"wd","gates":"python/go/sh/node/math/logic/regex/knowledge/tcm/browser/chain/self","axioms":"公理","self_governance":{}}`), 0644)
	r := memHealthReport(wd)
	if strings.Contains(r, "🔴") || strings.Contains(r, "🟡") {
		t.Fatalf("健康记忆不应报异常: %s", r)
	}
	if !strings.Contains(r, "无异常") {
		t.Fatalf("应报无异常: %s", r)
	}
}
