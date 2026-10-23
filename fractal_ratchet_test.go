package main

// fractal_ratchet_test.go — 阈值棘轮 (T35 P2, 20261018)
//
// 背景: 阈值 (F1 30 / F2 500 / F3 150 / F5 30) 与 fractalDebtCeiling 的
// 「只能改小」此前**只是注释纪律** —— 一次提交把 fractalMaxFileLines 改成 5000、
// 把 fractalDebtCeiling 改成 99, 全量测试照样全绿。注释里的约束等于愿望 (公理三)。
// 本文件把它变成死程序可判的事实: 每个阈值的每次收紧都留在 append-only 台账里,
// 链必须严格递减, 链尾必须等于当前值。
//
// 三套机制的边界 (定死, 防互相打架):
//   ① 阈值常量 F1/F2/F3/F5  → 管「判据松紧」 → 只降 (本文件钉住)
//   ② fractalDebtCeiling    → 管「豁免条数」 → 只降 (本文件钉住)
//   ③ fractalVitalityPins   → 管「裕度贴边」 → 只减 (元判据钉住)
// 三者正交: ① 管上限松紧, ② 管债务数量, ③ 管极值贴边。原 TODO 认为 P2 与 ①③
// 重叠, 是把棘轮误读为「取极值 × 系数自动收紧」—— 那必然制造新贴边(与③打架),
// 且自动改常量 = 判据运行时自变形。棘轮的本义是单向阀: 只许降, 不许升。
//
// 纪律 (改阈值时必须照做):
//   · 收紧(改小): 改常量 + 在链尾 append 一条 {规则, 新值, 理由+日期}, 旧记录永不删
//   · 放宽(改大): 无论是否记账都报红 —— 链不再严格递减; 真要放宽须连同本判据一起改并写明理由
//   · 新增判据维: 必须同批建账, 否则覆盖面对账报红 (漏登记 = 阈值可被无声放宽)

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// fractalRatchetStep 台账的一条记录。Note 是收紧理由 + 日期(显式决策留痕)。
type fractalRatchetStep struct {
	Rule  string
	Limit int
	Note  string
}

// fractalRatchetLedger 阈值棘轮台账 —— 受管阈值的「只降不升」印记。
// 键空间 = fractalMeasures() 的规则名 + debtCeiling, 由 ④ 双向对账钉住。
var fractalRatchetLedger = []fractalRatchetStep{
	{"F1", 30, "20261018 建账基线: 顶层声明数 (T32 P0 起的阈值)"},
	{"F2", 500, "20261018 建账基线: 文件行数 (T32 P0 起的阈值)"},
	{"F3", 150, "20261018 建账基线: 函数行数 (T32 P0 起的阈值)"},
	{"F5", 30, "20261018 建账基线: 函数圈复杂度 (T35 P1 引入, gocyclo 默认值)"},
	{"debtCeiling", 2, "20261018 建账基线: 豁免条数水位 (T61 收口后 8->2)"},
}

// fractalRatchetCurrent 当前生效阈值 —— 单一数据源: 度量表 + 水位常量, 不重定义。
// 这里刻意不复制数值: 阈值只有一处定义, 台账只记历史印记, 改常量即被对账发现。
func fractalRatchetCurrent() map[string]int {
	cur := map[string]int{"debtCeiling": fractalDebtCeiling}
	for _, m := range fractalMeasures() {
		cur[m.Rule] = m.Limit
	}
	return cur
}

// fractalRatchetChain 把台账按规则分链, 保持 append 顺序。
func fractalRatchetChain(ledger []fractalRatchetStep) map[string][]fractalRatchetStep {
	chains := map[string][]fractalRatchetStep{}
	for _, s := range ledger {
		chains[s.Rule] = append(chains[s.Rule], s)
	}
	return chains
}

