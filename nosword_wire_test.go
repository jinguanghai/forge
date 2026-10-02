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
	"strings"
	"testing"
)

// TestNSWWire_CallSitesExist 主干接线点存在性。
//
// 扫描范围是**整个包**而非 agent.go (B3 批3): 接线是包级属性, 硬编码单个文件
// 会在代码重组时误报 —— 功能还在, 只是换了文件。B3 批3 把收尾逻辑抽到
// agent_stream_final.go 后, 虚报检测三项即搬离了 agent.go。
//
// 判据为 AST 口径(见 prodSymbolRefs): 文本版 Contains(全包, "nswAudit(") 会被
// 函数定义行 func nswAudit( 本身满足 —— 调用点删光仍 PASS(变异测试实证)。
func TestNSWWire_CallSitesExist(t *testing.T) {
	refs := prodSymbolRefs(t)
	cases := []struct {
		name string
		kind wireKind
		why  string
	}{
		{"nswEnabled", wireCall, "无剑开关未接线 -> FORGE_NOSWORD=1 不生效"},
		{"nswFeedbackTextFresh", wireCall, "无剑复述过滤未接线 -> 自激循环不可控(引用算式被反复反馈)"},
		{"nswAudit", wireCall, "无剑埋点未接线 -> 触发率/空转率不可测, 扩语法只能盲扩"},
		{"maxNSWRounds", wireSelector, "无剑轮次上限缺失 -> 可能死循环"},
		{"verifyClaimEnabled", wireCall, "虚报检测开关未接线"},
		{"detectUnverifiedClaim", wireCall, "虚报检测判据未接线"},
		{"maxVerifyStrikes", wireRef, "虚报强干预上限缺失 -> 可能死循环"},
	}
	for _, c := range cases {
		if !refs.wired(c.kind, c.name) {
			t.Errorf("接线缺失: 生产代码中未见 %s 的%s (%s)", c.name, c.kind, c.why)
		}
	}
}

// nswOrphanAllowlist 显式豁免的"生产零引用但有意保留"的 nosword.go 顶层函数。
//
// 每项必须写明原因; 哨兵会反向检查豁免是否过期 —— 一旦该函数被真正接线,
// 即报"豁免已过期"要求移出。豁免表不得成为垃圾场。
var nswOrphanAllowlist = map[string]string{
	// 一期兼容薄封装(转调 nswIsCandidateMode), 一期路径已下线, 仅测试调用。
	// 由 TestNSWExprExplicit_Wired 钉住不得删除, 故显式豁免而非当作孤儿。
	"nswIsCandidate": "一期兼容入口, 仅测试调用; 不得删除(见 TestNSWExprExplicit_Wired)",
}

// TestNSWWire_NoOrphanFunctions nosword.go 包级函数零引用检测 (孤儿代码防线)
//
// 判据为 AST 口径: 原文本版把"注释里提到函数名"也算作引用, 于是给一个零调用
// 函数加一行注释即可骗过它(变异测试实证)。AST 口径天然排除注释与字符串。
func TestNSWWire_NoOrphanFunctions(t *testing.T) {
	refs := prodSymbolRefs(t)
	assertNoOrphanFuncs(t, refs, "nosword.go", nswOrphanAllowlist)
}

// TestNSWWire_BothExitSitesSymmetric 收尾出口对称性 (20260912 缺陷O 的持续防线)
//
// 教训: RunStream 的最终收尾曾有两个出口 —— ①无工具调用的纯文本回复 ②工具调用
// 碎片全无效降级为纯文本。功能只接一处 = 另一条路径上的算式错值静默漏过, 且单测
// (直接调 nosword.go 函数)永远发现不了。
//
// B3 批3 起两个出口合并为单一入口 turnFinalizer.finalize(agent_stream_final.go),
// 对称性由"共用入口"从结构上保证, 不再依赖两处拷贝手工同步。
// B3 批5 起干预入口由 RunStream 内闭包升为 runState 方法 (agent_stream_setup.go),
// 主循环迁至 runTurns —— 实现形态变了, 但下面这条不变式不变:
//
//   - 无剑干预定义 / 干预调用 / 反馈注入 / 分母埋点在全包内各恰好 1 处 (唯一入口,
//     不存在漏接的第二处; 出现 0 = 静默失效, >1 = 又分叉了);
//   - 两处收尾出口都必须调用 fin.finalize (有人改回内联收尾即红);
//   - agent.go 不得再出现内联的 nswProbeAudit 调用。
func TestNSWWire_BothExitSitesSymmetric(t *testing.T) {
	all := prodGoSources(t)
	agentRaw, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("读 agent.go 失败: %v", err)
	}
	agent := string(agentRaw)

	uniq := []struct {
		pat  string
		want int
		why  string
	}{
		{"nswIntervene(asst)", 1, "无剑干预调用点必须唯一 (0=该路径无剑静默失效, >1=又分叉了)"},
		{`ChatMessage{Role: "user", Content: fb}`, 1, "无剑反馈注入点必须唯一 (缺失=拦住了却不反馈=白拦)"},
		{"func (rs *runState) nswIntervene(asst string) string", 1, "无剑干预必须只有一份实现, 禁止多份拷贝(行为会漂移)"},
	}
	for _, c := range uniq {
		if got := strings.Count(all, c.pat); got != c.want {
			t.Errorf("出口对称性破坏: 全包中 %q 出现 %d 次, 期望 %d (%s)", c.pat, got, c.want, c.why)
		}
	}
	// 分母埋点: 排除函数定义行本身。
	if got := strings.Count(all, "nswProbeAudit(") - strings.Count(all, "func nswProbeAudit("); got != 1 {
		t.Errorf("无剑分母埋点应恰好 1 处调用, 实际 %d (缺失=触发率分母有洞)", got)
	}
	// 两处收尾出口都必须走统一入口 (批5 起主循环在 agent_stream_setup.go, 故扫全包)。
	if got := strings.Count(all, "fin.finalize("); got != 2 {
		t.Errorf("两处收尾出口都应调用 fin.finalize, 实际 %d 处 (改回内联收尾即漏拦)", got)
	}
	if strings.Contains(agent, "nswProbeAudit(a, asst,") {
		t.Error("agent.go 出现内联 nswProbeAudit 调用 —— 收尾逻辑被改回重复拷贝")
	}

	// 干预入口的方法体必须真的调用了嗅探+埋点+计数, 否则入口本身是空壳。
	idx := strings.Index(all, "func (rs *runState) nswIntervene")
	if idx < 0 {
		t.Fatal("未找到 nswIntervene 方法定义 (agent_stream_setup.go)")
	}
	body := all[idx:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	for _, must := range []string{
		"nswExprEnabled()", "nswEnabled()", "nswFeedbackTextFresh(asst)",
		"rs.nswRounds++", "nswAudit(rs.agent, asst, fresh, total)",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("nswIntervene 方法体缺 %q", must)
		}
	}
}

// TestNSWWire_ProbeHasDenominator 分母埋点字段完整性 (无分母=触发率不可算)
func TestNSWWire_ProbeHasDenominator(t *testing.T) {
	src := prodGoSources(t) // 包级扫描: 埋点函数若换文件不应误报
	need := []string{`"event":   "nosword_probe"`, `"enabled": enabled`, `"anchors": anchors`, `"fresh":   fresh`, `"source":  source`, `"rounds":  rounds`}
	for _, n := range need {
		if !strings.Contains(src, n) {
			t.Errorf("nswProbeAudit 缺字段 %s -> 分母口径不完整", n)
		}
	}
}
