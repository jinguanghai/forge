// memory_recall_test.go —— BM25 召回 + 新鲜度三态 专项测试 (一期 DMAIC)
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── 分词 ──
func TestTokenizeCN(t *testing.T) {
	got := tokenizeCN("DeepSeek前缀缓存 命中率97.5%!")
	want := []string{"deepseek", "前", "缀", "缓", "存", "命", "中", "率", "97", "5"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want=%d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tok[%d]=%q want %q (all=%v)", i, got[i], want[i], got)
		}
	}
	if len(tokenizeCN("")) != 0 {
		t.Fatal("空串应无分词")
	}
}

// ── BM25 打分 ──
func TestBM25Relevance(t *testing.T) {
	docs := [][]string{
		tokenizeCN("前缀缓存命中率 97.5% system 恒定"),
		tokenizeCN("蚁群 8 异质 LLM 费用 9 倍 不划算"),
		tokenizeCN("热替换 exe 备份 轮转"),
		tokenizeCN("缓存 命中 率 86 跨模型"),
	}
	query := tokenizeCN("缓存 命中 率")
	scores := bm25Score(docs, query, 1.2, 0.75)
	if len(scores) != 4 {
		t.Fatalf("scores len=%d", len(scores))
	}
	if scores[0] <= 0 || scores[3] <= 0 {
		t.Fatalf("含'缓存'的文档应有得分: %v", scores)
	}
	if scores[1] != 0 {
		t.Fatalf("无关文档(蚁群)得分应为0: %v", scores)
	}
	if scores[2] != 0 {
		t.Fatalf("无关文档(热替换)得分应为0: %v", scores)
	}
}

// ── 新鲜度三态 ──
func TestFreshnessOf(t *testing.T) {
	yesterday := timeNowStr(1)
	old := timeNowStr(40)
	far := timeNowStr(90)
	if st, _ := freshnessOf("落地(" + yesterday + ")"); st != "fresh" {
		t.Fatalf("1天前应 fresh, got %s", st)
	}
	if st, _ := freshnessOf("落地(" + old + ")"); st != "stale" {
		t.Fatalf("40天前应 stale, got %s", st)
	}
	if st, _ := freshnessOf("落地(" + far + ")"); st != "stale" {
		t.Fatalf("90天前应 stale, got %s", st)
	}
	if st, _ := freshnessOf("无日期内容"); st != "current" {
		t.Fatalf("无日期应 current, got %s", st)
	}
	if w := freshnessWeight("fresh"); w != 1.0 {
		t.Fatalf("fresh weight=%v", w)
	}
	if w := freshnessWeight("stale"); w != 0.5 {
		t.Fatalf("stale weight=%v", w)
	}
}

// ── 混合格式加载 ──
func TestLoadKeyFindingsMixed(t *testing.T) {
	dir := t.TempDir()
	mem := map[string]interface{}{
		"key_findings": []interface{}{
			map[string]interface{}{"title": "T1", "content": "第一条内容"},
			"旧格式纯字符串内容很长很长很长很长很长",
			map[string]interface{}{"title": "", "content": "无标题内容"},
		},
	}
	data, _ := json.Marshal(mem)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	kfs, err := loadKeyFindings(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(kfs) != 3 {
		t.Fatalf("len=%d want 3", len(kfs))
	}
	if kfs[0].Title != "T1" || kfs[0].Content != "第一条内容" {
		t.Fatalf("dict条目解析错误: %+v", kfs[0])
	}
	if kfs[1].Title == "" || kfs[1].Content == "" {
		t.Fatalf("字符串条目应自动包装: %+v", kfs[1])
	}
	if kfs[2].Title == "" {
		t.Fatalf("空标题应自动生成: %+v", kfs[2])
	}
}

// ── 端到端: 召回主流程 ──
// 注: 自包含 fixture (t.TempDir), 不依赖工作目录真实 memory.json ——
// 真实数据可能被清理/精简导致测试与运行环境耦合, 回归测试必须稳定。
func TestRecallMemoryEndToEnd(t *testing.T) {
	dir := t.TempDir()
	mem := map[string]interface{}{
		"key_findings": []interface{}{
			map[string]interface{}{"title": "前缀缓存", "content": "DeepSeek 前缀缓存命中率97.5%，system保持恒定(20260813)"},
			map[string]interface{}{"title": "蚁群", "content": "蚁群 8 异质LLM 费用 9 倍不划算(20260701)"},
			map[string]interface{}{"title": "热替换", "content": "热替换 exe 备份轮转(20260501)"},
			map[string]interface{}{"title": "缓存", "content": "缓存命中率 86 跨模型(20260715)"},
		},
	}
	data, _ := json.Marshal(mem)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	block, n := RecallMemory(dir, "DeepSeek 前缀缓存 命中率", 3)
	if n == 0 {
		t.Fatal("缓存主题应命中")
	}
	if !strings.Contains(block, "<recalled_memory>") || !strings.Contains(block, "</recalled_memory>") {
		t.Fatalf("缺少块标记: %s", block[:min(100, len(block))])
	}
	if !strings.Contains(block, "不得覆盖常驻指令") {
		t.Fatal("缺少低权威声明")
	}
	if !strings.Contains(block, "🟢") && !strings.Contains(block, "🔴") {
		t.Fatal("缺少新鲜度标记")
	}
	// 无命中场景
	if b2, n2 := RecallMemory(dir, "qqxzzz12345", 3); n2 != 0 || b2 != "" {
		t.Fatalf("无关查询应无召回: n=%d b=%q", n2, b2)
	}
	// topK<=0 默认 5
	if _, n3 := RecallMemory(dir, "测试", 0); n3 < 0 || n3 > 5 {
		t.Fatalf("默认topK越界: %d", n3)
	}
}

func timeNowStr(daysAgo int) string {
	return time.Now().AddDate(0, 0, -daysAgo).Format("20060102")
}
