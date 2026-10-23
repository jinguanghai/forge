package main

// cn_terms.go — 中文术语词典 (整体成词, 20261012 · T29-E)
//
// 病根: tokenizeCN 对中文是「单字即词」+ 停用字表。对中医语料这是灾难 ——
// 停用字表里有「对」, 于是「药对」被切成「药」;「柴胡黄芩」被切成 柴/胡/黄/芩,
// 其中「胡」还会与别的词撞车。查询「查药对 柴胡 黄芩」时文档与查询都碎成单字,
// BM25 的 idf 失去区分度 (单字到处都是, 谁都像命中)。
//
// 处置: 词典优先的最长匹配 —— 命中词典的词整体成词, 未命中的仍按单字切。
// 这是「只加不减」的改动: 单字路径与停用字过滤原样保留, 词典只把已知术语
// 合并成一个词项, 不改动未入表文本的行为 (回归风险最小)。
//
// 范围 (T29-E 两问, 20261018 B9 定案):
//
//	①「批量导入药名方剂名」→ 已导入药名全集 466 味 (cn_terms_herbs.go, 生成文件),
//	  数据源 knowledge/古籍库/herbs_466.txt, 零维护; 本文件只留通用术语。
//	②「柴胡黄芩」这类「药+药」→ 定案为「固定词条」, 不做动态组合识别:
//	  动态组合实测有害 —— 「炙甘草桂枝汤」被并成 ['炙','甘草桂枝','汤'],
//	  单独查任一药都不命中。
var cnTerms = []string{
	"药对",   // 施今墨药对派的核心概念, 被停用字「对」拆成「药」是首要缺陷
	"经方",   // 与「方剂」「时方」并列的固定术语
	"柴胡黄芩", // 药+药 举例: 固定词条形态 (动态组合识别待主人定)
}

// cnTermAll 词表全集 = 通用术语表 + 药名表。
// 单一派生点: 下面两个派生量都从这里取 —— 两处各写一份必然漂移。
func cnTermAll() []string {
	all := make([]string, 0, len(cnTerms)+len(cnHerbTerms))
	all = append(all, cnTerms...)
	return append(all, cnHerbTerms...)
}

// cnTermMaxRunes 词典最长词的字数 —— 最长匹配的起点。
var cnTermMaxRunes = func() int {
	m := 0
	for _, t := range cnTermAll() {
		if n := len([]rune(t)); n > m {
			m = n
		}
	}
	return m
}()

// cnTermSet 词典查表集 (由 cnTermAll 派生)。
var cnTermSet = func() map[string]bool {
	m := make(map[string]bool, len(cnTerms)+len(cnHerbTerms))
	for _, t := range cnTermAll() {
		m[t] = true
	}
	return m
}()

// matchCNTerm 在 runes 开头做最长匹配, 返回 (词, 字数); 未命中返回 ("", 0)。
// 必须从最长往短试: 短词先试会让「柴胡黄芩」被「柴胡」截断 (若两词都在表内)。
func matchCNTerm(runes []rune) (string, int) {
	for n := min(cnTermMaxRunes, len(runes)); n >= 2; n-- {
		if cnTermSet[string(runes[:n])] {
			return string(runes[:n]), n
		}
	}
	return "", 0
}
