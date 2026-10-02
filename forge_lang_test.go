package main

// forge_lang_test.go — 语言探测 / 编译器表 / 编译错误解析的哨兵。
//
// 这些函数决定"代码是什么语言"与"编译报错落在哪一行" —— 判错语言的后果是
// 用错编译器(错误信息完全无关), 解析错行号则会让模型去改一个没问题的行。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestForgeLang_Atoi(t *testing.T) {
	if got := atoi("42"); got != 42 {
		t.Errorf("atoi(\"42\") = %d, 期望 42", got)
	}
	// 非数字必须退化为 0 而非 panic(编译器输出格式不可控)
	if got := atoi("x"); got != 0 {
		t.Errorf("atoi(\"x\") = %d, 期望 0", got)
	}
	if got := atoi("-7"); got != -7 {
		t.Errorf("atoi(\"-7\") = %d, 期望 -7", got)
	}
}

func TestForgeLang_HasGoPackageDecl(t *testing.T) {
	if !hasGoPackageDecl("package main\nfunc main() {}") {
		t.Error("含 package 声明的代码应判定为 true")
	}
	if hasGoPackageDecl("func main() {}") {
		t.Error("无 package 声明应判定为 false")
	}
}

func TestForgeLang_LooksLikeValidGoTopLevel(t *testing.T) {
	// self gate 的 append 动作靠它拦"把语句当顶层声明插入" —— 插错会让整个包编译失败
	if !looksLikeValidGoTopLevel("func foo() {}") {
		t.Error("函数声明应判定为合法顶层声明")
	}
	if looksLikeValidGoTopLevel("x := 1") {
		t.Error("语句不得判定为合法顶层声明")
	}
}

func TestForgeLang_DetectLang(t *testing.T) {
	cases := []struct {
		code, hint, want string
	}{
		{"package main\nimport \"fmt\"\nfunc main(){fmt.Println(1)}", "", "go"},
		{"import os\nprint(os.getcwd())", "", "python"},
		{"", "python", "python"}, // 显式 hint 优先于探测
	}
	for _, c := range cases {
		if got := forgeDetectLang(c.code, c.hint); got != c.want {
			t.Errorf("forgeDetectLang(hint=%q) = %q, 期望 %q", c.hint, got, c.want)
		}
	}
}

func TestForgeLang_ExeSuffix(t *testing.T) {
	if got := exeSuffix(); got != ".exe" {
		t.Errorf("exeSuffix() = %q, 期望 \".exe\"", got)
	}
}

func TestForgeLang_ParseGoErr(t *testing.T) {
	got := parseGoErr("./x.go:3:5: undefined: foo")
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条, 期望 1", len(got))
	}
	e := got[0]
	if e.Lang != "go" || e.Line != 3 || e.Col != 5 {
		t.Errorf("位置 = (%s,%d,%d), 期望 (go,3,5)", e.Lang, e.Line, e.Col)
	}
	if !strings.Contains(e.Msg, "undefined: foo") {
		t.Errorf("Msg = %q, 应含 \"undefined: foo\"", e.Msg)
	}
}

func TestForgeLang_ParsePyErr(t *testing.T) {
	got := parsePyErr("  File \"x.py\", line 7\n    x =\nSyntaxError: invalid syntax")
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条, 期望 1", len(got))
	}
	if got[0].Lang != "python" || got[0].Line != 7 {
		t.Errorf("位置 = (%s,%d), 期望 (python,7)", got[0].Lang, got[0].Line)
	}
	if !strings.Contains(got[0].Msg, "SyntaxError") {
		t.Errorf("Msg = %q, 应含 SyntaxError", got[0].Msg)
	}
}

func TestForgeLang_ParseCompilerErrorDispatch(t *testing.T) {
	// 未知语言必须返回 nil 而非 panic
	if got := parseCompilerError("rust", "some error"); len(got) != 0 {
		t.Errorf("未知语言解析出 %d 条, 期望 0", len(got))
	}
	if got := parseCompilerError("go", "./x.go:1:1: boom"); len(got) != 1 {
		t.Errorf("go 分支解析出 %d 条, 期望 1", len(got))
	}
}

