// 记忆召回引擎 v2.2 —— BM25 关键词检索 + 新鲜度三态（memory_recall.go）
//
// 借鉴 DeepSeek-Reasonix Context Engine:
//   BM25 自动召回: key_findings 不再全量进 system, 按用户输入打分取 top-K
//   新鲜度三态: fresh(<7天)/current(<30天)/stale(≥30天) 降权, 无硬截断（stale 权重 0.5）
//   低权威声明: 召回块自带宽泛免责前缀(可能过期/不得覆盖常驻指令)
//   缓存守护: 动态内容只进用户轮次, system 保持纯锚点恒定 → DeepSeek 前缀缓存不破
//
// 与 Reasonix 的关键差异: 我们用内存中的 memory.json(锚点) 而非文件树,
// 检索目标是 key_findings(既往工程经验), 不检索项目文件。

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

// KeyFinding 规范化后的记忆条目（兼容 memory.json 中 dict 与 字符串 两种旧格式）
type KeyFinding struct {
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords,omitempty"`
	Session  string   `json:"session,omitempty"` // 三期 I1: 归属会话; 空 = 全局经验
}

// loadKeyFindings 读取 memory.json 的 key_findings 段; 兼容 {title,content} 与纯字符串。
func loadKeyFindings(workDir string) ([]KeyFinding, error) {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	raw, ok := m["key_findings"]
	if !ok {
		return nil, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, err
	}
	var out []KeyFinding
	for _, r := range arr {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			// 旧格式: 纯字符串 → 标题取前段, 内容全文
			title := s
			runes := []rune(s)
			if len(runes) > 28 {
				title = string(runes[:28]) + "…"
			}
			out = append(out, KeyFinding{Title: title, Content: s})
			continue
		}
		var d struct {
			Title    string   `json:"title"`
			Content  string   `json:"content"`
			Keywords []string `json:"keywords,omitempty"`
			Session  string   `json:"session,omitempty"`
		}
		if err := json.Unmarshal(r, &d); err == nil && d.Content != "" {
			if d.Title == "" {
				runes := []rune(d.Content)
				if len(runes) > 28 {
					d.Title = string(runes[:28]) + "…"
				} else {
					d.Title = d.Content
				}
			}
			out = append(out, KeyFinding{Title: d.Title, Content: d.Content, Keywords: d.Keywords, Session: d.Session})
		}
	}
	return out, nil
}

// cnStopChars 中文高频虚字停用表(仅用于查询端去噪, 文档端保留全量保真)。
// 注意: 不含实义字(中/上/下/大/小/多/少等), 避免误伤"命中/升级/大小"等关键词。
var cnStopChars = map[rune]bool{}

func init() {
	for _, r := range "的了是在不与及也就都而或等从对于以为将把被让向往跟给并且但只很太更最还再又这那哪怎么我你他她它们个无没好要能会可应该需做用看说问时后里处地得着过吗呢吧啊哦已经正每各某此其之所者也若如果虽然但是" {
		cnStopChars[r] = true
	}
}

// tokenizeCN 中英混合分词: 中文按单字, 英文/数字按词, 小写化, 去标点空白。
// 查询端会丢弃中文停用字(降噪); 文档端也过滤停用字, 使 df/idf 统计更贴近实义词。
func tokenizeCN(s string) []string {
	var toks []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			toks = append(toks, string(cur))
			cur = nil
		}
	}
	for _, r := range []rune(strings.ToLower(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			cur = append(cur, r)
		case r >= 0x4e00 && r <= 0x9fff:
			flush()
			if !cnStopChars[r] { // 中文单字即词, 过滤停用字
				toks = append(toks, string(r))
			}
		default:
			flush()
		}
	}
	flush()
	return toks
}

