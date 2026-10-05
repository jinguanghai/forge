package main

// memory_lessons.go — lessons 经验教训池的读取与标题生成 (20261004)
//
// 从 memory_recall.go 拆出: 新增这些声明后该文件顶层结构指纹 (fractal F1)
// 由 6 涨到 9, 触发结构退化判据 —— 拆分是正解, 不是把守卫水位调高。
//
// lessons 原是单个 str 字段 (10352 字符) 全量进 system 固定头, str 不可切分
// → BM25 无从召回 → 只增不减; 现为结构化数组, 由 RecallMemory 以独立配额
// (lessonsRecallTopK) 召回, 常驻硬约束另存 lessons_core (锚点字段, 留在固定头)。

import (
	"encoding/json"
	"strings"
)

// lessonsRecallTopK lessons 段独立召回配额 —— 不与 key_findings 混池竞争
// (混池时 68 条 lessons 会把 key_findings 挤出 topK, 反之亦然; 两池各自排名、各自配额)。
const lessonsRecallTopK = 3

// loadLessons 读取 memory.json 的 lessons 段 (经验教训池, 与 key_findings 同构)。
//
// 兼容两种格式:
//   - 旧格式 str: 整段按行切分, 每行一条 (迁移前数据仍可召回, 不需先改数据);
//   - 新格式 list: [{ts,title,content,keywords}] —— ts 是新鲜度锚点, 缺 ts 时
//     由 freshnessOf 回退正文日期, 仍无则 fail-closed 判 stale 降权。
func loadLessons(workDir string) ([]KeyFinding, error) {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	raw, ok := m["lessons"]
	if !ok {
		return nil, nil
	}
	// 旧格式: 整段字符串 → 按行切分
	var blob string
	if err := json.Unmarshal(raw, &blob); err == nil {
		var out []KeyFinding
		for _, line := range strings.Split(blob, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			out = append(out, KeyFinding{Title: lessonTitle(line), Content: line})
		}
		return out, nil
	}
	// 新格式: 结构化数组
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	var out []KeyFinding
	for _, r := range arr {
		var d struct {
			Title    string   `json:"title"`
			Content  string   `json:"content"`
			Keywords []string `json:"keywords,omitempty"`
			TS       string   `json:"ts,omitempty"`
		}
		if err := json.Unmarshal(r, &d); err != nil || d.Content == "" {
			continue
		}
		if d.Title == "" {
			d.Title = lessonTitle(d.Content)
		}
		out = append(out, KeyFinding{Title: d.Title, Content: d.Content, Keywords: d.Keywords, TS: d.TS})
	}
	return out, nil
}

// lessonTitle 取条目首句作标题 (≤28 字, 与 key_findings 旧格式同口径), 供召回块展示。
func lessonTitle(s string) string {
	head := s
	for _, sep := range []string{"。", ";", "；", "\n"} {
		if i := strings.Index(s, sep); i > 0 {
			head = s[:i]
			break
		}
	}
	runes := []rune(head)
	if len(runes) > 28 {
		return string(runes[:28]) + "…"
	}
	return head
}
