package main

// cn_terms_herbs_test.go — 中药名词典的判据 (T29-E/B9, 20261018)
//
// 病根: 药名被单字切碎后 BM25 的 idf 失去区分度; 21 味药名含停用字
// (地榆/太子参/没药/白及/草果/自然铜…) 会被停用字表直接吞掉整字。
//
// 判据方向: ①词表自洽 ②派生有效 ③与数据源不漂移 ④真实病根样本整体成词
// ⑤不做「药+药」动态组合 (实测有害, 见 cn_terms.go 范围段)

import (
	"os"
	"strings"
	"testing"
)

// herbSourcePath 药名数据源 (knowledge/ 被 .gitignore 忽略, 干净 clone 上不存在)。
const herbSourcePath = "knowledge/古籍库/herbs_466.txt"

// ① 词表自洽: 无重复、无单字、纯中文、规模下限。
func TestCNHerbTermsSelfConsistent(t *testing.T) {
	if len(cnHerbTerms) < 460 {
		t.Fatalf("药名表仅 %d 条 (应 >=460) —— 词表被截断或生成失败", len(cnHerbTerms))
	}
	seen := map[string]bool{}
	for _, h := range cnHerbTerms {
		if seen[h] {
			t.Errorf("药名重复: %q", h)
		}
		seen[h] = true
		if n := len([]rune(h)); n < 2 {
			t.Errorf("单字药名 %q —— 会绕过停用字过滤, 与单字路径语义冲突", h)
		}
		for _, r := range h {
			if r < 0x4e00 || r > 0x9fff {
				t.Errorf("药名 %q 含非汉字字符 %q (数据源被污染?)", h, r)
			}
		}
	}
}

// ② 派生有效: 每条药名都必须进查表集, 否则最长匹配永远命不中。
func TestCNHerbTermsAllIndexed(t *testing.T) {
	for _, h := range cnHerbTerms {
		if !cnTermSet[h] {
			t.Errorf("药名 %q 未进 cnTermSet (cnTermAll 派生失效)", h)
		}
	}
}

// ③ 不漂移: 词表必须与数据源逐条一致 (顺序无关, 重复计数)。
// 数据源不存在时 Skip —— 必须显式说明「未验」, 不得静默绿。
func TestCNHerbTermsSourceMatch(t *testing.T) {
	raw, err := os.ReadFile(herbSourcePath)
	if err != nil {
		t.Skipf("数据源不可读 (%v) —— knowledge/ 未入库, 本判据在干净 clone 上未验", err)
	}
	var want []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if s := strings.TrimSpace(ln); s != "" {
			want = append(want, s)
		}
	}
	got := map[string]int{}
	for _, h := range cnHerbTerms {
		got[h]++
	}
	for _, w := range want {
		got[w]--
	}
	var missing, extra []string
	for k, v := range got {
		if v < 0 {
			missing = append(missing, k)
		} else if v > 0 {
			extra = append(extra, k)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("词表与数据源漂移: 缺 %v / 多 %v (改了数据源须重排 cn_terms_herbs.go)", missing, extra)
	}
}

// ④ 最长匹配: 5 字药名不得被任何短词截断。
func TestTokenizeCNLongestHerbTerm(t *testing.T) {
	got := tokenizeCN("白花蛇舌草")
	if len(got) != 1 || got[0] != "白花蛇舌草" {
		t.Fatalf("「白花蛇舌草」应整体成词, 实得 %v", got)
	}
}

// ④ 含停用字的药名不得被停用字表吞掉整字 (病根现场)。
func TestTokenizeCNHerbWithStopChar(t *testing.T) {
	cases := map[string]string{
		"地榆":   "地",
		"太子参":  "太",
		"没药":   "没",
		"白及":   "及",
		"草果":   "果",
		"王不留行": "不",
	}
	for herb, stopChar := range cases {
		if !cnStopChars[[]rune(stopChar)[0]] {
			t.Fatalf("前提失效: %q 不在停用字表, %q 的用例前提不成立", stopChar, herb)
		}
		got := tokenizeCN(herb)
		if len(got) != 1 || got[0] != herb {
			t.Fatalf("%q 含停用字 %q, 应整体成词, 实得 %v", herb, stopChar, got)
		}
	}
}

// ⑤ 不做动态组合: 相邻两药各自成词, 不得并成一个词。
// 病根: 「炙甘草桂枝汤」曾被并成 ['炙','甘草桂枝','汤'], 单独查任一药都不命中。
func TestTokenizeCNNoHerbCombination(t *testing.T) {
	got := tokenizeCN("炙甘草桂枝汤")
	joined := strings.Join(got, "|")
	if strings.Contains(joined, "甘草桂枝") {
		t.Fatalf("「甘草桂枝」被并成一词 —— 动态组合有害, 只收药名本名: %v", got)
	}
	for _, want := range []string{"甘草", "桂枝"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("药名 %q 未成词: %v", want, got)
		}
	}
}

// ⑤ 固定词条优先于拆分: 「柴胡黄芩」是写死的药对术语, 应整体成词。
func TestTokenizeCNFixedPairWinsOverHerbs(t *testing.T) {
	got := tokenizeCN("柴胡黄芩")
	if len(got) != 1 || got[0] != "柴胡黄芩" {
		t.Fatalf("固定词条「柴胡黄芩」应整体成词 (不得被两味药拆开), 实得 %v", got)
	}
}