// fractalRatchetProblems 判定核心(纯函数, 便于合成样本反向自检)。
// 返回问题描述列表, 空 = 通过。问题文本以规则名起头, 保证 Python 侧 VIOL_RE 能搬运。
func fractalRatchetProblems(cur map[string]int, ledger []fractalRatchetStep) []string {
	var out []string
	chains := fractalRatchetChain(ledger)
	// ① 链必须严格递减 —— 出现「不比前一条小」即棘轮倒转
	for _, rule := range sortedRatchetRules(chains) {
		chain := chains[rule]
		for i := 1; i < len(chain); i++ {
			if chain[i].Limit >= chain[i-1].Limit {
				out = append(out, fmt.Sprintf(
					"%s 棘轮倒转: 台账 %d -> %d 未严格下降 (阈值只许降不许升; 放宽须删本判据并写明理由)",
					rule, chain[i-1].Limit, chain[i].Limit))
			}
		}
	}
	// ② 链尾必须等于当前值 —— 不等即漏同步(改小没记账, 或改了台账没改常量)
	for _, rule := range sortedRatchetRules(chains) {
		chain := chains[rule]
		last := chain[len(chain)-1].Limit
		v, ok := cur[rule]
		if !ok {
			out = append(out, fmt.Sprintf(
				"%s 台账僵尸: 台账有该规则但当前无对应阈值 (判据维已删? 须同步删台账)", rule))
			continue
		}
		if v != last {
			out = append(out, fmt.Sprintf(
				"%s 棘轮漏同步: 当前阈值 %d != 台账链尾 %d (改小须 append 记账, 改大即倒转)",
				rule, v, last))
		}
	}
	// ③ 双向覆盖: 当前有而台账无 = 新判据维未建账(阈值可被无声放宽的盲区)
	for _, rule := range sortedCurrentRules(cur) {
		if _, ok := chains[rule]; !ok {
			out = append(out, fmt.Sprintf(
				"%s 棘轮未建账: 当前阈值 %d 无台账记录 (新增判据维必须同批建账)", rule, cur[rule]))
		}
	}
	sort.Strings(out)
	return out
}

// sortedRatchetRules 键排序 —— 让问题列表与 map 迭代顺序无关(逐字节可复现)。
func sortedRatchetRules(chains map[string][]fractalRatchetStep) []string {
	out := make([]string, 0, len(chains))
	for k := range chains {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedCurrentRules 同上, 对当前阈值表排序。
func sortedCurrentRules(cur map[string]int) []string {
	out := make([]string, 0, len(cur))
	for k := range cur {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestFractalRatchet 判据本体: 真实台账与真实阈值对账。
func TestFractalRatchet(t *testing.T) {
	cur := fractalRatchetCurrent()
	// 判据自身的判据: 收集口径必须真的扫到东西 —— 扫到 0 条时对账无从谈起,
	// 放行则「口径坏掉」与「阈值健康」无法区分。
	if len(cur) < 5 {
		t.Fatalf("当前阈值只收集到 %d 条 (< 5) —— 度量表/水位口径失效, 不可判绿", len(cur))
	}
	for _, p := range fractalRatchetProblems(cur, fractalRatchetLedger) {
		t.Error(p)
	}
	chains := fractalRatchetChain(fractalRatchetLedger)
	for _, rule := range sortedCurrentRules(cur) {
		t.Logf("阈值台账 %s: 当前 %d, 台账 %d 条, 链尾 %d", rule, cur[rule],
			len(chains[rule]), chains[rule][len(chains[rule])-1].Limit)
	}
}

// TestFractalRatchetSelfCheck 判据的判据: 合成样本反向验证三条判定臂都活着。
func TestFractalRatchetSelfCheck(t *testing.T) {
	base := map[string]int{"F1": 30, "debtCeiling": 2}
	ok := []fractalRatchetStep{{"F1", 30, "基线"}, {"debtCeiling", 2, "基线"}}
	cases := []struct {
		name   string
		cur    map[string]int
		ledger []fractalRatchetStep
		want   string // 期望命中的关键词; "" = 必须无问题
	}{
		{"健康: 链尾==当前值", base, ok, ""},
		{"健康: 收紧后链尾跟随", map[string]int{"F1": 20, "debtCeiling": 2},
			[]fractalRatchetStep{{"F1", 30, "基线"}, {"F1", 20, "收紧"}, {"debtCeiling", 2, "基线"}}, ""},
		{"倒转: 链出现上升", base,
			[]fractalRatchetStep{{"F1", 20, "收紧"}, {"F1", 30, "放宽"}}, "棘轮倒转"},
		{"倒转: 链出现持平", base,
			[]fractalRatchetStep{{"F1", 30, "基线"}, {"F1", 30, "重记"}}, "棘轮倒转"},
		{"漏同步: 当前值大于链尾(改大没记账)", map[string]int{"F1": 99, "debtCeiling": 2}, ok, "漏同步"},
		{"漏同步: 当前值小于链尾(收紧没记账)", map[string]int{"F1": 20, "debtCeiling": 2}, ok, "漏同步"},
		{"未建账: 新判据维无台账", map[string]int{"F1": 30, "debtCeiling": 2, "F9": 7}, ok, "未建账"},
		{"僵尸: 台账有而当前无", map[string]int{"debtCeiling": 2}, ok, "台账僵尸"},
	}
	for _, c := range cases {
		got := fractalRatchetProblems(c.cur, c.ledger)
		if c.want == "" {
			if len(got) != 0 {
				t.Errorf("%s: 不该报红, got %v", c.name, got)
			}
			continue
		}
		if len(got) == 0 || !containsSub(got, c.want) {
			t.Errorf("%s: 应命中 %q, got %v", c.name, c.want, got)
		}
	}
}

// containsSub 判断问题列表里是否有条目包含关键词。
func containsSub(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
