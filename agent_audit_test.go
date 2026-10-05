// agent_audit_test.go — 审计落盘契约。
//
// appendAuditLine 此前零直接用例 (仅被哨兵间接引用)。它的两个契约是
// "关闸期必须彻底不写"与"每次调用追加一行合法 JSON" —— 前者是留痕纪律的边界,
// 后者是 gate_audit.jsonl 可解析的前提 (半行 JSON 会让统计脚本静默丢数据)。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAppendAuditLine_GuardOffWritesNothing(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "0")
	dir := t.TempDir()
	appendAuditLine(&AgentRunner{cfg: &Config{WorkDir: dir}}, map[string]interface{}{"event": "t"})
	if _, err := os.Stat(auditFilePath(dir)); !os.IsNotExist(err) {
		t.Errorf("FORGE_GATE_AUDIT=0 时不应创建审计文件 (err=%v)", err)
	}
}

func TestAppendAuditLine_WritesParseableJSONL(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}
	appendAuditLine(a, map[string]interface{}{"event": "probe", "n": 1})
	appendAuditLine(a, map[string]interface{}{"event": "probe", "n": 2})

	data, err := os.ReadFile(auditFilePath(dir))
	if err != nil {
		t.Fatalf("审计文件未写出: %v", err)
	}
	body := strings.TrimRight(string(data), "\n")
	lines := strings.Split(body, "\n")
	if len(lines) != 2 {
		t.Fatalf("追加语义: 期望 2 行, 得到 %d 行 (%q)", len(lines), body)
	}
	for i, ln := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Errorf("第 %d 行不是合法 JSON: %v (%q)", i, err, ln)
		}
		if m["event"] != "probe" {
			t.Errorf("第 %d 行 event 字段丢失: %v", i, m)
		}
	}
}

func TestAppendAuditLine_NilAgentUsesAuditPathEnv(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.jsonl")
	t.Setenv("FORGE_AUDIT_PATH", audit)

	if got := auditFilePath(""); got != audit {
		t.Errorf("空 dir 应回退 FORGE_AUDIT_PATH: got %q, want %q", got, audit)
	}
	// nil agent 不得 panic (审计是 best-effort, 不能反过来阻断主流程)
	appendAuditLine(nil, map[string]interface{}{"event": "nil-agent"})
	data, err := os.ReadFile(audit)
	if err != nil {
		t.Fatalf("nil agent 应仍能落盘: %v", err)
	}
	if !strings.Contains(string(data), "nil-agent") {
		t.Errorf("事件未写出: %s", data)
	}
}

func TestNswAudit_NoFreshWritesNothing(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}
	nswAudit(a, "正文", nil, 3)
	if _, err := os.Stat(auditFilePath(dir)); !os.IsNotExist(err) {
		t.Errorf("无 fresh 锚点时应零写入 (避免刷空转日志), err=%v", err)
	}
}

