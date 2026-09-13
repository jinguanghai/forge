package main

// consistency_sentinel_test.go — 契约与一致性哨兵 (B3 批次)
//
// 背景: 同类行为散落多处 → 改一处漏一处 → 静默不一致。
// 本文件把"必须同源 / 必须接住错误 / 必须只读"固化为死程序判定。
// 教训来源(20260912): vision.go 注释写明"真文件读不动必须报错, 不能静默丢图",
// 而 agent.go 调用点把 error 丢了 —— 设计承诺与实现脱节, 无任何测试发现。

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── 1) 审计写入并发: 追加不得丢行 / 不得撕裂 ─────────────────────
// 实测: 32 goroutine x 20 行 (每行 256+ 字节) 未复现丢行/撕裂 —— 小写入恰好原子。
// 但 forge 侧有锁、agent 侧无锁属结构不一致, 故统一为唯一写入路径 + 单一锁 (预防性)。
func TestAuditLineConcurrentAppend(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	a := &AgentRunner{cfg: &Config{WorkDir: dir}}
	const goroutines, perG = 32, 20
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				appendAuditLine(a, map[string]interface{}{
					"g": g, "i": i, "pad": strings.Repeat("x", 256),
				})
			}
		}(g)
	}
	wg.Wait()

	raw, err := os.ReadFile(filepath.Join(dir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("读取审计文件失败: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != goroutines*perG {
		t.Errorf("并发追加丢行/多行: 期望 %d 行, 实得 %d", goroutines*perG, len(lines))
	}
	bad := 0
	for _, ln := range lines {
		var m map[string]interface{}
		if json.Unmarshal([]byte(ln), &m) != nil {
			bad++
		}
	}
	if bad > 0 {
		t.Errorf("撕裂行 %d/%d (并发写未串行化)", bad, len(lines))
	}
}

// ── 1b) 审计写入路径唯一: 不得有旁路 OpenFile ─────────────────────
func TestAuditSingleWritePath(t *testing.T) {
	fg, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatal(err)
	}
	ag, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fg), "appendAuditJSONL(auditFilePath(f.workDir), entry)") {
		t.Error("forge.go auditGate 未走唯一写入路径 appendAuditJSONL")
	}
	if !strings.Contains(string(ag), "appendAuditJSONL(auditFilePath(dir), entry)") {
		t.Error("agent.go appendAuditLine 未走唯一写入路径 appendAuditJSONL")
	}
	for _, s := range []string{string(fg), string(ag)} {
		if strings.Contains(s, "os.OpenFile(auditFilePath(") {
			t.Error("发现审计文件旁路写入 (绕过 appendAuditJSONL) — 锁与路径规则会脱节")
		}
	}
}

// ── 2) vision 契约: "路径存在而读取失败"必须返回 error ────────────
func TestDetectImagesReadFailureReturnsError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "坏图.png")
	if err := os.WriteFile(bad, []byte("this is not a real png"), 0644); err != nil {
		t.Fatal(err)
	}
	parts, err := detectImages("看图 坏图.png", dir)
	if len(parts) != 0 {
		t.Fatalf("非图内容不应产出 ImagePart, 实得 %d", len(parts))
	}
	if err == nil {
		t.Fatal("契约违背: 路径存在而校验失败必须返回 error (不得静默丢图)")
	}
	// 不存在 → 跳过语义, 不报错 (文本里的 .png 字样不污染)
	parts, err = detectImages("看图 根本不存在.png", dir)
	if err != nil || len(parts) != 0 {
		t.Fatalf("不存在路径应静默跳过: parts=%d err=%v", len(parts), err)
	}
}

// ── 3) agent 侧必须把识图错误透出给用户 ─────────────────────────
func TestAgentSurfacesImageError(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if strings.Contains(s, "images, _ := detectImages(") {
		t.Error("agent.go 丢弃 detectImages 的 error — 用户以为已发图, 实际静默降级纯文本")
	}
	if !strings.Contains(s, "images, imgErr := detectImages(") {
		t.Error("agent.go 未接住 detectImages 的 error")
	}
	if !strings.Contains(s, "图片读取失败") {
		t.Error("agent.go 未向用户提示图片读取失败")
	}
}

// ── 4) 超时兜底单一源: 值正确 + 源码内无散落字面量 ───────────────
func TestGateTimeoutSingleSource(t *testing.T) {
	if defaultGateTimeout != 30*time.Second {
		t.Errorf("defaultGateTimeout = %v, 期望 30s", defaultGateTimeout)
	}
	if deadBoundaryTimeout != 20*time.Second {
		t.Errorf("deadBoundaryTimeout = %v, 期望 20s", deadBoundaryTimeout)
	}
	if shGateTimeout != 15*time.Second {
		t.Errorf("shGateTimeout = %v, 期望 15s", shGateTimeout)
	}
	src, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, lit := range []string{
		"timeout := 20 * time.Second",
		"timeout = 30 * time.Second",
		"return 30 * time.Second",
		"WithTimeout(f.effCtx(), 15*time.Second)",
	} {
		if strings.Contains(s, lit) {
			t.Errorf("forge.go 仍有散落超时字面量 %q — 应引用单一源常量", lit)
		}
	}
}

// ── 5) check/lint/exec 三段同构: 判据不得缺一 ────────────────────
func TestGateArgExpansionConsistency(t *testing.T) {
	src, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, name := range []string{"compiler.Check", "compiler.Lint", "compiler.Exec"} {
		if strings.Contains(s, ", _ := expandArgs("+name) {
			t.Errorf("%s 调用点丢弃 hasFilePlaceholder — 三段同构代码判据不一致", name)
		}
	}
	if n := strings.Count(s, "!hasFilePlaceholder"); n < 3 {
		t.Errorf("forgeGate 内 !hasFilePlaceholder 判据仅 %d 处 (应 >=3: check/lint/exec 同构)", n)
	}
}

// ── 6) 全局只读表: 运行时零写入 (AST 判定) ──────────────────────
func TestReadOnlyTablesNotWrittenAtRuntime(t *testing.T) {
	targets := map[string][]string{
		"memory_recall.go": {"cnStopChars"},
		"nosword.go":       {"nswRatioWords"},
		"ux.go":            {"langKeywords", "langKeywordRE"},
	}
	for file, names := range targets {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", file, err)
		}
		for _, name := range names {
			curFunc := ""
			ast.Inspect(f, func(n ast.Node) bool {
				if fd, ok := n.(*ast.FuncDecl); ok {
					curFunc = fd.Name.Name
				}
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range as.Lhs {
					if rootIdentName(lhs) == name && curFunc != "init" {
						t.Errorf("%s: %s 在函数 %s 内被写入 — 只读表必须在 init 内完成初始化",
							file, name, curFunc)
					}
				}
				return true
			})
		}
	}
}

func rootIdentName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.IndexExpr:
		return rootIdentName(x.X)
	case *ast.SelectorExpr:
		return rootIdentName(x.X)
	}
	return ""
}
