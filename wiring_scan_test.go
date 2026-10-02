package main

// wiring_scan_test.go — 接线哨兵的生产源码扫描: symRefs 引用图、prodSymbolRefs、孤儿函数断言。
// 20260927 自 wiring_sentinel_test.go 拆出 (同上)。

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

type symRefs struct {
	called       map[string][]string // 真调用 fn(...)        key=函数名
	calledMethod map[string][]string // 方法调用 x.fn(...)     key=方法名
	selected     map[string][]string // x.Name / pkg.Name     key=Name 与 X.Name+"."+Name
	referred     map[string][]string // 裸标识符(函数值/常量)  key=标识符
	funcs        map[string]string   // 包级函数名 -> 定义文件
}

// evidence 返回该符号在指定形态下的出现位置清单。
func (r symRefs) evidence(k wireKind, name string) []string {
	switch k {
	case wireCall:
		return r.called[name]
	case wireMethodCall:
		return r.calledMethod[name]
	case wireSelector:
		return r.selected[name]
	default:
		return r.referred[name]
	}
}

// wired 生产代码中是否存在该形态的引用。
func (r symRefs) wired(k wireKind, name string) bool {
	return len(r.evidence(k, name)) > 0
}

// prodSymbolRefs 用 AST 解析全包生产 .go 文件, 统计三类符号引用证据。
//
// 为什么必须用 AST: 文本匹配分不清"函数定义行 / 注释提及 / 真正的调用点"三者,
// 于是"删掉调用点"这类最该防的改动反而防不住(变异测试实证, 见文件头注释)。
// 扫描范围是包级而非单文件: 接线是包级属性, 代码重组会换文件而不改接线。
func prodSymbolRefs(t *testing.T) symRefs {
	t.Helper()
	r := symRefs{
		called:       map[string][]string{},
		calledMethod: map[string][]string{},
		selected:     map[string][]string{},
		referred:     map[string][]string{},
		funcs:        map[string]string{},
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		scanned++
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				r.funcs[fd.Name.Name] = name
			}
		}
		// 第一遍: 收集"定义类位置" —— 落在这些位置的标识符不是引用。
		defPos := map[token.Pos]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				defPos[x.Name.Pos()] = true
			case *ast.Field: // 结构体字段名 / 参数名 / 返回值名
				for _, nm := range x.Names {
					defPos[nm.Pos()] = true
				}
			case *ast.ValueSpec: // var / const 名
				for _, nm := range x.Names {
					defPos[nm.Pos()] = true
				}
			case *ast.ImportSpec:
				if x.Name != nil {
					defPos[x.Name.Pos()] = true
				}
			case *ast.KeyValueExpr: // 结构体字面量的字段名
				if id, ok := x.Key.(*ast.Ident); ok {
					defPos[id.Pos()] = true
				}
			case *ast.SelectorExpr: // x.Name 的 Name 是字段/方法名, 不计入标识符引用
				defPos[x.Sel.Pos()] = true
			case *ast.AssignStmt: // := 左侧新变量(含局部遮蔽同名)
				if x.Tok == token.DEFINE {
					for _, lhs := range x.Lhs {
						if id, ok := lhs.(*ast.Ident); ok {
							defPos[id.Pos()] = true
						}
					}
				}
			case *ast.LabeledStmt:
				defPos[x.Label.Pos()] = true
			}
			return true
		})
		// 第二遍: 统计引用证据。
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				switch fn := x.Fun.(type) {
				case *ast.Ident: // 包级函数调用 fn(...)
					r.called[fn.Name] = append(r.called[fn.Name], at(fset, name, x.Pos()))
				case *ast.SelectorExpr: // 方法调用 x.fn(...)
					r.calledMethod[fn.Sel.Name] = append(r.calledMethod[fn.Sel.Name], at(fset, name, x.Pos()))
				}
			case *ast.SelectorExpr:
				p := at(fset, name, x.Sel.Pos())
				r.selected[x.Sel.Name] = append(r.selected[x.Sel.Name], p)
				if id, ok := x.X.(*ast.Ident); ok {
					k := id.Name + "." + x.Sel.Name
					r.selected[k] = append(r.selected[k], p)
				}
			case *ast.Ident:
				if !defPos[x.Pos()] {
					r.referred[x.Name] = append(r.referred[x.Name], at(fset, name, x.Pos()))
				}
			}
			return true
		})
	}
	if scanned == 0 {
		t.Fatal("未扫描到任何生产 .go 文件 — 哨兵自身失效(扫描范围或工作目录错误)")
	}
	return r
}

func at(fset *token.FileSet, file string, p token.Pos) string {
	return fmt.Sprintf("%s:%d", file, fset.Position(p).Line)
}

// assertNoOrphanFuncs 判定 file 中每个包级函数在生产代码里确有引用。
//
// allowlist 是显式豁免(必须写明原因), 且反向检查豁免是否过期 ——
// 若豁免项在生产代码中已被引用, 说明它不再是孤儿, 必须移出豁免表:
// 豁免表不得成为垃圾场。
func assertNoOrphanFuncs(t *testing.T, refs symRefs, file string, allowlist map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	checked, exempted := 0, 0
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil {
			continue
		}
		name := fd.Name.Name
		checked++
		n := len(refs.called[name]) + len(refs.referred[name])
		if reason, ok := allowlist[name]; ok {
			exempted++
			if n > 0 {
				t.Errorf("孤儿豁免已过期: %s@%s 在生产代码中已被引用(%d 处) — 请移出豁免表", name, file, n)
			} else if reason == "" {
				t.Errorf("孤儿豁免 %s@%s 未写明原因 — 豁免必须可追溯", name, file)
			}
			continue
		}
		if n == 0 {
			t.Errorf("孤儿函数: %s@%s 生产代码零调用零引用 — 实现完整但未接线, 设开关也不生效", name, file)
		}
	}
	if checked == 0 {
		t.Fatalf("%s 未解析到任何包级函数 — 哨兵自身失效", file)
	}
	t.Logf("%s: 包级函数 %d 个, 全部有引用 (显式豁免 %d)", file, checked, exempted)
}

// wiringRequirements 关键功能函数 → 必须在包内生产代码中被真正调用。
// inFile 仅为"当前归属文件"提示, 不参与判定 —— 判据是包级(见 prodSymbolRefs)。
