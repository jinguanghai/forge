package main

// ── nosword_wire_test.go — 无剑接线哨兵 (孤儿代码防线) ──
//
// 教训(20260910): nosword.go 曾实现完整+单测全绿, 却零调用接线 ——
// 设 FORGE_NOSWORD=1 也不生效。『单测通过』≠『功能可用』。
// 本文件把"验收必须查调用点"固化为死程序判定:
//   1) 主干接线点必须存在于 agent.go (无剑反馈注入 + 虚报检测)
//   2) nosword.go 中每个顶层函数必须至少有一处调用点 (无孤儿)

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNSWWire_CallSitesExist 主干接线点存在性
func TestNSWWire_CallSitesExist(t *testing.T) {
	agent, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("读 agent.go 失败: %v", err)
	}
	src := string(agent)
	cases := []struct {
		call string
		why  string
	}{
		{"nswEnabled()", "无剑开关未接线 -> FORGE_NOSWORD=1 不生效"},
		{"nswFeedbackTextFresh(", "无剑复述过滤未接线 -> 自激循环不可控(引用算式被反复反馈)"},
		{"nswAudit(", "无剑埋点未接线 -> 触发率/空转率不可测, 扩语法只能盲扩"},
		{"maxNSWRounds", "无剑轮次上限缺失 -> 可能死循环"},
		{"verifyClaimEnabled()", "虚报检测开关未接线"},
		{"detectUnverifiedClaim(", "虚报检测判据未接线"},
		{"maxVerifyStrikes", "虚报强干预上限缺失 -> 可能死循环"},
	}
	for _, c := range cases {
		if !strings.Contains(src, c.call) {
			t.Errorf("接线缺失: agent.go 未出现 %q (%s)", c.call, c.why)
		}
	}
}

// TestNSWWire_NoOrphanFunctions nosword.go 顶层函数零调用检测
func TestNSWWire_NoOrphanFunctions(t *testing.T) {
	// 收集非测试的 Go 源文件
	files, _ := filepath.Glob("*.go")
	var corpus strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		corpus.Write(b)
		corpus.WriteString("\n")
	}
	all := corpus.String()

	ns, err := os.ReadFile("nosword.go")
	if err != nil {
		t.Fatalf("读 nosword.go 失败: %v", err)
	}
	re := regexp.MustCompile(`(?m)^func (\w+)\(`)
	orphans := []string{}
	for _, m := range re.FindAllStringSubmatch(string(ns), -1) {
		name := m[1]
		// 定义行本身占 1 次匹配, 其余为调用/引用
		defs := strings.Count(all, "func "+name+"(")
		refs := len(regexp.MustCompile(`\b`+name+`\s*\(`).FindAllString(all, -1)) - defs
		// 也可能作为值被传递(无括号), 额外统计
		refs += len(regexp.MustCompile(`\b`+name+`\b`).FindAllString(all, -1)) - defs
		if refs <= 0 {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		t.Errorf("孤儿函数(零调用点): %v — 实现完整但未接线, 设开关也不生效", orphans)
	}
	t.Logf("nosword.go 顶层函数全部有调用点, 无孤儿")
}
