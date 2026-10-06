package main

// ============================================================================
// judgement_ledger_test.go —— 判据台账哨兵 (J5, 20261005)
//
// 动机 (公理三: 无判据 = 愿望): judgement_ledger.py 能列出判据候选, 但
//   「新增判据有没有人登记维度」这件事本身没有判据 —— 台账会静静过期,
//   而「改动面 -> 该跑哪些判据」的映射随之失真。
//
// 本文件把三件事钉成死程序:
//   1. 真实台账自洽: 无未裁决 / 无僵尸裁决 / 维度合法 / dynamic 与维度同源 /
//      逻辑型判据载体存在 / 双向覆盖 (semantics <-> entries) / 水位不降
//   2. 新鲜度: 重新扫描源码与磁盘台账逐字段比对 (过期即报红) —— 过期检测
//      单独用沙箱「删一条 entry」验证鉴别力 (判据自身也要有判据)
//   3. 鉴别力: 沙箱喂 9 种变异, 判据必须报红
// ============================================================================

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	judgeLedgerFile    = "defense_system/judgement_ledger.json"
	judgeSemanticsFile = "defense_system/judgement_semantics.json"
	judgeLedgerPy      = "defense_system/judgement_ledger.py"
	// 水位 (棘轮, 只升不降): 删条目规避 = 报红
	judgeEntriesFloor     = 129
	judgeTestAssertsFloor = 100
)

// judgeDims 合法维度 (以恶对手): runtime=非判据。unclassified 不在列 = 必报红。
var judgeDims = map[string]string{
	"logic":       "forbidden",
	"adversarial": "forbidden",
	"structural":  "ratchet",
	"drift":       "bounded",
	"runtime":     "n/a",
}

type judgeEntry struct {
	ID          string  `json:"id"`
	Dimension   string  `json:"dimension"`
	DimSource   string  `json:"dim_source"`
	Dynamic     string  `json:"dynamic"`
	IsJudgement string  `json:"is_judgement"`
	Value       float64 `json:"value"`
}

type judgeLogic struct {
	ID            string `json:"id"`
	Carrier       string `json:"carrier"`
	CarrierExists bool   `json:"carrier_exists"`
}

type judgeLedger struct {
	Version         int               `json:"version"`
	DynamicByDim    map[string]string `json:"dynamic_by_dim"`
	SemanticsSource string            `json:"semantics_source"`
	Entries         []judgeEntry      `json:"entries"`
	Unregistered    []string          `json:"unregistered"`
	OrphanOverrides []string          `json:"orphan_overrides"`
	TestAssertions  []judgeEntry      `json:"test_assertions"`
	LogicJudgements []judgeLogic      `json:"logic_judgements"`
}

func loadJudgeLedger(t *testing.T, path string) *judgeLedger {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读台账失败: %v", err)
	}
	var led judgeLedger
	if err := json.Unmarshal(raw, &led); err != nil {
		t.Fatalf("解析台账失败: %v", err)
	}
	return &led
}

