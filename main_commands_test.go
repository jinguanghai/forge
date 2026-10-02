// main_commands_test.go: 命令分发的行为契约。
//
// 路由与签名一致性已由 commands_*_test.go 覆盖; 本文件补"分发本身"的边界:
// 空输入不得误判、未知命令必须走 cmdDefault、大小写归一、case 引用的函数必须存在。
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// withStdoutCapture 把 stdout 重定向到临时文件, 返回读取函数。
// 命令处理函数走 fmt.Printf(stdout), 与 spinner 的 stderr 不同。
func withStdoutCapture(t *testing.T) func() string {
	t.Helper()
	old := os.Stdout
	f, err := os.CreateTemp(t.TempDir(), "stdout*.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = f
	t.Cleanup(func() {
		os.Stdout = old
		_ = f.Close()
	})
	return func() string {
		_ = f.Sync()
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatalf("读 stdout 捕获文件失败: %v", err)
		}
		return string(b)
	}
}

func TestMainCommands_EmptyInputIgnored(t *testing.T) {
	get := withStdoutCapture(t)
	showR := true
	// 空串与纯空白必须静默返回 —— 不得 panic, 不得走 cmdDefault
	handleCommand("", nil, nil, "", &showR)
	handleCommand("   ", nil, nil, "", &showR)
	handleCommand("\t\n", nil, nil, "", &showR)
	if out := get(); out != "" {
		t.Errorf("空输入不应产生输出: %q", out)
	}
}

func TestMainCommands_UnknownGoesDefault(t *testing.T) {
	get := withStdoutCapture(t)
	showR := true
	handleCommand("/no-such-command", nil, nil, "", &showR)
	out := stripANSI(get())
	if !strings.Contains(out, "未知命令") {
		t.Errorf("未知命令应提示: %q", out)
	}
	if !strings.Contains(out, "/no-such-command") {
		t.Errorf("提示应回显命令名: %q", out)
	}
}

func TestMainCommands_CaseInsensitive(t *testing.T) {
	get := withStdoutCapture(t)
	showR := true
	handleCommand("/NOSUCHCMD", nil, nil, "", &showR)
	out := stripANSI(get())
	if !strings.Contains(out, "/nosuchcmd") {
		t.Errorf("命令名应小写归一: %q", out)
	}
}

func TestMainCommands_ExtraSpacesTolerated(t *testing.T) {
	get := withStdoutCapture(t)
	showR := true
	// strings.Fields 切分: 多余空白不应影响命令识别
	handleCommand("   /NOSUCHCMD   ", nil, nil, "", &showR)
	out := stripANSI(get())
	if !strings.Contains(out, "/nosuchcmd") {
		t.Errorf("多余空白应被容忍: %q", out)
	}
}

func TestMainCommands_NilCfgAndAgentSafe(t *testing.T) {
	// 未知命令路径不得触碰 cfg/agent —— 传 nil 若 panic 说明耦合了无关依赖
	get := withStdoutCapture(t)
	showR := true
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("cmdDefault 不应 panic: %v", r)
		}
	}()
	handleCommand("/unknown", nil, nil, "", &showR)
	_ = get()
}

func TestMainCommands_AllCasesResolveToDefinedFuncs(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// 必须扫【全包】: cmd* 函数在 F2 清债时已分散到 commands_diag.go /
	// commands_session.go, 只扫 main_commands.go 会把所有跨文件目标误判成"未定义"。
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, wd, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("解析包失败: %v", err)
	}
	pkg, ok := pkgs["main"]
	if !ok {
		t.Fatal("未找到 main 包")
	}
	defined := map[string]bool{}
	var hc *ast.FuncDecl
	for _, f := range pkg.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			if fd, ok := n.(*ast.FuncDecl); ok {
				defined[fd.Name.Name] = true
				if fd.Name.Name == "handleCommand" {
					hc = fd
				}
			}
			return true
		})
	}
	if hc == nil {
		t.Fatal("main_commands.go 未找到 handleCommand")
	}
	calls := map[string]bool{}
	ast.Inspect(hc.Body, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok && strings.HasPrefix(id.Name, "cmd") {
				calls[id.Name] = true
			}
		}
		return true
	})
	if len(calls) < 25 {
		t.Errorf("handleCommand 只分发 %d 个命令, 期望 >= 25 (分支被误删?)", len(calls))
	}
	for name := range calls {
		if !defined[name] {
			t.Errorf("handleCommand 调用了未定义函数 %s (改名后漏改调用点)", name)
		}
	}
}
