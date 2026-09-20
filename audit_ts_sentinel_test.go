package main

// audit_ts_sentinel_test.go — 埋点时间戳哨兵 (20260914, 六西格玛 C 控制阶段)
//
// 背景(实测): gate_audit.jsonl 里 compact 1861 条 + compact_failed 436 条
// 共 2297 条记录缺 ts 字段 —— 记录存在却无法按时间观测
// ("上周压缩几次" / "哪天开始失败" 都答不出来)。
// 同文件内 nosword_probe / gate_attempt 都写了 ts, 故此为遗漏而非设计。
//
// 本哨兵把"agent 侧埋点必须带 ts"固化为死程序判定:
// 任何新增或修改的 appendAuditLine 调用若漏写 "ts", 测试当场失败。

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// auditCallSite 一个 appendAuditLine 调用点的摘要。
type auditCallSite struct {
	line  int
	event string
	hasTS bool
}

// scanAuditCallSites 用 AST 解析 srcFile, 返回全部 appendAuditLine 调用点。
// 用 AST 而非文本匹配: 文本匹配分不清"注释里提到 ts"与"真的写了 ts 键"。
func scanAuditCallSites(t *testing.T, srcFile string) []auditCallSite {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, srcFile, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", srcFile, err)
	}
	var out []auditCallSite
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "appendAuditLine" || len(call.Args) < 2 {
			return true
		}
		lit, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			return true
		}
		site := auditCallSite{line: fset.Position(call.Pos()).Line, event: "?"}
		for _, e := range lit.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING {
				continue
			}
			k, _ := strconv.Unquote(key.Value)
			switch k {
			case "ts":
				site.hasTS = true
			case "event":
				if v, ok := kv.Value.(*ast.BasicLit); ok && v.Kind == token.STRING {
					site.event, _ = strconv.Unquote(v.Value)
				}
			}
		}
		out = append(out, site)
		return true
	})
	return out
}

// TestAuditCallSitesHaveTS 静态判定: agent.go 每个埋点都必须带 ts。
func TestAuditCallSitesHaveTS(t *testing.T) {
	sites := scanAuditCallSites(t, "agent.go")
	if len(sites) == 0 {
		t.Fatal("未扫描到任何 appendAuditLine 调用点 — 扫描逻辑失效")
	}
	var bad []string
	for _, s := range sites {
		if !s.hasTS {
			bad = append(bad, fmt.Sprintf("%s(agent.go:%d)", s.event, s.line))
		}
	}
	if len(bad) > 0 {
		t.Errorf("埋点缺 ts 字段 %d/%d: %v", len(bad), len(sites), bad)
	}
	t.Logf("agent.go 共 %d 个 appendAuditLine 调用点, 全部含 ts", len(sites))
}

// TestAuditTSRoundTrip 端到端: ts 真的落盘, 且是合法 RFC3339Nano。
func TestAuditTSRoundTrip(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}
	want := time.Now().Format(time.RFC3339Nano)
	appendAuditLine(a, map[string]interface{}{
		"event": "compact", "ts": want, "compressed_msgs": 3,
	})

	raw, err := os.ReadFile(filepath.Join(dir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("读取审计文件失败: %v", err)
	}
	line := strings.Split(strings.TrimSpace(string(raw)), "\n")[0]
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("审计行不是合法 JSON: %v", err)
	}
	if got["ts"] != want {
		t.Errorf("ts 未落盘: got=%v want=%v", got["ts"], want)
	}
	if _, err := time.Parse(time.RFC3339Nano, got["ts"].(string)); err != nil {
		t.Errorf("ts 不是合法 RFC3339Nano: %v", err)
	}
	t.Logf("落盘 JSON: %s", line)
}

// TestCompactFailedAuditHasTS 真实代码路径: 走 maybeCompact 的摘要失败分支,
// 验证 compact_failed 埋点真的带 ts (而非只验证我手写的 map)。
// 手法: BaseURL 指向 127.0.0.1:1 (必然拒连) + 禁代理, 让 Summarize 确定性失败。
func TestCompactFailedAuditHasTS(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	cfg := &Config{
		WorkDir:               dir,
		BaseURL:               "http://127.0.0.1:1", // 必然拒连 → Summarize 返回 error
		Model:                 "unused",
		CompactEnabled:        true,
		CompactTokenThreshold: 1, // 强制越过阈值检查
	}
	a := &AgentRunner{
		cfg:     cfg,
		llm:     &LLMClient{cfg: cfg, client: &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}},
		ctx:     context.Background(),
		headLen: 1,
	}
	a.history = append(a.history, ChatMessage{Role: "system", Content: "固定头"})
	for i := 0; i < 19; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		a.history = append(a.history, ChatMessage{Role: role, Content: "填充内容填充内容填充内容"})
	}
	a.maybeCompact()

	raw, err := os.ReadFile(filepath.Join(dir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("未产生审计文件 — compact_failed 分支可能没走到: %v", err)
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("审计行不是合法 JSON: %v (%s)", err, line)
		}
		if rec["event"] != "compact_failed" {
			continue
		}
		found = true
		tsv, ok := rec["ts"].(string)
		if !ok || tsv == "" {
			t.Fatalf("compact_failed 埋点缺 ts: %v", rec)
		}
		if _, err := time.Parse(time.RFC3339Nano, tsv); err != nil {
			t.Errorf("compact_failed 的 ts 不是合法 RFC3339Nano: %v", err)
		}
		t.Logf("compact_failed 落盘: %s", line)
	}
	if !found {
		t.Fatal("未捕获 compact_failed 事件 — 测试构造失效, 请检查阈值/配对逻辑")
	}
}
