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

// TestNSWWire_BothExitSitesSymmetric 双收尾出口对称性 (20260912 缺陷O)
//
// 教训: agent.go 的最终收尾有两个出口 —— ①无工具调用的纯文本回复 ②工具调用
// 碎片全无效降级为纯文本。功能只接一处 = 另一条路径上的算式错值静默漏过,
// 且单测(直接调 nosword.go 函数)永远发现不了。把"两处必须对称"固化为死程序判定。
func TestNSWWire_BothExitSitesSymmetric(t *testing.T) {
	agent, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("读 agent.go 失败: %v", err)
	}
	src := string(agent)
	checks := []struct {
		pat  string
		want int
		why  string
	}{
		{"nswIntervene(asst)", 2, "无剑干预调用点必须两处收尾出口各一 (缺失=该路径无剑静默失效)"},
		{`ChatMessage{Role: "user", Content: fb}`, 2, "无剑反馈注入点必须两处 (缺失=拦住了却不反馈=白拦)"},
		{"nswProbeAudit(a, asst, nswRounds", 2, "无剑分母埋点必须两处 (缺失=触发率分母有洞)"},
		{"nswIntervene := func(asst string) string", 1, "无剑干预必须走统一闭包, 禁止两处各写一份(行为会漂移)"},
	}
	for _, c := range checks {
		if got := strings.Count(src, c.pat); got != c.want {
			t.Errorf("出口对称性破坏: agent.go 中 %q 出现 %d 次, 期望 %d (%s)", c.pat, got, c.want, c.why)
		}
	}
	// 闭包内必须真的调用了嗅探+埋点+计数, 否则闭包本身是空壳
	body := src[strings.Index(src, "nswIntervene := func"):]
	if end := strings.Index(body, "\n\t}"); end > 0 {
		body = body[:end]
	}
	for _, must := range []string{"nswEnabled()", "nswFeedbackTextFresh(asst)", "nswRounds++", "nswAudit(a, asst, fresh, total)"} {
		if !strings.Contains(body, must) {
			t.Errorf("nswIntervene 闭包体缺 %q", must)
		}
	}
}

// TestNSWWire_ProbeHasDenominator 分母埋点字段完整性 (无分母=触发率不可算)
func TestNSWWire_ProbeHasDenominator(t *testing.T) {
	agent, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("读 agent.go 失败: %v", err)
	}
	src := string(agent)
	need := []string{`"event":   "nosword_probe"`, `"enabled": enabled`, `"anchors": anchors`, `"fresh":   fresh`, `"source":  source`, `"rounds":  rounds`}
	for _, n := range need {
		if !strings.Contains(src, n) {
			t.Errorf("nswProbeAudit 缺字段 %s -> 分母口径不完整", n)
		}
	}
}