func TestForgeLang_CompilerErrorsToJSON(t *testing.T) {
	// 空结果必须返回空串: 调用方据此判断"有没有结构化错误", 返回 "[]" 会被当成有错误
	if got := compilerErrorsToJSON(nil); got != "" {
		t.Errorf("空错误应返回空串, 得到 %q", got)
	}
	got := compilerErrorsToJSON([]CompilerError{{Lang: "go", Line: 1, Col: 2, Msg: "m"}})
	want := `[{"lang":"go","line":1,"col":2,"msg":"m"}]`
	if got != want {
		t.Errorf("JSON = %q, 期望 %q", got, want)
	}
}

func TestForgeLang_DetectSemanticGate(t *testing.T) {
	// 纯算式应路由到 math gate(语义路由), 普通代码不得被误路由
	if got := detectSemanticGate("x = 2 + 3"); got != "math" {
		t.Errorf("detectSemanticGate(算式) = %q, 期望 \"math\"", got)
	}
	if got := detectSemanticGate("print(1)"); got != "" {
		t.Errorf("detectSemanticGate(普通代码) = %q, 期望空串", got)
	}
}

// TestForgeDetectLang_ReturnsOnlyCompilerTableKeys 钉住 forgeDetectLang 的返回值域
// 必须 ⊆ 编译器表 keys —— 这是 forge_gate.go「不支持的语言」分支保持不可达的前置条件。
// 一旦有人给 forgeDetectLang 加表外返回值(如 "ruby"), 该分支会从不可达变可达,
// 缓存语义随之变化, 故必须前置拦截而非等运行时暴露。
func TestForgeDetectLang_ReturnsOnlyCompilerTableKeys(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge_lang.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge_lang.go 失败: %v", err)
	}

	tableKeys := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, nm := range vs.Names {
			if nm.Name != "铸剑炉_COMPILERS" || i >= len(vs.Values) {
				continue
			}
			cl, ok := vs.Values[i].(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, el := range cl.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if bl, ok := kv.Key.(*ast.BasicLit); ok {
						tableKeys[strings.Trim(bl.Value, `"`)] = true
					}
				}
			}
		}
		return true
	})
	if len(tableKeys) < 10 {
		t.Fatalf("编译器表解析异常, 仅取到 %d 个 key", len(tableKeys))
	}

	collect := func(fnName string) (lits, vars []string) {
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Name.Name != fnName {
				return true
			}
			ast.Inspect(fd.Body, func(m ast.Node) bool {
				rs, ok := m.(*ast.ReturnStmt)
				if !ok {
					return true
				}
				for _, r := range rs.Results {
					switch v := r.(type) {
					case *ast.BasicLit:
						lits = append(lits, strings.Trim(v.Value, `"`))
					case *ast.Ident:
						vars = append(vars, v.Name)
					}
				}
				return true
			})
			return false
		})
		return
	}

	for _, fnName := range []string{"forgeDetectLang", "detectSemanticGate"} {
		lits, vars := collect(fnName)
		if len(lits) == 0 {
			t.Fatalf("%s 未解析到任何字面量 return —— 解析器失效", fnName)
		}
		for _, l := range lits {
			if l == "" {
				continue // detectSemanticGate 的 "" = 不路由
			}
			if !tableKeys[l] && !retiredLangSentinels[l] {
				t.Errorf("%s 返回表外语言 %q —— 会让 forge_gate.go 的「不支持的语言」分支从不可达变可达", fnName, l)
			}
			if retiredLangSentinels[l] {
				// 退役哨兵豁免的前提: 它必须真的被 Build 的拒绝分支拦住。
				// 本断言读 forge.go 源码核对 —— 否则哨兵值会流到「不支持的语言」分支。
				forgeSrc, rerr := os.ReadFile("forge.go")
				if rerr != nil {
					t.Fatalf("读 forge.go: %v", rerr)
				}
				if !strings.Contains(string(forgeSrc), `lang == "`+l+`"`) {
					t.Errorf("退役哨兵 %q 未在 forge.go 的 Build 里被拦截 —— 豁免前提不成立", l)
				}
			}
		}
		for _, v := range vars {
			if v != "gate" && v != "hint" {
				t.Errorf("%s 返回未登记变量 %q —— 需人工确认其取值域 ⊆ 编译器表", fnName, v)
			}
		}
		t.Logf("%s: 字面量=%v 变量=%v", fnName, lits, vars)
	}
}
