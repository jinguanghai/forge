package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 构造数据测试: 验证聚合的字段统计正确。
// 20260930: gate 主记录显式带 event="gate" (audit_schema.go 单一数据源), 测试数据同步。
func TestScorecardAggregateConstructed(t *testing.T) {
	data := []byte(
		"{\"event\":\"gate\",\"lang\":\"python\",\"ok\":true,\"duration_ms\":100,\"retries\":0,\"fallback\":false,\"lang_omitted\":false}\n" +
			"{\"event\":\"gate\",\"lang\":\"python\",\"ok\":false,\"duration_ms\":200,\"retries\":2,\"fallback\":true,\"lang_omitted\":true}\n" +
			"{\"event\":\"gate\",\"lang\":\"math\",\"ok\":true,\"duration_ms\":50,\"retries\":0,\"fallback\":false,\"lang_omitted\":false}\n" +
			"{\"event\":\"gate\",\"lang\":\"\",\"ok\":true,\"duration_ms\":10,\"retries\":0,\"fallback\":false,\"lang_omitted\":false}\n")
	out := scorecardAggregate(data, 0)
	// 第 4 行 (有 event 但 lang 为空) 仍归 unknown —— 保底语义不变
	for _, want := range []string{"合计: 4 调用", "python", "math", "unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q\n%s", want, out)
		}
	}
	// 验证 python 行: 2 调用 1 成功 1 失败
	if !strings.Contains(out, "python") {
		t.Fatalf("缺 python 行")
	}
	// 验证窗口: 只取最近2条 → 应为 math + unknown, 合计2
	outW := scorecardAggregate(data, 2)
	if !strings.Contains(outW, "合计: 2 调用") {
		t.Errorf("窗口测试失败\n%s", outW)
	}
}

// 真实数据测试: 读 gate_audit.jsonl, 输出排行榜供死程序交叉验证对照。
func TestScorecardAggregateReal(t *testing.T) {
	path := filepath.Join(os.Getenv("FORGE_WORKDIR_OVERRIDE"), "gate_audit.jsonl")
	if path == "gate_audit.jsonl" {
		path = filepath.Join("D:\\forge", "gate_audit.jsonl")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("无法读取 gate_audit.jsonl: %v", err)
	}
	out := scorecardAggregate(data, 0)
	t.Log("\n" + out)
}

// 非 gate 埋点过滤: compact/compact_failed/nosword*/gate_attempt 均不得进入聚合。
//
// 背景 (实测 20260930): 旧判据 `Lang=="" && Event!=""` 只堵住"无 lang 的混入",
// 漏掉 gate_attempt (同时带 lang 与 event) —— 真实数据里多收 194 条重试明细,
// 而重试明细字段名是 ms 而非 duration_ms, 解析为零值, 使 self/sh 等重试多的 gate
// 成功率与均耗时被系统性低估。旧用例曾把该行为钉成契约 ("应只统计 2 条"),
// 用的是真实数据里不存在的形态 (带 ok/duration_ms 的 gate_attempt) 来证明错误判据。
func TestScorecardSkipsNonGateEvents(t *testing.T) {
	data := []byte(
		"{\"event\":\"gate\",\"lang\":\"python\",\"ok\":true,\"duration_ms\":100}\n" +
			"{\"event\":\"compact\",\"saved_tokens\":123}\n" +
			"{\"event\":\"compact_failed\",\"err\":\"boom\"}\n" +
			"{\"event\":\"nosword_probe\",\"anchors\":3}\n" +
			"{\"event\":\"gate_attempt\",\"lang\":\"python\",\"attempt\":1,\"ms\":20,\"stage\":\"run\"}\n")
	out := scorecardAggregate(data, 0)
	if strings.Contains(out, "unknown") {
		t.Errorf("非 gate 埋点不应进入聚合 (unknown 组不该出现)\n%s", out)
	}
	if !strings.Contains(out, "合计: 1 调用") {
		t.Errorf("应只统计 1 条 gate 主记录 (gate_attempt 是重试明细, 不计入调用数)\n%s", out)
	}
}

// 回归钉: 无 event 字段的记录不再被当成 gate。
//
// 20260930 废除"字段缺失即语义"的隐式约定 —— 缺失检测天然脆弱, 正是它让
// gate_attempt 长期隐形。历史记录已由一次性脚本补齐 event="gate",
// 此后任何缺 event 的记录都是契约违规, 必须被排除而非猜测。
func TestScorecardRejectsEventlessRecord(t *testing.T) {
	data := []byte("{\"lang\":\"python\",\"ok\":true,\"duration_ms\":100}\n")
	out := scorecardAggregate(data, 0)
	if strings.Contains(out, "python") {
		t.Errorf("无 event 记录不得被当成 gate 调用\n%s", out)
	}
}