// TestStripAudit_RecordsAndCounts 剥离埋点的三个契约:
//  1. 返回值 = 被剥离的 rune 数 —— 调用方据此打回执, "判"与"显"必须同源;
//  2. 落盘一条 event=strip 且 audit_schema.go 声明的必填字段全在;
//  3. 空/纯空白文本零写入零返回 —— 否则每个工具轮都记一条, 触发率分母被稀释成噪音。
//
// 动机 (20261004): 工具轮文本剥离此前零留痕, "剥了几次/剥掉多少/剥的是什么"
// 全不可测, 于是这条纪律该不该留在 prompt 里只能靠感觉(违反度量铁律)。
func TestStripAudit_RecordsAndCounts(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}

	text := "  分析: 先看看文件再决定  "
	want := utf8.RuneCountInString(strings.TrimSpace(text))
	if got := stripAudit(a, text, 2, 3); got != want {
		t.Errorf("返回值 = %d, 期望 %d (回执与埋点必须同源)", got, want)
	}
	if got := stripAudit(a, "  \n\t ", 1, 1); got != 0 {
		t.Errorf("纯空白应返回 0, 得到 %d", got)
	}

	data, err := os.ReadFile(auditFilePath(dir))
	if err != nil {
		t.Fatalf("未落盘: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("空白轮不应落盘: 期望 1 行, 得到 %d 行 (%q)", len(lines), data)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("非合法 JSON: %v (%q)", err, lines[0])
	}
	if m["event"] != "strip" {
		t.Errorf("event 字段错误: %v", m["event"])
	}
	if got := int(m["chars"].(float64)); got != want {
		t.Errorf("chars = %d, 期望 %d", got, want)
	}
	if int(m["tools"].(float64)) != 2 || int(m["turn"].(float64)) != 3 {
		t.Errorf("tools/turn 记录错误: %v", m)
	}
	if head, _ := m["head"].(string); !strings.Contains(head, "分析") {
		t.Errorf("head 摘要未记录: %v", m["head"])
	}
	if _, ok := m["ts"].(string); !ok {
		t.Errorf("埋点缺 ts: %v", m)
	}
	// 注册表声明的必填字段与实际写入必须一致 (注册了不写 = 契约腐化)。
	for f := range auditEventRequired("strip") {
		if _, ok := m[f]; !ok {
			t.Errorf("strip 埋点缺 audit_schema.go 声明的必填字段 %q", f)
		}
	}
}

// TestStripPoint_UniqueCallSite 剥离点唯一性: 工具轮文本剥离只能有一处。
//
// 教训同 TestNSWWire_BothExitSitesSymmetric: 收尾/剥离这类动作一旦分叉成两份
// 拷贝, 后续只会改其中一处 —— 另一条路径静默失守且单测发现不了 (20260912 缺陷O)。
func TestStripPoint_UniqueCallSite(t *testing.T) {
	all := prodGoSources(t)
	if got := strings.Count(all, "stripAudit(") - strings.Count(all, "func stripAudit("); got != 1 {
		t.Errorf("剥离埋点应恰好 1 处调用, 实际 %d (0=静默失效, >1=又分叉了)", got)
	}
}

// TestMetricClaimAudit_Vectors 度量铁律埋点的双向向量表。
//
// 正例 = 含"统计词 + 百分比"的结论句; 反例 = 不含结论、或含词无数字、或含数字
// 无统计词 —— 三类都要能区分 (只钉正例会让判据退化成"见到 % 就报")。
func TestMetricClaimAudit_Vectors(t *testing.T) {
	positives := []string{
		"优化后成功率提升到 95%",
		"缓存命中率 98.8%, 会话级",
		"失败率从 12% 降到 3%",
		"gate 平均耗时下降 40%",
	}
	negatives := []string{
		"测试全绿, 1392 个用例通过",
		"这个方案成功率很高, 但没量过",
		"先把 50% 的冗余代码删掉",
		"",
	}
	for _, s := range positives {
		if metricClaimRe.FindStringIndex(s) == nil {
			t.Errorf("正例漏检: %q", s)
		}
	}
	for _, s := range negatives {
		if metricClaimRe.FindStringIndex(s) != nil {
			t.Errorf("反例误报: %q", s)
		}
	}
}

// TestMetricClaimAudit_SourceDetection 数据源识别 + 与虚报检测同口径 (只看本轮)。
func TestMetricClaimAudit_SourceDetection(t *testing.T) {
	toolSrc := ChatMessage{Role: "tool", ToolCallID: "1", Content: "跑 quality/quality_report.py 输出 ..."}
	cases := []struct {
		name     string
		messages []ChatMessage
		want     bool
	}{
		{"本轮读过源", []ChatMessage{
			{Role: "user", Content: "看下质量报告"}, toolSrc,
		}, true},
		{"本轮零工具", []ChatMessage{
			{Role: "user", Content: "看下质量报告"},
		}, false},
		{"历史读过源但本轮没读 (不得冒充)", []ChatMessage{
			{Role: "user", Content: "旧任务"}, toolSrc,
			{Role: "user", Content: "新任务"},
		}, false},
		{"工具输出与源无关", []ChatMessage{
			{Role: "user", Content: "跑个测试"},
			{Role: "tool", ToolCallID: "1", Content: "PASS"},
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasMetricSource(c.messages); got != c.want {
				t.Errorf("hasMetricSource = %v, 期望 %v", got, c.want)
			}
		})
	}
}