// loadSemanticsIDs 读人工裁决层的 key 集合 (台账的双向覆盖基准)。
func loadSemanticsIDs(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读语义层失败: %v", err)
	}
	var doc struct {
		Overrides map[string]json.RawMessage `json:"overrides"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析语义层失败: %v", err)
	}
	out := map[string]bool{}
	for k := range doc.Overrides {
		out[k] = true
	}
	return out
}

// validateJudgeLedger 纯函数判据: 返回问题清单 (空 = 全绿)。
// 抽成纯函数是为了让「判据自身」能在沙箱里被变异验证。
func validateJudgeLedger(led *judgeLedger, semIDs map[string]bool) []string {
	var p []string
	if led.Version < 2 {
		p = append(p, fmt.Sprintf("version=%d < 2", led.Version))
	}
	if n := len(led.Unregistered); n != 0 {
		p = append(p, fmt.Sprintf("未裁决 %d 条: %v", n, firstN(led.Unregistered, 5)))
	}
	if n := len(led.OrphanOverrides); n != 0 {
		p = append(p, fmt.Sprintf("僵尸裁决 %d 条: %v", n, firstN(led.OrphanOverrides, 5)))
	}
	if n := len(led.Entries); n < judgeEntriesFloor {
		p = append(p, fmt.Sprintf("条目水位下降: %d < %d", n, judgeEntriesFloor))
	}
	if n := len(led.TestAssertions); n < judgeTestAssertsFloor {
		p = append(p, fmt.Sprintf("测试断言水位下降: %d < %d", n, judgeTestAssertsFloor))
	}
	for _, e := range led.Entries {
		want, ok := judgeDims[e.Dimension]
		if !ok {
			p = append(p, "维度非法: "+e.ID+"="+e.Dimension)
			continue
		}
		if e.Dynamic != want {
			p = append(p, fmt.Sprintf("dynamic 与维度不符: %s dim=%s dynamic=%s 应为 %s",
				e.ID, e.Dimension, e.Dynamic, want))
		}
		if e.DimSource == "" {
			p = append(p, "dim_source 为空(未标来源): "+e.ID)
		}
	}
	ids := map[string]bool{}
	for _, e := range led.Entries {
		ids[e.ID] = true
	}
	for id := range semIDs {
		if !ids[id] {
			p = append(p, "僵尸裁决(台账无此条目): "+id)
		}
	}
	for id := range ids {
		if !semIDs[id] {
			p = append(p, "未登记裁决(台账有条目但语义层没有): "+id)
		}
	}
	for _, j := range led.LogicJudgements {
		if !j.CarrierExists {
			p = append(p, "逻辑判据载体缺失: "+j.ID+" -> "+j.Carrier)
		}
	}
	return p
}

func firstN(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}

func cloneJudgeLedger(src *judgeLedger) *judgeLedger {
	b, _ := json.Marshal(src)
	var out judgeLedger
	json.Unmarshal(b, &out)
	return &out
}

// baselineJudgeLedger 构造一份「刚好过线」的合法台账 (供变异对比)。
func baselineJudgeLedger() (*judgeLedger, map[string]bool) {
	ents := make([]judgeEntry, judgeEntriesFloor)
	sem := map[string]bool{}
	for i := range ents {
		id := fmt.Sprintf("e%03d", i)
		ents[i] = judgeEntry{ID: id, Dimension: "drift", DimSource: "manual",
			Dynamic: "bounded", IsJudgement: "yes"}
		sem[id] = true
	}
	led := &judgeLedger{
		Version: 2, SemanticsSource: judgeSemanticsFile,
		DynamicByDim:   map[string]string{"logic": "forbidden", "adversarial": "forbidden", "structural": "ratchet", "drift": "bounded", "runtime": "n/a"},
		Entries:        ents,
		TestAssertions: make([]judgeEntry, judgeTestAssertsFloor),
	}
	return led, sem
}

// TestJudgementLedgerRealWorkspace 真实台账必须自洽 (未裁决=0 / 双向覆盖 / 水位)。
func TestJudgementLedgerRealWorkspace(t *testing.T) {
	led := loadJudgeLedger(t, judgeLedgerFile)
	sem := loadSemanticsIDs(t, judgeSemanticsFile)
	if problems := validateJudgeLedger(led, sem); len(problems) != 0 {
		t.Fatalf("台账不自洽 (%d 项):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
}

// TestJudgementLedgerFresh 新鲜度: 重新扫描源码, 磁盘台账必须跟上。
func TestJudgementLedgerFresh(t *testing.T) {
	cmd := exec.Command(guardGatePython(), judgeLedgerPy, "--check")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("台账已过期(新增判据未登记/字段漂移): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "台账新鲜") {
		t.Fatalf("--check 输出异常: %s", out)
	}
}

// TestJudgementLedgerStaleCaught 鉴别力: 沙箱造过期台账 (删一条 entry), --check 必须报红。
func TestJudgementLedgerStaleCaught(t *testing.T) {
	raw, err := os.ReadFile(judgeLedgerFile)
	if err != nil {
		t.Fatalf("读台账失败: %v", err)
	}
	var led map[string]interface{}
	if err := json.Unmarshal(raw, &led); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	ents, ok := led["entries"].([]interface{})
	if !ok || len(ents) < 2 {
		t.Fatalf("entries 结构异常")
	}
	led["entries"] = ents[:len(ents)-1] // 人为制造「现扫多一条」
	b, _ := json.Marshal(led)
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.json")
	if err := os.WriteFile(stale, b, 0o644); err != nil {
		t.Fatalf("写沙箱台账失败: %v", err)
	}
	cmd := exec.Command(guardGatePython(), judgeLedgerPy, "--check")
	cmd.Env = append(pythonUTF8Env(), "FORGE_JUDGEMENT_LEDGER="+stale)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("过期台账未报红 (判据无鉴别力): %s", out)
	}
	if !strings.Contains(string(out), "新增未入台账") {
		t.Fatalf("报红原因不是「未入台账」: %s", out)
	}
}

// TestJudgementLedgerMutationSelfCheck 9 种变异, 判据必须逐一报红。
func TestJudgementLedgerMutationSelfCheck(t *testing.T) {
	base, sem0 := baselineJudgeLedger()
	if p := validateJudgeLedger(cloneJudgeLedger(base), copyBoolMap(sem0)); len(p) != 0 {
		t.Fatalf("基线应全绿, 实际报红: %v", p)
	}
	cases := []struct {
		name string
		mut  func(*judgeLedger, map[string]bool)
	}{
		{"未裁决非空", func(l *judgeLedger, s map[string]bool) { l.Unregistered = []string{"x"} }},
		{"僵尸裁决", func(l *judgeLedger, s map[string]bool) { l.OrphanOverrides = []string{"y"} }},
		{"维度非法", func(l *judgeLedger, s map[string]bool) { l.Entries[0].Dimension = "unclassified" }},
		{"dynamic 与维度不符", func(l *judgeLedger, s map[string]bool) { l.Entries[0].Dynamic = "ratchet" }},
		{"dim_source 为空", func(l *judgeLedger, s map[string]bool) { l.Entries[0].DimSource = "" }},
		{"条目水位下降", func(l *judgeLedger, s map[string]bool) { l.Entries = l.Entries[:5] }},
		{"测试断言水位下降", func(l *judgeLedger, s map[string]bool) { l.TestAssertions = nil }},
		{"语义层缺登记", func(l *judgeLedger, s map[string]bool) { delete(s, "e000") }},
		{"语义层多僵尸", func(l *judgeLedger, s map[string]bool) { s["zzz"] = true }},
		{"逻辑判据载体缺失", func(l *judgeLedger, s map[string]bool) {
			l.LogicJudgements = []judgeLogic{{ID: "lj", Carrier: "none.go", CarrierExists: false}}
		}},
		{"version 过低", func(l *judgeLedger, s map[string]bool) { l.Version = 1 }},
	}
	for _, c := range cases {
		led := cloneJudgeLedger(base)
		s := copyBoolMap(sem0)
		c.mut(led, s)
		if p := validateJudgeLedger(led, s); len(p) == 0 {
			t.Fatalf("变异 %q 未被捕获 —— 判据无鉴别力", c.name)
		}
	}
}

func copyBoolMap(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
