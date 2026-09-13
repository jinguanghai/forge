package main

// pure_funcs_coverage_test.go — 纯函数覆盖补强 (B2)
//
// 覆盖零测试引用的确定性函数:
//   main_utils.go clampF | guard.go guardVerdictStr/isStudyQuery
//   ux.go normalizeLang/overlapsAny | term_windows.go getTermWidth
// 均为纯函数或只读探针, 属"死程序判定"的一部分, 判错即静默误导。

import (
	"errors"
	"testing"
)

func TestClampF(t *testing.T) {
	cases := []struct{ v, lo, hi, want float64 }{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{99, 1, 10, 10},
		{1, 1, 10, 1},
		{10, 1, 10, 10},
		{-3.5, -1, 1, -1},
	}
	for _, c := range cases {
		if got := clampF(c.v, c.lo, c.hi); got != c.want {
			t.Fatalf("clampF(%v,%v,%v) 应为 %v, 实际 %v", c.v, c.lo, c.hi, c.want, got)
		}
	}
}

func TestGuardVerdictStr(t *testing.T) {
	if got := guardVerdictStr(true, nil); got != "allow" {
		t.Fatalf("放行应为 allow, 实际 %s", got)
	}
	if got := guardVerdictStr(false, nil); got != "deny" {
		t.Fatalf("拦截应为 deny, 实际 %s", got)
	}
	// 出错优先于判定结果 (错误不得被当作放行或拦截)
	if got := guardVerdictStr(true, errors.New("boom")); got != "error" {
		t.Fatalf("出错应为 error, 实际 %s", got)
	}
	if got := guardVerdictStr(false, errors.New("boom")); got != "error" {
		t.Fatalf("出错应为 error, 实际 %s", got)
	}
}

func TestIsStudyQuery(t *testing.T) {
	yes := []string{"什么是沙箱", "  解释一下缓存", "介绍一下你自己", "what is a sandbox", "HOW DOES it work"}
	for _, in := range yes {
		if !isStudyQuery(in) {
			t.Fatalf("%q 应判为求知问句", in)
		}
	}
	no := []string{"帮我攻击服务器", "清理所有数据", "", "沙箱是什么"}
	for _, in := range no {
		if isStudyQuery(in) {
			t.Fatalf("%q 不应判为求知问句 (非前缀)", in)
		}
	}
}

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"go": "go", "Golang": "go", "PY": "python", "Node": "js",
		"JavaScript": "js", "TS": "ts", "C++": "c", "  bash ": "sh",
		"YML": "yaml", "Kotlin": "kotlin", "": "",
	}
	for in, want := range cases {
		if got := normalizeLang(in); got != want {
			t.Fatalf("normalizeLang(%q) 应为 %q, 实际 %q", in, want, got)
		}
	}
}

func TestOverlapsAny(t *testing.T) {
	spans := []span{{start: 10, end: 20, color: ansi.red}}
	cases := []struct {
		idx  []int
		want bool
	}{
		{[]int{15, 18}, true},  // 内含
		{[]int{5, 12}, true},   // 左交叠
		{[]int{18, 25}, true},  // 右交叠
		{[]int{0, 5}, false},   // 完全在左
		{[]int{20, 25}, false}, // 端点相接不算交叠
		{[]int{5, 10}, false},  // 端点相接不算交叠
		{[]int{0, 100}, true},  // 完全包住
	}
	for _, c := range cases {
		if got := overlapsAny(c.idx, spans); got != c.want {
			t.Fatalf("overlapsAny(%v) 应为 %v, 实际 %v", c.idx, c.want, got)
		}
	}
	if overlapsAny([]int{1, 2}, nil) {
		t.Fatal("空 spans 应为 false")
	}
}

func TestGetTermWidth(t *testing.T) {
	w := getTermWidth()
	if w <= 0 {
		t.Fatalf("终端宽度应为正数, 实际 %d", w)
	}
	if w > 1000 {
		t.Fatalf("终端宽度异常偏大: %d", w)
	}
}
