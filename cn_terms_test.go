package main

// cn_terms_test.go — 中医术语整体成词的判据 (T29-E)
//
// 判据方向: 词典只做「合并」不做「删减」—— 未入表文本的分词结果必须逐字节
// 与加词典前一致 (否则是召回面改动, 不是改进)。

import (
	"strings"
	"testing"
)

// 「药对」必须整体成词 —— 病根现场: 停用字表里的「对」把它拆成「药」。
func TestTokenizeCNTermWinsOverStopChar(t *testing.T) {
	if !cnStopChars['对'] {
		t.Fatal("前提失效: 「对」不在停用字表里, 本用例的前提(被拆)不成立")
	}
	got := tokenizeCN("药对")
	if len(got) != 1 || got[0] != "药对" {
		t.Fatalf("「药对」应整体成词, 实得 %v", got)
	}
}

// 最长匹配优先: 「柴胡黄芩」不得被任何短词截断。
func TestTokenizeCNLongestMatchWins(t *testing.T) {
	got := tokenizeCN("柴胡黄芩")
	if len(got) != 1 || got[0] != "柴胡黄芩" {
		t.Fatalf("「柴胡黄芩」应整体成词, 实得 %v", got)
	}
}

// 混合句: 术语与药名整体成词, 其余仍按单字切 (只加不减)。
//
// 20261018 B9 行为变更: 药名全集入表后,「柴胡」「黄芩」由单字变整体成词 ——
// 这正是 B9 的目标 (单字切碎让 BM25 的 idf 失去区分度)。旧断言「未入表药名
// 不该成词」是「等主人定范围」期的占位判据, 定案后改为正向断言。
func TestTokenizeCNMixedSentence(t *testing.T) {
	got := tokenizeCN("查药对 柴胡 黄芩")
	joined := strings.Join(got, "|")
	if !strings.Contains(joined, "药对") {
		t.Fatalf("混合句里的「药对」未成词: %v", got)
	}
	for _, want := range []string{"柴胡", "黄芩"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("药名 %q 未整体成词 (B9: 药名全集入表): %v", want, got)
		}
	}
	// 未入表的普通中文仍按单字切 —— 词典只做合并, 不吞并整句
	if !strings.Contains(joined, "查") {
		t.Fatalf("单字路径被破坏 (词典吞了未入表文本): %v", got)
	}
}

// 只加不减: 与加词典前逐字节一致的样本 (英文/数字/标点/停用字过滤)。
func TestTokenizeCNUnchangedForNonTermText(t *testing.T) {
	cases := map[string][]string{
		"Hello World 42": {"hello", "world", "42"},
		"的了是在":           nil, // 全停用字 -> 空
		"a,b;c":          {"a", "b", "c"},
	}
	for in, want := range cases {
		got := tokenizeCN(in)
		if len(got) != len(want) {
			t.Fatalf("tokenizeCN(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("tokenizeCN(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

// 词典自洽: 无重复、无单字词 (单字进词典 = 与单字路径重复, 且会绕过停用字过滤)。
func TestCNTermsSelfConsistent(t *testing.T) {
	seen := map[string]bool{}
	// 遍历全集 (术语 + 药名): 只查 cnTerms 会让 466 味药名的重复/单字无人管
	for _, term := range cnTermAll() {
		if seen[term] {
			t.Errorf("词典重复条目: %q", term)
		}
		seen[term] = true
		if n := len([]rune(term)); n < 2 {
			t.Errorf("词典含单字词 %q —— 单字词会绕过停用字过滤, 语义与单字路径冲突", term)
		}
		if !cnTermSet[term] {
			t.Errorf("条目 %q 未进查表集 (派生失效)", term)
		}
	}
	if cnTermMaxRunes < 2 {
		t.Fatalf("最长词长 = %d, 词典可能为空", cnTermMaxRunes)
	}
	// 派生一致性: 最长词长必须等于全集真实最长, 否则最长匹配会漏掉长词前缀
	want := 0
	for _, term := range cnTermAll() {
		if n := len([]rune(term)); n > want {
			want = n
		}
	}
	if cnTermMaxRunes != want {
		t.Fatalf("cnTermMaxRunes = %d, 全集真实最长 = %d (派生失效)", cnTermMaxRunes, want)
	}
}
