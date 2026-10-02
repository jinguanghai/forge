package main

// nosword_lexer_test.go — 无剑解析器 (nosword_lexer.go) 的结构约束与端到端行为。
//
// 拆分引入的不变式:
//  1. 解析器声明必须留在 nosword_lexer.go
//  2. 解析路径不得有包级 var (无状态)
//  3. 不得引入非确定性依赖 (time/rand/net)

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// ── 本文件同时提供 nswTest* 系列 helper, 供其余 nosword_*_test.go 复用 ──

// nswTestAllFiles 无剑引擎拆分后的全部生产文件。
var nswTestAllFiles = []string{
	"nosword.go", "nosword_lexer.go", "nosword_eval.go", "nosword_sniff.go", "nosword_cand.go",
}

// nswTestForbiddenImports 无剑引擎禁止依赖的非确定性来源 —— 同输入必须逐字节同输出。
var nswTestForbiddenImports = map[string]string{
	"time":      "时间依赖破坏确定性 (反馈须逐字节稳定以保护缓存前缀)",
	"math/rand": "随机依赖破坏确定性",
	"net":       "网络依赖使求值不可复现",
}

// nswTestDecls 解析源文件, 返回全部顶层声明名 (函数/类型/变量/常量)。
func nswTestDecls(t *testing.T, file string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	m := map[string]bool{}
	for _, d := range f.Decls {
		switch dd := d.(type) {
		case *ast.FuncDecl:
			m[dd.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range dd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					m[s.Name.Name] = true
				case *ast.ValueSpec:
					for _, n := range s.Names {
						m[n.Name] = true
					}
				}
			}
		}
	}
	return m
}

// nswTestOwnership 断言 names 全部在 file 内, 且不出现在 others 中。
func nswTestOwnership(t *testing.T, file string, names []string, others ...string) {
	t.Helper()
	own := nswTestDecls(t, file)
	for _, n := range names {
		if !own[n] {
			t.Errorf("%s 缺 %s", file, n)
		}
	}
	for _, o := range others {
		om := nswTestDecls(t, o)
		for _, n := range names {
			if om[n] {
				t.Errorf("%s 出现 %s —— 已迁出的声明不得搬回", o, n)
			}
		}
	}
}

// nswTestDeterminism 断言文件未引入非确定性依赖。
func nswTestDeterminism(t *testing.T, files ...string) {
	t.Helper()
	for _, file := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", file, err)
		}
		for _, im := range f.Imports {
			p := strings.Trim(im.Path.Value, `"`)
			if why, bad := nswTestForbiddenImports[p]; bad {
				t.Errorf("%s 引入 %q —— %s", file, p, why)
			}
		}
	}
}

// nswTestNoPackageVar 断言文件无包级 var (该路径必须无状态)。
func nswTestNoPackageVar(t *testing.T, file string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
			t.Errorf("%s 出现包级 var —— 该路径必须无状态", file)
		}
	}
}

var nswLexerDecls = []string{
	"nswAnchor", "nswParser", "skip", "tok", "number", "expr", "term", "power", "unary", "factor",
}

func TestNSWLexer_Ownership(t *testing.T) {
	nswTestOwnership(t, "nosword_lexer.go", nswLexerDecls, "nosword.go", "nosword_eval.go", "nosword_cand.go")
}

func TestNSWLexer_NoPackageVar(t *testing.T) { nswTestNoPackageVar(t, "nosword_lexer.go") }

func TestNSWLexer_Deterministic(t *testing.T) { nswTestDeterminism(t, "nosword_lexer.go") }

// TestNSWLexer_EndToEndSemantics 钉住优先级与结合性 —— 拆分不得改变求值结果。
func TestNSWLexer_EndToEndSemantics(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"1+2*3", "7"},
		{"(1+2)*3", "9"},
		{"(10-2)-3", "5"},
		{"100/4/5", "5"},
		{"2*3+4*5", "26"},
	}
	for _, c := range cases {
		got, ok := nswEval(c.expr)
		if !ok {
			t.Errorf("nswEval(%q) 被拒, want %s", c.expr, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("nswEval(%q) = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestNSWLexer_SameInputSameOutput 确定性铁律: 同输入必须逐字节同输出。
func TestNSWLexer_SameInputSameOutput(t *testing.T) {
	const expr = "128*365+7*11"
	want, ok := nswEval(expr)
	if !ok {
		t.Fatalf("nswEval(%q) 被拒", expr)
	}
	for i := 0; i < 200; i++ {
		if got, ok2 := nswEval(expr); !ok2 || got != want {
			t.Fatalf("第 %d 次结果漂移: %q (首次 %q)", i, got, want)
		}
	}
}

// TestNSWLexer_ImplicitModeRejectsBareMinus 记录真实契约 (非缺陷):
// 隐式口径把裸 '-' 判为日期/范围形态 —— 防 "1853-1861" 被算成 -8 注入 LLM 上下文。
// 故 "1-2" / "10-2-3" 一律拒判; 需显式加括号才求值。
// 本测试防的是"有人好心修掉这个拒判"从而重新引入该事故。
func TestNSWLexer_ImplicitModeRejectsBareMinus(t *testing.T) {
	for _, e := range []string{"1-2", "10-2", "10-2-3", "10 - 2 - 3"} {
		if v, ok := nswEval(e); ok {
			t.Errorf("nswEval(%q) = %q, want 拒判 (隐式口径下裸 '-' 属日期/范围形态)", e, v)
		}
	}
	if v, ok := nswEval("(10-2)-3"); !ok || v != "5" {
		t.Errorf("nswEval(\"(10-2)-3\") = %q, %v; want 5, true", v, ok)
	}
}
