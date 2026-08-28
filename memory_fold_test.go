package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 折叠展开 v2.1: 半显化/深入/π回写/相关性/节律诊断
func TestFoldUnfoldRoundTrip(t *testing.T) {
	dir := t.TempDir()
	foldDir := filepath.Join(dir, "_archive", "folded_memory")
	if err := os.MkdirAll(foldDir, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(foldDir, "test_001.md")
	if err := os.WriteFile(archive, []byte("# 测试任务\n- 摘要: 测试折叠\n- 细节: hello 内容\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mem := `{"folded_memory":{"version":2,"items":[{"id":"test_001","name":"测试任务","status":"done","summary":"测试折叠","archive":"_archive/folded_memory/test_001.md","folded_at":"20260805","last_unfolded":"","tier":1,"pi_expansions":0,"phi_folds":0,"keywords":["测试","折叠"]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(mem), 0644); err != nil {
		t.Fatal(err)
	}

	// M2 半显化: 摘要先行, 不含全文细节
	prev, err := UnfoldPreview(dir, "测试任务")
	if err != nil {
		t.Fatalf("preview err: %v", err)
	}
	if !strings.Contains(prev, "测试折叠") || !strings.Contains(prev, "深入测试任务") {
		t.Fatalf("preview 缺失摘要/深入提示: %s", prev)
	}
	if strings.Contains(prev, "hello") {
		t.Fatalf("半显化不应含全文: %s", prev)
	}

	// M2 深入: 读全文
	deep, err := UnfoldDeep(dir, "测试任务")
	if err != nil {
		t.Fatalf("deep err: %v", err)
	}
	if !strings.Contains(deep, "hello") {
		t.Fatalf("deep 内容缺失: %s", deep)
	}

	// M4 循环回写: π=2, last_unfolded 非空
	items, _ := foldedItems(dir)
	if len(items) != 1 || items[0].PiExpansions != 2 || items[0].LastUnfolded == "" {
		t.Fatalf("π回写失败: %+v", items)
	}

	// 总账
	lst := ListFoldedTasks(dir)
	if !strings.Contains(lst, "测试任务") || !strings.Contains(lst, "展开测试任务") {
		t.Fatalf("list 缺失: %s", lst)
	}

	if _, err := UnfoldDeep(dir, "不存在的任务"); err == nil {
		t.Fatal("应返回未找到错误")
	}

	ledger, err := UnfoldPreview(dir, "任务总账")
	if err != nil || !strings.Contains(ledger, "测试任务") {
		t.Fatalf("总账展开失败: %v %s", err, ledger)
	}
}

func TestMatchFoldUnfoldCmd(t *testing.T) {
	cases := map[string]int{
		"展开输入端修复":  1,
		"展开 输入端修复": 1,
		"展开任务总账":   1,
		"深入输入端修复":  2,
		"深入任务总账":   2,
		"展开讲讲你的思路": 1, // 指令形态命中, 未命中索引时 fall through 给 LLM
		"继续":       0,
		"帮助":       0,
		"展开":       0, // 空名称不拦截
		"展开 ":      0,
		"深入":       0,
	}
	for in, want := range cases {
		got, _ := matchFoldUnfoldCmd(in)
		if got != want {
			t.Errorf("%q: got %v want %v", in, got, want)
		}
	}
}

// M3 相关性触发: 关键词命中 + 24h去重
func TestFindRelevantFold(t *testing.T) {
	dir := t.TempDir()
	mem := `{"folded_memory":{"version":2,"items":[{"id":"a1","name":"蚁群研究","status":"done","summary":"蚁群LLM研究","archive":"a.md","folded_at":"20260801","last_unfolded":"","tier":1,"pi_expansions":0,"phi_folds":0,"keywords":["蚁群","异质LLM"]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(mem), 0644); err != nil {
		t.Fatal(err)
	}

	// 命中
	rel := FindRelevantFold(dir, "蚁群实验怎么做")
	if rel == nil || rel.Name != "蚁群研究" {
		t.Fatalf("应命中蚁群: %+v", rel)
	}
	MarkHinted(dir, rel.ID)

	// 24h内去重
	if rel2 := FindRelevantFold(dir, "蚁群再试试"); rel2 != nil {
		t.Fatalf("24h内不应重复提示: %+v", rel2)
	}

	// 无关输入不命中
	if rel3 := FindRelevantFold(dir, "今天天气如何"); rel3 != nil {
		t.Fatalf("无关输入不应命中: %+v", rel3)
	}
}

// M6 节律诊断: 指标采集+八极向量
func TestMemDiagMetrics(t *testing.T) {
	dir := t.TempDir()
	mem := `{"folded_memory":{"version":2,"items":[
		{"id":"a1","name":"任务甲","status":"done","summary":"甲","archive":"a.md","folded_at":"20260101","last_unfolded":"","tier":1,"pi_expansions":0,"phi_folds":1},
		{"id":"a2","name":"任务乙","status":"done","summary":"乙","archive":"b.md","folded_at":"20260101","last_unfolded":"20260801","tier":2,"pi_expansions":1,"phi_folds":1},
		{"id":"a3","name":"任务丙","status":"done","summary":"丙","archive":"c.md","folded_at":"20260101","last_unfolded":"20260805","tier":1,"pi_expansions":2,"phi_folds":1}
	]}}`
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(mem), 0644); err != nil {
		t.Fatal(err)
	}
	md := MemDiagMetrics(dir)
	if md == nil || md.Total != 3 || md.NeverUnfold != 1 || md.TotalPi != 3 || md.Tier2Count != 1 {
		t.Fatalf("指标错误: %+v", md)
	}
	if len(md.Vector) != 8 {
		t.Fatalf("八极向量缺失: %v", md.Vector)
	}
	if len(md.Advice) == 0 {
		t.Fatal("应有诊断建议")
	}
	if !strings.Contains(md.Summary, "3项") {
		t.Fatalf("summary 异常: %s", md.Summary)
	}
}

// 空系统: 无折叠任务时诊断兜底
func TestMemDiagEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), []byte(`{"identity":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	md := MemDiagMetrics(dir)
	if md == nil || md.Summary == "" {
		t.Fatalf("空系统诊断失败: %+v", md)
	}
}