// TestMetricClaimAudit_Records 落盘契约 (20261004 二次修正后):
//  1. 每轮收尾必写一行 —— 分母 = 收尾轮数, 否则命中率永远算不出
//     (首版"只在命中时落行"实测上线后 25 次收尾零行, 决策依旧卡死);
//  2. 纯叙述轮只写分母行, 不带样本字段 (体积可控);
//  3. 强判据命中轮: hit=true + has_source + 样本摘要;
//  4. 弱信号未命中轮: hit=false 但 weak=true 且留样本 —— 这是改进强判据的语料来源。
func TestMetricClaimAudit_Records(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}

	metricClaimAudit(a, "没什么结论", []ChatMessage{{Role: "user", Content: "hi"}})
	metricClaimAudit(a, "优化后成功率提升到 95%", []ChatMessage{
		{Role: "user", Content: "汇报"},
		{Role: "tool", ToolCallID: "1", Content: "quality_report 输出"},
	})
	metricClaimAudit(a, "测试全绿, PASS 2057 / FAIL 0", []ChatMessage{{Role: "user", Content: "跑测试"}})

	data, err := os.ReadFile(auditFilePath(dir))
	if err != nil {
		t.Fatalf("未落盘: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("每轮收尾都应落一行: 期望 3 行, 得到 %d 行 (%q)", len(lines), data)
	}
	rows := make([]map[string]interface{}, 0, 3)
	for i, ln := range lines {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("第 %d 行非合法 JSON: %v (%q)", i, err, ln)
		}
		if m["event"] != "metric_claim" {
			t.Errorf("第 %d 行 event 错误: %v", i, m["event"])
		}
		if _, ok := m["ts"].(string); !ok {
			t.Errorf("第 %d 行缺 ts: %v", i, m)
		}
		for f := range auditEventRequired("metric_claim") {
			if _, ok := m[f]; !ok {
				t.Errorf("第 %d 行缺 audit_schema.go 声明的必填字段 %q", i, f)
			}
		}
		rows = append(rows, m)
	}
	// 行 0: 纯叙述 —— 分母行, 无样本字段
	if rows[0]["hit"] != false || rows[0]["weak"] != false {
		t.Errorf("纯叙述轮应 hit=false/weak=false: %v", rows[0])
	}
	if _, ok := rows[0]["sample"]; ok {
		t.Errorf("纯叙述轮不应写 sample (避免淹没数据): %v", rows[0])
	}
	// 行 1: 强判据命中
	if rows[1]["hit"] != true {
		t.Errorf("命中轮 hit 应为 true: %v", rows[1])
	}
	if rows[1]["has_source"] != true {
		t.Errorf("has_source 应为 true (本轮 tool 含 quality_report): %v", rows[1])
	}
	if v, _ := rows[1]["sample"].(string); !strings.Contains(v, "成功率") {
		t.Errorf("sample 摘要未记录: %v", rows[1]["sample"])
	}
	// 行 2: 计数式结论 —— 强判据漏报, 但必须留下语料
	if rows[2]["hit"] != false || rows[2]["weak"] != true {
		t.Errorf("计数式结论应 hit=false/weak=true: %v", rows[2])
	}
	if v, _ := rows[2]["sample"].(string); !strings.Contains(v, "PASS") {
		t.Errorf("弱信号轮应留样本 (漏报语料): %v", rows[2]["sample"])
	}
}
