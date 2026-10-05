package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// ══════════════════════════════════════════════════════════════════
// memory.json.architecture「数字可复现」判据 (T1b, 20261005)
//
// 动机: 20261005 的 T1 把 architecture 里的「6源文件/13gate/4200字符」改成
// 「114 个 .go/14gate/4000字符」—— 修的是"描述过期", 但新写进去的 114 同样会
// 随下一个源文件落地而失效(换个位置复发)。能钉语义的就不该只钉字符串:
// 描述里的数字必须在载体现场可复现, 否则报红。
//
// 口径与 axiom_carrier_test.go 的 roleStaleNumbers(公理⑧「数字必须在载体可复现」)
// 同构 —— 两处都是「描述数字 vs 载体真值」。判据宁可窄也不能吵: 只认三个有明确
// 载体的模式, 不认"倍/次"这类修辞性数字。
//
// 载体单一数据源(全部现场取, 不硬编码):
//   "N 个 .go"        → 根目录 *.go 且非 *_test.go 的文件数
//   "Ngate"           → memory.json.gates 字段的 "/" 分隔条目数
//   "长度 N 字符预算"  → agent_memory.go 里 promptMapBudgetRunes 常量值
// ══════════════════════════════════════════════════════════════════

var (
	archGoCountRe = regexp.MustCompile(`(\d+)\s*个\s*\.go`)
	archGateRe    = regexp.MustCompile(`(\d+)\s*gate`)
	archBudgetRe  = regexp.MustCompile(`长度\s*(\d+)\s*字符预算`)
	budgetConstRe = regexp.MustCompile(`(?m)^const\s+promptMapBudgetRunes\s*=\s*(\d+)`)
)

// archClaimProblems 返回 architecture 描述里与载体不符的数字声明; 空 = 健康。
// 纯函数(输入=文本 + 三个载体真值, 无全局状态) —— 变异自检得以喂伪造输入。
// fail-closed: 三个模式一个都没命中 = 判据失去作用面(描述格式变了), 报红而非静默通过。
func archClaimProblems(arch string, goCount, gateCount, budget int) []string {
	var probs []string
	hit := 0
	for _, mm := range archGoCountRe.FindAllStringSubmatch(arch, -1) {
		hit++
		if n, err := strconv.Atoi(mm[1]); err != nil || n != goCount {
			probs = append(probs, fmt.Sprintf("声明「%s 个 .go」, 实际 %d", mm[1], goCount))
		}
	}
	for _, mm := range archGateRe.FindAllStringSubmatch(arch, -1) {
		hit++
		if n, err := strconv.Atoi(mm[1]); err != nil || n != gateCount {
			probs = append(probs, fmt.Sprintf("声明「%sgate」, 实际 %d 个 gate", mm[1], gateCount))
		}
	}
	for _, mm := range archBudgetRe.FindAllStringSubmatch(arch, -1) {
		hit++
		if n, err := strconv.Atoi(mm[1]); err != nil || n != budget {
			probs = append(probs, fmt.Sprintf("声明「长度 %s 字符预算」, 实际 %d", mm[1], budget))
		}
	}
	if hit == 0 {
		probs = append(probs, "architecture 未命中任何可复现数字模式 (描述格式变化? fail-closed 报红)")
	}
	return probs
}

// archCarriersFromRepo 现场取三个载体真值 (读盘, 无硬编码)。
func archCarriersFromRepo(t *testing.T) (goCount, gateCount, budget int) {
	t.Helper()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读根目录失败: %v", err)
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		goCount++
	}
	mem := readMemoryField(t, "gates")
	var gates string
	if err := json.Unmarshal(mem, &gates); err != nil {
		t.Fatalf("解析 memory.json.gates 失败: %v", err)
	}
	for _, g := range strings.Split(gates, "/") {
		if strings.TrimSpace(g) != "" {
			gateCount++
		}
	}
	src, err := os.ReadFile("agent_memory.go")
	if err != nil {
		t.Fatalf("读 agent_memory.go 失败: %v", err)
	}
	mm := budgetConstRe.FindStringSubmatch(string(src))
	if mm == nil {
		t.Fatalf("agent_memory.go 里找不到 promptMapBudgetRunes 常量定义 (fail-closed)")
	}
	budget, _ = strconv.Atoi(mm[1])
	return goCount, gateCount, budget
}

// readMemoryField 取 memory.json 某个顶层字段的原始 JSON。
func readMemoryField(t *testing.T, field string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("memory.json")
	if err != nil {
		t.Fatalf("读 memory.json 失败: %v", err)
	}
	var mem map[string]json.RawMessage
	if err := json.Unmarshal(raw, &mem); err != nil {
		t.Fatalf("解析 memory.json 失败: %v", err)
	}
	v, ok := mem[field]
	if !ok {
		t.Fatalf("memory.json 缺字段 %q (fail-closed)", field)
	}
	return v
}

func TestMemoryArchClaimsReproducible(t *testing.T) {
	goCount, gateCount, budget := archCarriersFromRepo(t)
	var arch string
	if err := json.Unmarshal(readMemoryField(t, "architecture"), &arch); err != nil {
		t.Fatalf("解析 memory.json.architecture 失败: %v", err)
	}
	probs := archClaimProblems(arch, goCount, gateCount, budget)
	for _, p := range probs {
		t.Errorf("❌ architecture 描述与载体不符: %s", p)
	}
	if len(probs) > 0 {
		t.Logf("载体真值: %d 个 .go / %d gate / %d 字符预算 —— 改描述或改载体, 二者必须一致",
			goCount, gateCount, budget)
	}
}

// TestMemoryArchClaimsMutationSelfCheck 判据自身的鉴别力: 改错必须报红, 正确必须不报。
func TestMemoryArchClaimsMutationSelfCheck(t *testing.T) {
	const good = "源码=根目录 114 个 .go(非测试) 模块化: forge*(14gate/缓存/self自改); " +
		"三判据(长度4000字符预算(promptMapBudgetRunes)/gate覆盖/变异自检)"
	cases := []struct {
		name string
		arch string
		goN  int
		gate int
		bud  int
		want bool // true = 应报红
	}{
		{"全对(正例)", good, 114, 14, 4000, false},
		{"源文件数过期", strings.Replace(good, "114 个 .go", "113 个 .go", 1), 114, 14, 4000, true},
		{"gate 数过期", strings.Replace(good, "14gate", "13gate", 1), 114, 14, 4000, true},
		{"预算过期", strings.Replace(good, "长度4000", "长度4200", 1), 114, 14, 4000, true},
		{"模式全不命中(fail-closed)", "架构描述被改写, 一个可复现数字都没有", 114, 14, 4000, true},
	}
	for _, c := range cases {
		got := len(archClaimProblems(c.arch, c.goN, c.gate, c.bud)) > 0
		if got != c.want {
			t.Errorf("变异「%s」: 报红=%v, 期望=%v", c.name, got, c.want)
		}
	}
}
