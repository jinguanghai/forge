package main

// memory_recall_rank.go — 召回候选的打分与配额截取 (20261004)
//
// 从 memory_recall.go 拆出: 新增这些声明后该文件顶层结构指纹 (fractal F1)
// 由 6 涨到 9, 触发结构退化判据 —— 拆分是正解, 不是把守卫水位调高。
//
// 分区召回语义: key_findings 与 lessons 各自 BM25 排名、各自配额, 互不挤占
// (混池时任一池的条目数优势会把另一池挤出 topK)。

import "sort"

// recallItem 一条打分后的召回候选 (isLesson 区分来源池, 仅用于展示标记)。
type recallItem struct {
	kf       KeyFinding
	s        float64
	st       string
	age      int
	isLesson bool
}

// rankRecall 对单个候选池做 BM25 打分 × 新鲜度权重, 按得分降序返回。
func rankRecall(pool []KeyFinding, query []string, isLesson bool) []recallItem {
	if len(pool) == 0 {
		return nil
	}
	docs := make([][]string, len(pool))
	for i, kf := range pool {
		docs[i] = tokenizeCN(kf.Title + " " + kf.Content)
	}
	scores := bm25Score(docs, query, 1.2, 0.75)
	out := make([]recallItem, 0, len(pool))
	for i := range pool {
		st, age := freshnessOf(pool[i].TS, pool[i].Content)
		out = append(out, recallItem{pool[i], scores[i] * freshnessWeight(st), st, age, isLesson})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].s > out[j].s })
	return out
}

// takeTop 取前 n 条正得分候选 (输入已降序, 首个非正得分即终止)。
func takeTop(items []recallItem, n int) []recallItem {
	var out []recallItem
	for _, it := range items {
		if it.s <= 0 || len(out) >= n {
			break
		}
		out = append(out, it)
	}
	return out
}

// filterBySession 会话隔离: 当前会话非空时只保留"全局经验 + 本会话经验";
// 旧条目无 session 字段 = 全局经验, 全部保留 (向后兼容)。
func filterBySession(kfs []KeyFinding) []KeyFinding {
	sid := currentSession()
	if sid == "" {
		return kfs
	}
	out := make([]KeyFinding, 0, len(kfs))
	for _, kf := range kfs {
		if kf.Session == "" || kf.Session == sid {
			out = append(out, kf)
		}
	}
	return out
}
