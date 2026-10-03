package main

// audit_diag_test.go — 诊断留证的哨兵 (20261003)。
//
// 缺口(实测, 非推测): gate_audit.jsonl 862 条失败 gate 记录里, 超时类只剩
// "python execution failed: context deadline exceeded"(49 字符) —— diagnostics
// (结构化 JSON / 超时诊断文本) 此前只进渲染层回灌给 LLM, 审计侧拿不到
// "它当时卡在哪一行"。事后分析只能靠倒推。
//
// 本哨兵钉三件事:
//   1. 有诊断必落 diag 且内容保真 (截断时补 diag_len)
//   2. 无诊断不得凭空造字段 (否则字段恒真, "多少失败带定位"这个量就量不出来)
//   3. 端到端: 真跑一次编译失败, 审计里必须能读到可定位的诊断

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditGateEmitsDiagnostics(t *testing.T) {
	os.Setenv("FORGE_GATE_AUDIT", "1")
	defer os.Unsetenv("FORGE_GATE_AUDIT")
	wd := t.TempDir()
	f := &Forge{workDir: wd}

	// ① 正例: 诊断保真落盘
	diag := `[{"lang":"python","line":2,"col":15,"msg":"SyntaxError: '(' was never closed"}]`
	res := ForgeGateResult{OK: false, Lang: "python", Stage: "compile",
		Error: "python syntax check failed", Diagnostics: diag}
	f.auditGate("python", false, false, time.Now(), 0, 10, 0, "", &res)
	e := lastAuditEntry(t, wd)
	if got, _ := e["diag"].(string); got != diag {
		t.Errorf("diag 未落或失真:\n got=%q\nwant=%q", got, diag)
	}
	if _, ok := e["diag_len"]; ok {
		t.Errorf("未截断不该带 diag_len: %v", e)
	}
	// err_snip 与 diag 并存, 不是替换
	if e["err_snip"] == nil {
		t.Error("diag 不该挤掉 err_snip (二者语义不同: 发生了什么 vs 错在哪)")
	}

	// ② 反例: 无诊断不得凭空造字段
	res2 := ForgeGateResult{OK: false, Lang: "python", Stage: "execute", Error: "boom"}
	f.auditGate("python", false, false, time.Now(), 0, 10, 0, "", &res2)
	if e := lastAuditEntry(t, wd); e["diag"] != nil {
		t.Errorf("无诊断不该有 diag 字段: %v", e)
	}

	// ③ 反例: 成功记录不带 diag
	f.auditGate("python", false, false, time.Now(), 0, 10, 0, "",
		&ForgeGateResult{OK: true, Lang: "python", Stage: "done"})
	if e := lastAuditEntry(t, wd); e["diag"] != nil {
		t.Errorf("成功记录不该有 diag: %v", e)
	}

	// ④ 超长: 截断到上限, 原始长度留痕 (否则读者不知道看到的是全貌还是碎片)
	long := strings.Repeat("x", auditDiagLimit*3)
	res3 := ForgeGateResult{OK: false, Lang: "python", Error: "e", Diagnostics: long}
	f.auditGate("python", false, false, time.Now(), 0, 10, 0, "", &res3)
	e3 := lastAuditEntry(t, wd)
	got, _ := e3["diag"].(string)
	if len(got) > auditDiagLimit {
		t.Errorf("diag 未截断: %d 字节 > 上限 %d", len(got), auditDiagLimit)
	}
	if v, _ := e3["diag_len"].(float64); int(v) != len(long) {
		t.Errorf("diag_len = %v, 期望 %d", e3["diag_len"], len(long))
	}
}

func TestAuditAttemptEmitsDiagnostics(t *testing.T) {
	os.Setenv("FORGE_GATE_AUDIT", "1")
	defer os.Unsetenv("FORGE_GATE_AUDIT")
	wd := t.TempDir()
	f := &Forge{workDir: wd}

	diag := `[{"lang":"go","line":7,"col":2,"msg":"undefined: foo"}]`
	f.auditAttempt("go", 1, ForgeGateResult{OK: false, Lang: "go", Stage: "compile",
		Error: "go build failed", Diagnostics: diag})

	e := lastAuditEntry(t, wd)
	if e["event"] != "gate_attempt" {
		t.Fatalf("event = %v, 期望 gate_attempt", e["event"])
	}
	if got, _ := e["diag"].(string); got != diag {
		t.Errorf("gate_attempt 未落 diag: %q", got)
	}

	// 反例: 无诊断不落字段
	f.auditAttempt("go", 2, ForgeGateResult{OK: false, Lang: "go", Stage: "execute", Error: "x"})
	if e := lastAuditEntry(t, wd); e["diag"] != nil {
		t.Errorf("无诊断不该有 diag: %v", e)
	}
}

// TestAuditDiagEndToEnd 端到端: 真跑一次编译失败, 审计里必须留下可定位的诊断。
//
// 这是本组判据里唯一"不伪造结果"的一条 —— 前两条直接调 auditGate 传构造值,
// 能证明写入侧, 证不了"Build 真失败时 Diagnostics 确实被填上"。
func TestAuditDiagEndToEnd(t *testing.T) {
	os.Setenv("FORGE_GATE_AUDIT", "1")
	defer os.Unsetenv("FORGE_GATE_AUDIT")
	wd := t.TempDir()
	f := &Forge{workDir: wd, ctx: context.Background(),
		sem: make(chan struct{}, 1), cache: make(map[string]ForgeGateResult)}

	if _, _, err := f.Build("def f(:\n    pass\n", "python", ""); err == nil {
		t.Fatal("语法错代码必须失败")
	}

	raw, err := os.ReadFile(filepath.Join(wd, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("读审计失败: %v", err)
	}
	var diag string
	found := false
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var o map[string]interface{}
		if json.Unmarshal([]byte(l), &o) != nil {
			continue
		}
		if o["event"] != "gate" || o["ok"] != false {
			continue
		}
		found = true
		diag, _ = o["diag"].(string)
	}
	if !found {
		t.Fatal("审计里没有失败 gate 记录 —— 链路根本没走到")
	}
	if !strings.Contains(diag, "SyntaxError") {
		t.Errorf("端到端诊断不可定位(拿不到 SyntaxError): %q", diag)
	}
	if !strings.Contains(diag, `"line"`) {
		t.Errorf("诊断应是结构化 JSON(带行号), 得到: %q", diag)
	}
}
