package main

// commands_diag_test.go — 诊断类子命令 (commands_diag.go) 的结构约束与分发冒烟。
//
// 拆分引入的不变式:
//  1. 诊断命令必须定义在 commands_diag.go, 不得搬回 main_commands.go (搬回则 F2 债务回流)
//  2. 全部命令函数签名一致 —— handleCommand 靠统一签名做分发
//  3. handleCommand 的每个 case 都必须可安全执行 (新增命令自动纳入本测试)
//  4. commands_diag.go 只放函数, 不引入包级可变状态

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strings"
	"testing"
)

// diagCmdNames 诊断组命令函数 (与 commands_diag.go 一一对应)。
var diagCmdNames = []string{
	"cmdMemhealth", "cmdGatesync", "cmdAnchor", "cmdMemdiag",
	"cmdDiagnose", "cmdHealth", "cmdVision",
}

// parseTopFuncs 解析源文件, 返回 fset/文件/顶层函数表 (不含方法)。
func parseTopFuncs(t *testing.T, file string) (*token.FileSet, *ast.File, map[string]*ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	m := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
			m[fd.Name.Name] = fd
		}
	}
	return fset, f, m
}

// funcSigText 返回函数类型 (参数+返回值) 的源码文本。
func funcSigText(t *testing.T, fset *token.FileSet, fd *ast.FuncDecl) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, fd.Type); err != nil {
		t.Fatalf("打印 %s 签名失败: %v", fd.Name.Name, err)
	}
	return buf.String()
}

// handleCommandCalls 收集 handleCommand 函数体内直接调用的标识符。
func handleCommandCalls(t *testing.T, main map[string]*ast.FuncDecl) map[string]bool {
	t.Helper()
	fd, ok := main["handleCommand"]
	if !ok {
		t.Fatal("main_commands.go 缺 handleCommand")
	}
	called := map[string]bool{}
	ast.Inspect(fd, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if id, ok := c.Fun.(*ast.Ident); ok {
				called[id.Name] = true
			}
		}
		return true
	})
	return called
}

func TestCommandsDiag_FileOwnership(t *testing.T) {
	_, _, diag := parseTopFuncs(t, "commands_diag.go")
	_, _, main := parseTopFuncs(t, "main_commands.go")
	for _, n := range diagCmdNames {
		if _, ok := diag[n]; !ok {
			t.Errorf("commands_diag.go 缺 %s", n)
		}
		if _, ok := main[n]; ok {
			t.Errorf("main_commands.go 出现 %s —— 诊断命令必须留在 commands_diag.go", n)
		}
	}
}

func TestCommandsDiag_SignatureUniform(t *testing.T) {
	fset, _, diag := parseTopFuncs(t, "commands_diag.go")
	want := ""
	for _, n := range diagCmdNames {
		fd, ok := diag[n]
		if !ok {
			t.Fatalf("缺 %s", n)
		}
		got := funcSigText(t, fset, fd)
		if want == "" {
			want = got
			continue
		}
		if got != want {
			t.Errorf("%s 签名不一致:\n  期望 %s\n  实际 %s", n, want, got)
		}
	}
}

func TestCommandsDiag_RoutedFromHandleCommand(t *testing.T) {
	_, _, main := parseTopFuncs(t, "main_commands.go")
	called := handleCommandCalls(t, main)
	for _, n := range diagCmdNames {
		if !called[n] {
			t.Errorf("handleCommand 未分发 %s —— 命令不可达", n)
		}
	}
}

func TestCommandsDiag_NoPackageLevelState(t *testing.T) {
	_, f, _ := parseTopFuncs(t, "commands_diag.go")
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue // import 块也是 GenDecl, 不算包级状态
		}
		if _, ok := d.(*ast.FuncDecl); !ok {
			t.Errorf("commands_diag.go 出现非函数顶层声明 (%T) —— 纯函数集合不得引入包级状态", d)
		}
	}
}

// cmdSmokeSkip 列出不能在本测试中执行的命令, 原因必须写明 (新增命令若不安全, 须显式登记)。
var cmdSmokeSkip = map[string]string{
	"/upgrade":  "自替换 exe, 会破坏当前二进制",
	"/diagnose": "外部探测, 耗时且依赖网络",
	"/vision":   "需图片输入与网络",
	"/看图":       "需图片输入与网络",
	"/see":      "需图片输入与网络",
	"/voice":    "需麦克风设备",
}

// TestCommandsDiag_AllSwitchCasesSmoke 从 handleCommand 的 switch 动态提取全部 case 并逐个执行。
// 价值: 新增命令自动进入测试 —— 若新命令不安全, 必须显式登记到 cmdSmokeSkip 才能通过。
func TestCommandsDiag_AllSwitchCasesSmoke(t *testing.T) {
	_, _, main := parseTopFuncs(t, "main_commands.go")
	fd, ok := main["handleCommand"]
	if !ok {
		t.Fatal("缺 handleCommand")
	}
	var cases []string
	ast.Inspect(fd, func(n ast.Node) bool {
		cc, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range cc.List {
			if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
				cases = append(cases, strings.Trim(bl.Value, `"`))
			}
		}
		return true
	})
	if len(cases) < 20 {
		t.Fatalf("仅提取到 %d 个 case, 疑似解析失效 (期望 >=20)", len(cases))
	}
	for _, c := range cases {
		if reason, skip := cmdSmokeSkip[c]; skip {
			t.Logf("跳过 %s: %s", c, reason)
			continue
		}
		t.Run(strings.TrimPrefix(c, "/"), func(t *testing.T) {
			agent, cfg, hist := newHandleCmdAgent(t)
			sr := false
			captureStdout(t, func() { handleCommand(c, agent, cfg, hist, &sr) })
		})
	}
}
