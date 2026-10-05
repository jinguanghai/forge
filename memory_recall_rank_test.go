package main

// memory_recall_rank_test.go — 分区召回与配额截取的判据 (20261004)
//
// 钉住「两池各自排名、各自配额」这一语义: 若退化成混池, 任一池的条目数
// 优势会把另一池整体挤出 topK (lessons 有 67 条, key_findings 有 18 条,
// 混池后 key_findings 在高分词面上会被压掉)。

import (
	"fmt"
	"strings"
	"testing"
)

// ① 独立配额: key_findings 满额时 lessons 仍被召回 (混池则被挤掉)。
func TestRecallMemoryLessonsIndependentQuota(t *testing.T) {
	dir := t.TempDir()
	var kfs []interface{}
	for i := 0; i < 8; i++ {
		kfs = append(kfs, map[string]interface{}{
			"title":   fmt.Sprintf("前缀缓存%d", i),
			"content": fmt.Sprintf("DeepSeek 前缀缓存 命中率 锚点 %d (20261001)", i),
		})
	}
	writeMemFile(t, dir, map[string]interface{}{
		"key_findings": kfs,
		"lessons": []interface{}{
			map[string]interface{}{"title": "缓存铁律", "content": "前缀缓存 固定头必须恒定", "ts": "static"},
		},
	})
	block, n := RecallMemory(dir, "前缀缓存 命中率 铁律", 2)
	if n != 3 {
		t.Fatalf("配额应为 key_findings 2 + lessons 1 = 3, got %d\n%s", n, block)
	}
	if !strings.Contains(block, "·教训") {
		t.Fatalf("lessons 条目缺来源标记: %s", block)
	}
	if !strings.Contains(block, "缓存铁律") {
		t.Fatalf("lessons 条目未被召回 (被 key_findings 挤掉?): %s", block)
	}
}

// ② takeTop 遇非正得分即终止 (输入已降序, 首个非正得分之后全是非正)。
func TestTakeTopStopsAtNonPositive(t *testing.T) {
	items := []recallItem{{s: 3}, {s: 1}, {s: 0}, {s: -2}}
	if got := takeTop(items, 10); len(got) != 2 {
		t.Fatalf("非正得分应截断, got %d 条", len(got))
	}
	if got := takeTop(items, 1); len(got) != 1 || got[0].s != 3 {
		t.Fatalf("配额 1 应只取首条, got %+v", got)
	}
	if got := takeTop(nil, 5); len(got) != 0 {
		t.Fatalf("空池应返回空, got %d 条", len(got))
	}
}
