package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// 构造一份最小完整主记忆 (含锚点字段, 通过 memHealthLint 软检查)
func testMainMemory() []byte {
	m := map[string]interface{}{
		"identity":        "test",
		"role":            "test",
		"language":        "zh",
		"working_dir":     "wd",
		"gates":           "python math logic regex",
		"axioms":          "公理一: 注意力稀缺",
		"self_governance": map[string]interface{}{},
	}
	data, _ := json.Marshal(m)
	return data
}

// TestAnchorGuardSecondWriteBlocked: 同一天第 2 次锚点写入被护栏拦截
func TestAnchorGuardSecondWriteBlocked(t *testing.T) {
	wd := t.TempDir()
	if err := SaveMemory(wd, testMainMemory()); err != nil {
		t.Fatalf("首次锚点写入应成功: %v", err)
	}
	// 第二次锚点写入 (改 axioms 值)
	m := map[string]interface{}{}
	_ = json.Unmarshal(testMainMemory(), &m)
	m["axioms"] = "公理一: 注意力稀缺(改)"
	data, _ := json.Marshal(m)
	err := SaveMemory(wd, data)
	if err == nil {
		t.Fatal("同日第 2 次锚点写入应被护栏拦截")
	}
	if !strings.Contains(err.Error(), "锚点写入护栏") {
		t.Fatalf("错误信息应为护栏提示, got: %v", err)
	}
}

// TestAnchorGuardFragmentNotBlocked: 纯片段写入 (key_findings) 多次不被拦
func TestAnchorGuardFragmentNotBlocked(t *testing.T) {
	wd := t.TempDir()
	for i := 0; i < 3; i++ {
		frag := map[string]interface{}{
			"key_findings": []interface{}{
				map[string]interface{}{"title": "片段", "content": "会话经验"},
			},
		}
		data, _ := json.Marshal(frag)
		if err := SaveMemory(wd, data); err != nil {
			t.Fatalf("片段写入第 %d 次不应被拦: %v", i+1, err)
		}
	}
}

// TestMemHealthLintRejectsGarbage: 乱码数据被 lint 拒绝
func TestMemHealthLintRejectsGarbage(t *testing.T) {
	// 无效 UTF-8 (GBK 乱码场景)
	garbage := []byte{0xff, 0xfe, 0x41}
	if err := memHealthLint(garbage); err == nil {
		t.Fatal("乱码数据应被 lint 拒绝")
	}
	if err := memHealthLint([]byte("not json")); err == nil {
		t.Fatal("非法 JSON 应被 lint 拒绝")
	}
}

// TestMemHealthLintAllowsFragment: 会话片段通过 lint
func TestMemHealthLintAllowsFragment(t *testing.T) {
	frag := []byte(`{"key_findings":[{"title":"x","content":"y"}]}`)
	if err := memHealthLint(frag); err != nil {
		t.Fatalf("会话片段应通过 lint: %v", err)
	}
}

