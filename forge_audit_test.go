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
	"time"
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

// lastAuditEntry 读审计文件末行并解析 (判定型断言用)。
func lastAuditEntry(t *testing.T, dir string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("读审计失败: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &v); err != nil {
		t.Fatalf("末行非合法 JSON: %v", err)
	}
	return v
}

// TestAuditGateEmitsNearBudget 钉住贴边率领先指标 (P1-4a, 20261002)。
//
// 为什么需要领先指标: 失败率/白耗/P95 都是滞后指标。实测贴边区(20-30s)有 606 条
// 且 100% 成功(全是侥幸), 而贴边率 W36 2.18% → W40 5.12% 翻倍时失败率还没动 ——
// 等到失败率抬头, 白耗已经付过了。
// 判据: 净耗时(duration - 审批等待) ≥ 预算 80% 且非缓存命中 → near_budget + net_ms。
func TestAuditGateEmitsNearBudget(t *testing.T) {
	root := t.TempDir()
	_, cfg, _ := newHandleCmdAgent(t)
	f := NewForge(root, cfg)
	t.Cleanup(f.Shutdown)
	done := func() *ForgeGateResult {
		return &ForgeGateResult{OK: true, Lang: "python", Stage: "done"}
	}

	// ① 贴边: 伪造 25s 前开始 (python 预算 30s → 贴边线 24s)
	f.auditGate("python", false, false, time.Now().Add(-25*time.Second), 0, 10, 0, "", done())
	e := lastAuditEntry(t, root)
	if e["near_budget"] != true {
		t.Errorf("25s/30s 应判贴边: %v", e)
	}
	if _, ok := e["net_ms"]; !ok {
		t.Error("贴边记录必须带 net_ms —— 只有布尔看不出贴多紧")
	}

	// ② 反例: 3s 完成不贴边 (否则字段恒真, 等于没有指标)
	f.auditGate("python", false, false, time.Now().Add(-3*time.Second), 0, 10, 0, "", done())
	if e := lastAuditEntry(t, root); e["near_budget"] != nil {
		t.Errorf("3s/30s 不该判贴边: %v", e)
	}

	// ③ 反例: 缓存命中不贴边 —— 命中不消耗预算, 且已有 cache_hit 单独标注
	f.auditGate("python", false, false, time.Now().Add(-25*time.Second), 0, 10, 0, "",
		&ForgeGateResult{OK: true, Lang: "python", Stage: "done", CachedAt: time.Now().Unix()})
	if e := lastAuditEntry(t, root); e["near_budget"] != nil {
		t.Errorf("缓存命中不该判贴边: %v", e)
	}

	// ④ 审批等待不计入净耗时: 25s 里 20s 是等人按 y → 净 5s, 不贴边
	f.auditGate("python", false, false, time.Now().Add(-25*time.Second), 20*time.Second, 10, 0, "", done())
	if e := lastAuditEntry(t, root); e["near_budget"] != nil {
		t.Errorf("审批等待不该计入净耗时(否则人工慢=炉子慢): %v", e)
	}
}
