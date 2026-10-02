package main

// wiring_sentinel_test.go — 接线哨兵 (死程序判定)
//
// 事故背景(20260910): checkDangerousTarget 函数正确、guard_target_test.go
// 7正7反全绿, 但生产代码从未调用它 → 第二道防线(受保护目标×破坏谓词)
// 形同虚设。"测试全绿"给了假的安心。
// 教训: 验收必须查调用点, 不能只查函数行为。本文件把该判定固化为死程序。
//
// ── 判据升级 (哨兵审计, 变异测试实证) ─────────────────────────────────
// 原判据 strings.Contains(全包, fn+"(") 有三类漏判, 且前两类互相掩盖:
//   · 函数定义行 func fn(...) 本身即含 "fn(" 子串 → 调用点删光, 判据仍通过;
//   · 注释/字符串里提到 fn( 同样命中;
//   · 把调用改为 `_ = fn`(函数值引用, 编译通过) 后判据仍通过。
// 实测: nswAudit 的唯一调用点改为 `_ = nswAudit` 后文本版哨兵仍 PASS ——
// 本该抓的"删掉调用点"完全防不住。现改为 AST 口径(见 prodSymbolRefs)。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

var wiringRequirements = []struct {
	fn     string
	kind   wireKind
	inFile string
}{
	{"checkDangerousTarget", wireCall, "guard.go"},  // 第二道防线: 受保护目标×破坏谓词
	{"goalAnchorRef", wireCall, "tool_ref.go"},      // 大工具输出落盘引用
	{"storeToolOutputRef", wireCall, "tool_ref.go"}, // 落盘引用注册
	{"pruneToolOutput", wireCall, "agent_pure.go"},  // 引用剪枝
	// math 内联快速路径 (P0, 20261001): 删掉调用点只会「变慢」不会报错,
	// 正是最容易被无声退化成死代码的一类 —— 由哨兵钉住。
	{"mathInlineGate", wireMethodCall, "forge_gate_host.go"},
	// 审批提示的编码块解码 (2026-10 安全测试): 提示只显示乱码 = 主人盲批,
	// 而「解码了但没接进 confirmDangerous」在功能上等于没做 —— 由哨兵钉住。
	{"approvalExtraText", wireCall, "forge.go"},
}

func TestWiringSentinels(t *testing.T) {
	refs := prodSymbolRefs(t)
	for _, req := range wiringRequirements {
		if !refs.wired(req.kind, req.fn) {
			t.Errorf("未接线: 生产代码中未见 %s 的%s (预期归属 %s) — 功能写了但没接上", req.fn, req.kind, req.inFile)
		}
	}
}

// prodGoSources 读取包内全部生产源文件(排除 _test.go)并拼接返回。
//
// 用于**负向禁令**判定(如"不得出现某字面串")。为什么是包级而非单文件 (B3 批1):
// 源码扫描型哨兵若硬编码单个文件, 代码重组会同时制造两类误判 ——
//
//	· 正向断言("必须包含 X")误报: 功能还在, 只是换了文件;
//	· 负向断言("不得包含 X")假阳性: 扫描的文件里已无该代码, 断言恒真通过。
//
// 正向接线判定请改用 prodSymbolRefs (AST 口径), 文本口径防不住"删掉调用点"。
func prodGoSources(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		data, err := os.ReadFile(n)
		if err != nil {
			t.Fatalf("读取 %s: %v", n, err)
		}
		b.WriteString("// ==== " + n + " ====\n")
		b.Write(data)
		b.WriteString("\n")
	}
	return b.String()
}

// TestWiringCtrlCExitSavesCache 闲时 Ctrl+C 必须走 Shutdown 保存缓存。
func TestWiringCtrlCExitSavesCache(t *testing.T) {
	src, err := os.ReadFile("main_startup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	idx := strings.Index(s, "!agentBusy.Load() && sig == os.Interrupt")
	if idx < 0 {
		t.Fatal("未找到闲时退出分支")
	}
	tail := s[idx:]
	end := strings.Index(tail, "os.Exit(0)")
	if end < 0 {
		t.Fatal("未找到 os.Exit(0)")
	}
	if !strings.Contains(tail[:end], "agent.Shutdown()") {
		t.Error("闲时 Ctrl+C 退出未调用 agent.Shutdown() — os.Exit 会跳过 defer, 缓存不落盘")
	}
}

// TestWiringNoByteTruncationOnOutput 工具输出展示不得按字节截断(中文/emoji 会切碎)。
func TestWiringNoByteTruncationOnOutput(t *testing.T) {
	if strings.Contains(prodGoSources(t), "ol = ol[:120]") {
		t.Error("仍按字节截断工具输出行 — 应改 []rune 截断")
	}
}

// TestWiringApprovalExtraTextGetsHit 命中窗口必须真的拿到 hit。
// 变异实证: 把 confirmDangerous 里的 approvalExtraText(code, hit) 改成
// approvalExtraText(code, "") 仍编译通过、其余用例全绿, 而窗口从此永不显示。
// 文本禁令防不住这种退化, 故用 AST 口径断言第二实参就是标识符 hit。
func TestWiringApprovalExtraTextGetsHit(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge.go: %v", err)
	}
	found := 0
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "approvalExtraText" {
			return true
		}
		found++
		if len(call.Args) != 2 {
			t.Errorf("approvalExtraText 调用实参 %d 个, 期望 2 个 (code, hit)", len(call.Args))
			return true
		}
		arg, ok := call.Args[1].(*ast.Ident)
		if !ok || arg.Name != "hit" {
			t.Errorf("approvalExtraText 第二实参不是 hit — 命中窗口会静默失效")
		}
		return true
	})
	if found == 0 {
		t.Error("forge.go 中未找到 approvalExtraText 调用点 — 接线丢失")
	}
}