// TestAnchorAuditWritesFile: 锚点写入后 anchor_audit.jsonl 生成
func TestAnchorAuditWritesFile(t *testing.T) {
	wd := t.TempDir()
	if err := SaveMemory(wd, testMainMemory()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(anchorAuditPath(wd)); err != nil {
		t.Fatalf("anchor_audit.jsonl 应生成: %v", err)
	}
	sum := anchorAuditSummary(wd)
	if !strings.Contains(sum, "锚点改动总数") {
		t.Fatalf("摘要应含总数: %s", sum)
	}
}

// TestAnchorAuditRecordsWhenGuardOff: 关闭护栏仍必须留痕, 且不占当日配额。
// 回归 20260925: anchorGuardAudit 首行即 return → 关护栏=审计静默(失效不留痕)。
func TestAnchorAuditRecordsWhenGuardOff(t *testing.T) {
	wd := t.TempDir()
	t.Setenv("FORGE_ANCHOR_GUARD", "0")
	if err := SaveMemory(wd, testMainMemory()); err != nil {
		t.Fatalf("关护栏写入应成功: %v", err)
	}
	data, err := os.ReadFile(anchorAuditPath(wd))
	if err != nil {
		t.Fatalf("关护栏写入必须留痕(原缺陷=审计静默): %v", err)
	}
	if !strings.Contains(string(data), `"guard_off":true`) {
		t.Fatalf("审计条目应带 guard_off 标记: %s", string(data))
	}
	if c, _ := anchorAuditTodayCount(wd); c != 0 {
		t.Fatalf("关护栏写入不应占配额, count=%d", c)
	}

	// 关护栏写入不占配额 → 开护栏后第 1 次写入仍应放行 (配额语义不变)
	t.Setenv("FORGE_ANCHOR_GUARD", "1")
	m := map[string]interface{}{}
	_ = json.Unmarshal(testMainMemory(), &m)
	m["axioms"] = "公理一: 注意力稀缺(改)"
	nd, _ := json.Marshal(m)
	if err := SaveMemory(wd, nd); err != nil {
		t.Fatalf("关护栏写入不应占用配额, 开护栏后首次写入应放行: %v", err)
	}
	// 第 2 次仍必须被拦 (护栏主功能未被削弱)
	if err := SaveMemory(wd, nd); err == nil {
		t.Fatal("开护栏后同日第 2 次锚点写入应被拦")
	}
}

// TestAnchorAuditCountSkipsExemptOnly: 配额计数只豁免 exempt (事后认领) 与 guard_off,
// 其余条目一律计数 —— 反例钉住豁免不得扩散 (豁免分支单独打标, 宽口子=静默放行)。
func TestAnchorAuditCountSkipsExemptOnly(t *testing.T) {
	wd := t.TempDir()
	today := time.Now().Format(time.RFC3339)
	lines := []string{
		`{"time":"` + today + `","file":"memory.json","fields":["identity"],"src":"exempt"}`,
		`{"time":"` + today + `","file":"memory.json","fields":["identity"],"guard_off":true}`,
		`{"time":"` + today + `","file":"memory.json","fields":["lessons"],"src":"save"}`,
		`{"time":"` + today + `","file":"memory.json","fields":["architecture"]}`,
	}
	if err := os.WriteFile(anchorAuditPath(wd), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if c, _ := anchorAuditTodayCount(wd); c != 2 {
		t.Fatalf("只应计 2 条 (exempt 与 guard_off 各豁免一条, 另两条必须计数), count=%d", c)
	}
}

// TestAnchorAuditTodayStatsCountsExempt: 豁免必须计入 total, 只从 counted 剔除。
//
// 回归 20261003: 当日 14 次锚点写入全走豁免(手工路径 / 关护栏), 而 anchorAuditTodayCount
// 返回 0 —— 拦截与度量共用一个计数, 于是绕行不可见 (测量失真比没有测量更危险: 会让人
// 误以为"今天没怎么改")。豁免不计配额是设计, 但必须可计量。
func TestAnchorAuditTodayStatsCountsExempt(t *testing.T) {
	wd := t.TempDir()
	today := time.Now().Format(time.RFC3339)
	lines := []string{
		`{"time":"` + today + `","file":"memory.json","fields":["axioms"]}`,                   // 计配额
		`{"time":"` + today + `","file":"memory.json","fields":["lessons"],"guard_off":true}`, // 豁免: 关护栏
		`{"time":"` + today + `","file":"memory.json","fields":["defense"],"src":"exempt"}`,   // 豁免: 事后认领
		`{"time":"` + today + `","file":"memory.json","fields":["gates"]}`,                    // 计配额
	}
	if err := os.WriteFile(anchorAuditPath(wd), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	total, counted, err := anchorAuditTodayStats(wd)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || counted != 2 {
		t.Fatalf("应 total=4/counted=2 (豁免 2 条计入 total), got total=%d/counted=%d", total, counted)
	}
	// 拦截计数必须与 stats.counted 同源 (否则度量与拦截各算一份, 迟早漂移)
	if c, _ := anchorAuditTodayCount(wd); c != counted {
		t.Fatalf("anchorAuditTodayCount 与 stats.counted 漂移: %d vs %d", c, counted)
	}
	sum := anchorAuditSummary(wd)
	if !strings.Contains(sum, "今日: 锚点改动 4 次 (计配额 2 / 豁免 2)") {
		t.Fatalf("摘要应显示今日实际改动量与豁免数, got:\n%s", sum)
	}
}

// TestAnchorAuditSummaryWarnsOnExemptFlood: 豁免达阈值必须告警 (绕行可见才算闭环)。
// 阈值自身也要有判据: 阈值下方必须不告警, 否则告警常驻 = 告警贬值。
func TestAnchorAuditSummaryWarnsOnExemptFlood(t *testing.T) {
	build := func(n int) string {
		wd := t.TempDir()
		today := time.Now().Format(time.RFC3339)
		var lines []string
		for i := 0; i < n; i++ {
			lines = append(lines, `{"time":"`+today+`","file":"memory.json","fields":["lessons"],"guard_off":true}`)
		}
		if err := os.WriteFile(anchorAuditPath(wd), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		return anchorAuditSummary(wd)
	}
	if s := build(anchorExemptWarnThreshold - 1); strings.Contains(s, "⚠") {
		t.Fatalf("豁免 %d 次 (阈值 %d 下方) 不该告警:\n%s", anchorExemptWarnThreshold-1, anchorExemptWarnThreshold, s)
	}
	if s := build(anchorExemptWarnThreshold); !strings.Contains(s, "⚠") {
		t.Fatalf("豁免 %d 次 (达阈值) 必须告警:\n%s", anchorExemptWarnThreshold, s)
	}
}
