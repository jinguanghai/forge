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