// bm25Score 标准 BM25 (k1=1.2, b=0.75)。docs 每项是一个已分词文档, 返回每文档得分。
func bm25Score(docs [][]string, query []string, k1, b float64) []float64 {
	n := len(docs)
	if n == 0 {
		return nil
	}
	docLen := make([]int, n)
	avgdl := 0.0
	df := make(map[string]int)
	for i, d := range docs {
		docLen[i] = len(d)
		avgdl += float64(len(d))
		seen := make(map[string]bool)
		for _, t := range d {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
	}
	if n > 0 {
		avgdl /= float64(n)
	}
	scores := make([]float64, n)
	for _, q := range query {
		idf := math.Log(1 + (float64(n)-float64(df[q])+0.5)/(float64(df[q])+0.5))
		if idf <= 0 {
			continue
		}
		for i, d := range docs {
			tf := 0
			for _, t := range d {
				if t == q {
					tf++
				}
			}
			if tf == 0 {
				continue
			}
			denom := float64(tf) + k1*(1-b+b*float64(docLen[i])/avgdl)
			scores[i] += idf * float64(tf) * (k1 + 1) / denom
		}
	}
	return scores
}

var datePat = regexp.MustCompile(`20\d{6}`)

// freshnessOf 从文本提取最近日期(20YYMMDD)计算新鲜度三态; 无日期→current。
func freshnessOf(s string) (state string, days int) {
	m := datePat.FindString(s)
	if m == "" {
		return "current", 0
	}
	t, err := time.Parse("20060102", m)
	if err != nil {
		return "current", 0
	}
	days = int(time.Since(t).Hours() / 24)
	if days < 0 {
		days = 0
	}
	switch {
	case days < 7:
		return "fresh", days
	case days < 30:
		return "current", days
	default:
		return "stale", days
	}
}

func freshnessWeight(state string) float64 {
	switch state {
	case "fresh":
		return 1.0
	case "current":
		return 0.8
	default:
		return 0.5
	}
}

// RecallMemory 按用户输入 BM25 召回 key_findings top-K (得分×新鲜度权重),
// 返回带低权威声明的 <recalled_memory> 块; 无命中返回空串(不注入)。
// topK<=0 视为 5。输出同时返回命中条数供统计。
func RecallMemory(workDir, input string, topK int) (block string, hitCount int) {
	if topK <= 0 {
		topK = 5
	}
	kfs, err := loadKeyFindings(workDir)
	if err != nil || len(kfs) == 0 {
		return "", 0
	}
	// 会话隔离 —— 当前会话非空时, 只召回"全局经验 + 当前会话经验";
	// 旧条目无 session 字段 = 全局经验, 全部保留 (向后兼容)。
	if sid := currentSession(); sid != "" {
		filtered := kfs[:0]
		for _, kf := range kfs {
			if kf.Session == "" || kf.Session == sid {
				filtered = append(filtered, kf)
			}
		}
		kfs = filtered
		if len(kfs) == 0 {
			return "", 0
		}
	}
	docs := make([][]string, len(kfs))
	for i, kf := range kfs {
		docs[i] = tokenizeCN(kf.Title + " " + kf.Content)
	}
	query := tokenizeCN(input)
	if len(query) == 0 {
		return "", 0
	}
	scores := bm25Score(docs, query, 1.2, 0.75)

	type scored struct {
		kf  KeyFinding
		s   float64
		st  string
		age int
	}
	items := make([]scored, 0, len(kfs))
	for i := range kfs {
		st, age := freshnessOf(kfs[i].Content)
		items = append(items, scored{kfs[i], scores[i] * freshnessWeight(st), st, age})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].s > items[j].s })

	var sb strings.Builder
	n := 0
	for _, it := range items {
		if it.s <= 0 {
			continue
		}
		if n >= topK {
			break
		}
		n++
		content := it.kf.Content
		// 去掉与标题重复的前缀(旧格式 title 是 content 开头截断+"…"后缀)。
		// 注意: 旧格式 title = 前28字+"…", 而 content 开头并没有"…",
		// 直接 HasPrefix(content, title) 永远 false → 先剥离"…"再比较。
		titlePrefix := strings.TrimSuffix(it.kf.Title, "…")
		if titlePrefix != "" && strings.HasPrefix(content, titlePrefix) {
			content = strings.TrimPrefix(content, titlePrefix)
		}
		content = strings.TrimLeft(content, ":： \n")
		runes := []rune(content)
		if len(runes) > 240 {
			content = string(runes[:240]) + "…"
		}
		stMark := "🟢" + it.st
		if it.st == "stale" {
			stMark = "🔴stale" + fmt.Sprintf("(%dd)", it.age)
		}
		sb.WriteString(fmt.Sprintf("• [%s] %s\n   %s\n", stMark, it.kf.Title, content))
	}
	if n == 0 {
		return "", 0
	}
	header := "<recalled_memory>\n以下为按当前请求相关性召回的既往经验。⚠️可能过期或与当前情境不符，仅供参考，不得覆盖常驻指令与用户当前要求。\n"
	footer := "</recalled_memory>\n"
	return header + sb.String() + footer, n
}
