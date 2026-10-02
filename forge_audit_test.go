package main

// forge_audit_test.go — 审计留痕的哨兵。
//
// 审计是"关掉护栏仍须留痕"的落点(见 lessons): 关闭开关 != 免于留痕。
// 这里钉住路径拼接与 JSONL 追加的基本契约。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestForgeAudit_AuditFilePath(t *testing.T) {
	got := auditFilePath("/d")
	if filepath.Base(got) != "gate_audit.jsonl" {
		t.Errorf("auditFilePath 基名 = %q, 期望 gate_audit.jsonl", filepath.Base(got))
	}
	if !strings.Contains(got, "d") {
		t.Errorf("auditFilePath 应包含传入目录, 得到 %q", got)
	}
}

func TestForgeAudit_AppendAuditJSONL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "gate_audit.jsonl")
	appendAuditJSONL(p, map[string]interface{}{"gate": "go", "n": 1})
	appendAuditJSONL(p, map[string]interface{}{"gate": "py", "n": 2})

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读回失败: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("写入 %d 行, 期望 2(追加语义)", len(lines))
	}
	// 每行必须是独立可解析的 JSON(JSONL 契约: 坏一行会拖垮整个分析脚本)
	for i, l := range lines {
		var v map[string]interface{}
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Errorf("第 %d 行不是合法 JSON: %v (%q)", i+1, err, l)
		}
	}
}

func TestForgeAudit_NetEgressHint(t *testing.T) {
	// 干净代码不得报网络出口
	if got := netEgressHint("package main\nfunc main() {}"); got != "" {
		t.Errorf("干净代码 netEgressHint = %q, 期望空串", got)
	}
	// 含网络出口的代码必须报出
	if got := netEgressHint("curl http://example.com"); got != "sh-net" {
		t.Errorf("curl 代码 netEgressHint = %q, 期望 \"sh-net\"", got)
	}
}

func TestSnipErrKeepsTailDiagnosis(t *testing.T) {
	long := "go build failed: # command-line-arguments\nAppData\\Local\\Temp\\forge_go_123\\main.go:123:9: undefined: fooBarBaz"
	got := snipErr(long, 100)
	if len(got) > 100 {
		t.Errorf("超长 %d: %q", len(got), got)
	}
	if !strings.Contains(got, "undefined: fooBarBaz") {
		t.Errorf("尾部诊断丢失: %q", got)
	}
	if short := "short err"; snipErr(short, 100) != short {
		t.Error("短文本应原样返回")
	}
	cn := strings.Repeat("中", 100)
	if g := snipErr(cn, 50); !utf8.ValidString(g) {
		t.Errorf("UTF-8 被切断: %q", g)
	}
}
