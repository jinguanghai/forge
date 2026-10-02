package main

// forge_gate_host_test.go — 自托管 gate(go/sh 走独立二进制)的哨兵。
//
// 行为测试需要真实编译, 成本高; 这里钉结构契约: 入口存在 + 分派到正确实现,
// 以及"编译器缺失/不支持语言"必须走环境性失败标记(而非误判为代码错误)。

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestForgeGateHost_MethodsExist(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge_gate_host.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge_gate_host.go: %v", err)
	}
	want := map[string]bool{
		"forgeGateSelfHosted": false,
		"selfHostedGate":      false,
		"selfHostedGo":        false,
	}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		if _, hit := want[fn.Name.Name]; hit {
			want[fn.Name.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("forge_gate_host.go 缺方法 %s", name)
		}
	}
}

func TestForgeGateHost_UnsupportedLangIsEnvFailure(t *testing.T) {
	// default 分支 = 配置漂移兜底(表标了 SelfHosted 却漏加 case), 属环境/能力缺失,
	// 不是代码文本错误 → 必须打标 EnvFailure: 不写缓存(补上 case 后即刻生效,
	// 不会命中旧错误结果), 且允许换语言重跑。
	// 可达性由 TestSelfHostedSwitchCoversCompilerTable 前置拦截(表与 switch 同步)。
	f := &Forge{workDir: t.TempDir(), ctx: context.Background()}
	r := f.forgeGateSelfHosted("code", "definitely-not-a-lang", CompilerDef{}, "", time.Now())
	if r.OK {
		t.Fatal("不支持的语言不应返回 OK")
	}
	if !r.EnvFailure {
		t.Error("default 分支应打标 EnvFailure=true (环境/能力缺失, 非代码错误)")
	}
	if !strings.Contains(r.Error, "not implemented") {
		t.Errorf("错误文案已变: %q", r.Error)
	}
}

// TestSelfHostedSwitchCoversCompilerTable 钉死「编译器表 SelfHosted 条目」与
// 「forgeGateSelfHosted 的 switch case」两个集合相等。
//
// 为什么需要: forgeGateSelfHosted 的 default 分支只在两者不同步时可达 —— 那是
// 配置漂移(表中加了 SelfHosted 条目却漏加 case)。本哨兵把它变成前置拦截:
// 一旦漂移测试当场红, 而不是等运行时靠 default 兜底。
//
// 判据用 AST 而非文本: 表项与 case 都从语法树取值, 注释/字符串里的同名字样不算。
// 反例已钉: 若把某个 case 注释掉, 本测试报「表标了 SelfHosted 但 switch 无 case」。
func TestSelfHostedSwitchCoversCompilerTable(t *testing.T) {
	inTable := compilerTableSelfHostedLangs(t)
	inSwitch := selfHostedSwitchCases(t)
	if len(inTable) == 0 || len(inSwitch) == 0 {
		t.Fatalf("AST 取值失败: 表=%v switch=%v (取值逻辑需随代码结构调整)", inTable, inSwitch)
	}
	var missing, extra []string
	for l := range inTable {
		if !inSwitch[l] {
			missing = append(missing, l)
		}
	}
	for l := range inSwitch {
		if !inTable[l] {
			extra = append(extra, l)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("编译器表标了 SelfHosted 但 switch 无 case: %v —— 这些语言会落到 default 兜底, 请补 case", missing)
	}
	if len(extra) > 0 {
		t.Errorf("switch 有 case 但表中未标 SelfHosted: %v —— 该 case 不可达", extra)
	}
	t.Logf("表与 switch 同步: %d 个 SelfHosted 语言", len(inTable))
}

// compilerTableSelfHostedLangs 从 铸剑炉_COMPILERS 取 SelfHosted=true 的 key。
func compilerTableSelfHostedLangs(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge_lang.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge_lang.go: %v", err)
	}
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		// 包级 var 声明是 ValueSpec(不是 KeyValueExpr —— 那只在 map/结构体字面量内部)
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Values) == 0 {
			return true
		}
		named := false
		for _, id := range vs.Names {
			if id.Name == "铸剑炉_COMPILERS" {
				named = true
			}
		}
		if !named {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			e, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			name, ok := e.Key.(*ast.BasicLit)
			if !ok || name.Kind != token.STRING {
				continue
			}
			body, ok := e.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			if compositeLitHasTrueField(body, "SelfHosted") {
				out[strings.Trim(name.Value, `"`)] = true
			}
		}
		return false
	})
	return out
}

// compositeLitHasTrueField 判定结构体字面量是否含 `Field: true`。
func compositeLitHasTrueField(lit *ast.CompositeLit, field string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		id, ok := kv.Key.(*ast.Ident)
		if !ok || id.Name != field {
			continue
		}
		if v, ok := kv.Value.(*ast.Ident); ok && v.Name == "true" {
			return true
		}
	}
	return false
}

// selfHostedSwitchCases 从 forgeGateSelfHosted 的 switch 取全部 case 字符串值。
func selfHostedSwitchCases(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge_gate_host.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge_gate_host.go: %v", err)
	}
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "forgeGateSelfHosted" {
			return true
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			sw, ok := m.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range sw.Body.List {
				cc, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range cc.List {
					if bl, ok := expr.(*ast.BasicLit); ok && bl.Kind == token.STRING {
						out[strings.Trim(bl.Value, `"`)] = true
					}
				}
			}
			return true
		})
		return false
	})
	return out
}

// TestForgeGateHost_GoRealBuildChain go 自托管 gate 的真实编译执行链
// (真 go build 到临时 exe → 真执行)。双向: 成功与编译错各一。
//
// 成本说明: 单文件 go build 约 0.3-1s (不依赖模块缓存外的网络)。
func TestForgeGateHost_GoRealBuildChain(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实 go build")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("本机无 go 工具链")
	}
	f := newTestForge(t)
	defer f.Shutdown()

	t.Run("成功", func(t *testing.T) {
		r := f.forgeGate(`package main

import "fmt"

func main() { fmt.Println("GO_GATE_OK") }
`, "go", "")
		if !r.OK {
			t.Fatalf("go gate 失败: stage=%s err=%s stderr=%s", r.Stage, r.Error, r.Stderr)
		}
		if r.Stage != "done" {
			t.Errorf("Stage = %q, want done", r.Stage)
		}
		if !strings.Contains(r.Stdout, "GO_GATE_OK") {
			t.Errorf("Stdout = %q", r.Stdout)
		}
	})

	t.Run("片段自动包main", func(t *testing.T) {
		// 不含 package main 的裸片段应被自动包进 main() 后编译执行
		r := f.forgeGate(`fmt.Println("FRAGMENT_OK")`, "go", "")
		if !r.OK {
			t.Fatalf("片段包裹失败: stage=%s err=%s", r.Stage, r.Error)
		}
		if !strings.Contains(r.Stdout, "FRAGMENT_OK") {
			t.Errorf("Stdout = %q", r.Stdout)
		}
	})

	t.Run("编译错_编译阶段拦截", func(t *testing.T) {
		r := f.forgeGate("package main\nfunc main() { undefinedThing() }\n", "go", "")
		if r.OK {
			t.Fatal("编译错不得返回 OK")
		}
		if r.Stage != "compile" {
			t.Errorf("Stage = %q, want compile", r.Stage)
		}
		if !strings.Contains(r.Error, "go build failed") {
			t.Errorf("Error = %q", r.Error)
		}
		if r.EnvFailure {
			t.Error("编译错是代码文本错误, 不得打环境性标记 (会被误判为换语言可救)")
		}
	})
}
