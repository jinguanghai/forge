package main

// commands_session_test.go — 会话/配置类子命令 (commands_session.go) 的结构约束。
//
// 拆分引入的不变式:
//  1. 会话命令必须定义在 commands_session.go, 不得搬回 main_commands.go
//  2. 全部命令函数签名一致
//  3. 每个命令都被 handleCommand 分发
//  4. 全包 cmd* 函数不得重复定义 (拆分最怕复制粘贴出两份实现, 改一处漏一处)

import (
	"go/ast"
	"go/token"
	"os"
	"strings"
	"testing"
)

// sessionCmdNames 会话组命令函数 (与 commands_session.go 一一对应)。
var sessionCmdNames = []string{
	"cmdStats", "cmdGoal", "cmdHistory", "cmdReasoning", "cmdTheme",
	"cmdModel", "cmdRouter", "cmdUpgrade", "cmdUnfold", "cmdLast",
}

// 本文件复用 commands_diag_test.go 中的 parseTopFuncs / funcSigText / handleCommandCalls (同包)。
func TestCommandsSession_FileOwnership(t *testing.T) {
	_, _, sess := parseTopFuncs(t, "commands_session.go")
	_, _, main := parseTopFuncs(t, "main_commands.go")
	for _, n := range sessionCmdNames {
		if _, ok := sess[n]; !ok {
			t.Errorf("commands_session.go 缺 %s", n)
		}
		if _, ok := main[n]; ok {
			t.Errorf("main_commands.go 出现 %s —— 会话命令必须留在 commands_session.go", n)
		}
	}
}

func TestCommandsSession_SignatureUniform(t *testing.T) {
	fset, _, sess := parseTopFuncs(t, "commands_session.go")
	want := ""
	for _, n := range sessionCmdNames {
		fd, ok := sess[n]
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

func TestCommandsSession_RoutedFromHandleCommand(t *testing.T) {
	_, _, main := parseTopFuncs(t, "main_commands.go")
	called := handleCommandCalls(t, main)
	for _, n := range sessionCmdNames {
		if !called[n] {
			t.Errorf("handleCommand 未分发 %s —— 命令不可达", n)
		}
	}
}

func TestCommandsSession_NoPackageLevelState(t *testing.T) {
	_, f, _ := parseTopFuncs(t, "commands_session.go")
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue // import 块也是 GenDecl, 不算包级状态
		}
		if _, ok := d.(*ast.FuncDecl); !ok {
			t.Errorf("commands_session.go 出现非函数顶层声明 (%T) —— 纯函数集合不得引入包级状态", d)
		}
	}
}

// TestCommandsSession_NoDuplicateCmdDefs 全包扫描, 每个 cmd* 顶层函数只允许定义一次。
// 价值: 拆分时若复制粘贴导致两份实现, 改一处漏一处 —— 本测试直接拦住。
func TestCommandsSession_NoDuplicateCmdDefs(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读目录失败: %v", err)
	}
	seen := map[string]map[string]bool{} // 函数名 -> 出现过的平台 base 集合
	platBase := func(n string) string {
		b := strings.TrimSuffix(n, ".go")
		for _, suf := range []string{"_windows", "_other", "_linux", "_darwin"} {
			if strings.HasSuffix(b, suf) {
				return strings.TrimSuffix(b, suf)
			}
		}
		return b
	}
	nFiles := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		nFiles++
		_, _, m := parseTopFuncs(t, name)
		for n := range m {
			if !strings.HasPrefix(n, "cmd") {
				continue
			}
			b := platBase(name)
			if seen[n] == nil {
				seen[n] = map[string]bool{}
			}
			for prevBase := range seen[n] {
				if prevBase != b {
					t.Errorf("cmd 函数 %s 跨文件重复定义: %s.* 与 %s.*", n, prevBase, b)
				}
			}
			seen[n][b] = true
		}
	}
	if nFiles < 50 {
		t.Fatalf("仅扫到 %d 个生产文件, 疑似扫描失效", nFiles)
	}
	if len(seen) < 20 {
		t.Fatalf("仅扫到 %d 个 cmd* 函数, 疑似解析失效", len(seen))
	}
	t.Logf("扫描 %d 个生产文件, %d 个 cmd* 函数无跨文件重复 (平台变体已归并)", nFiles, len(seen))
}
