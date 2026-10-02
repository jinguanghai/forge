// main_test.go: 入口函数与启动序的接线契约。
//
// main() 无法在测试进程内调用(一调就进 REPL), 因此改为 AST 断言:
// 启动序的每一步都必须在 main() 体内真实出现。删掉任何一步都会让该功能静默失效
// (例如删 runSelfReplace → 自替换永不生效; 删 defer Shutdown → 退出时缓存不落盘)。
//
// 本文件【严禁】定义 TestMain —— 它会接管全包测试入口, 静默跳过其余 900+ 用例。
package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestMain_NoTestMainDefined(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(wd, "main_test.go"), nil, 0)
	if err != nil {
		t.Fatalf("解析 main_test.go 失败: %v", err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name.Name == "TestMain" {
			t.Error("main_test.go 不得定义 TestMain —— 会接管全包测试入口")
		}
		return true
	})
}

func TestMain_StartupSequenceWired(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join(wd, "main.go"), nil, 0)
	if err != nil {
		t.Fatalf("解析 main.go 失败: %v", err)
	}
	var mainFn *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name.Name == "main" && fd.Recv == nil {
			mainFn = fd
		}
		return true
	})
	if mainFn == nil {
		t.Fatal("main.go 未找到 func main()")
	}
	calls := map[string]bool{}
	ast.Inspect(mainFn.Body, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			switch fun := ce.Fun.(type) {
			case *ast.Ident:
				calls[fun.Name] = true
			case *ast.SelectorExpr:
				calls[fun.Sel.Name] = true
			}
		}
		return true
	})
	required := []string{
		"runSelfReplace",          // 自替换就位
		"enableWindowsUTF8",       // 控制台 UTF-8 代码页
		"parseFlags",              // 参数解析
		"runStartupConfig",        // 配置装配
		"NewAgentRunner",          // agent 初始化
		"installSignals",          // Ctrl+C 处理
		"ensureConsoleProbe",      // 首轮状态栏可绘制
		"installCtrlCloseHandler", // 窗口关闭时保存缓存
		"checkPeakHour",           // 高峰提醒
		"setupHistoryFile",        // 历史文件
		"printHealthReport",       // 健康报告
		"printWelcome",            // 欢迎横幅
	}
	for _, want := range required {
		if !calls[want] {
			t.Errorf("main() 未调用 %s —— 该功能静默失效", want)
		}
	}
	if !calls["Shutdown"] {
		t.Error("main() 缺 agent.Shutdown() —— 退出时缓存不落盘, 下次冷启动全量 miss")
	}
	if !calls["runInteractive"] && !calls["runNonInteractive"] {
		t.Error("main() 未进入任何运行模式")
	}
}

func TestMain_VersionFormat(t *testing.T) {
	if AppVersion == "" {
		t.Fatal("AppVersion 为空")
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(AppVersion) {
		t.Errorf("AppVersion 格式异常: %q (期望 x.y.z)", AppVersion)
	}
}

func TestMain_PackageLevelState(t *testing.T) {
	// 三个包级状态必须存在且可读写(信号处理与交互循环共用)
	agentBusy.Store(true)
	if !agentBusy.Load() {
		t.Error("agentBusy 读写不一致")
	}
	agentBusy.Store(false)
	sigCount.Store(3)
	if sigCount.Load() != 3 {
		t.Error("sigCount 读写不一致")
	}
	sigCount.Store(0)
	exitRequested = true
	if !exitRequested {
		t.Error("exitRequested 读写不一致")
	}
	exitRequested = false
}
