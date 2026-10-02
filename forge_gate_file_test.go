package main

// forge_gate_file_test.go — 文件式 gate(把代码写临时文件后调编译器)的哨兵。

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"
)

func TestForgeGateFile_MethodsExist(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge_gate_file.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge_gate_file.go: %v", err)
	}
	want := map[string]bool{"forgeGateInline": false, "forgeGateFile": false}
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
			t.Errorf("forge_gate_file.go 缺方法 %s", name)
		}
	}
}

func TestForgeGateFile_MissingCompilerFailsClosed(t *testing.T) {
	// 编译器不存在时必须失败且打环境性标记, 不得静默返回 OK
	// ctx 必须显式给: effCtx() 回落到 f.ctx, nil 会在 context.WithTimeout 处 panic
	f := &Forge{workDir: t.TempDir(), ctx: context.Background()}
	comp := CompilerDef{Check: []string{"definitely-not-a-real-binary-xyz", "{file}"}}
	r := f.forgeGateFile("print(1)", "python", comp, "", time.Now())
	if r.OK {
		t.Fatalf("编译器缺失时不得返回 OK: %+v", r)
	}
	if r.Error == "" {
		t.Error("失败必须带错误信息")
	}
}

// TestForgeGateFile_PythonBehaviorChain 文件式 gate 的真实行为链
// (真写临时文件 → 真 py_compile → 真执行)。
//
// 为什么必须真跑: 方法存在性哨兵查不出 Check/Exec 任一段静默失效。
// 本测试钉住三种真实结果形态 —— 形态本身就是契约:
//
//	① 成功        → OK + Stage=done + ExitCode=0 + 代码度量非零
//	② 语法错      → 编译阶段拦截(Stage=compile) + 结构化诊断含 SyntaxError
//	③ 报错有输出  → 设计契约「Runtime errors with output are results」:
//	   OK=true 且 ExitCode≠0 且 Error 非空 —— 三者并存, 缺一即歧义。
//	④ 报错无输出  → 无证据即不得判为结果(OK=false), 但退出码仍须透出。
func TestForgeGateFile_PythonBehaviorChain(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实编译执行")
	}
	f := newTestForge(t)
	defer f.Shutdown()

	t.Run("成功", func(t *testing.T) {
		r := f.forgeGate("print('FILE_GATE_OK')", "python", "")
		if !r.OK {
			t.Fatalf("python 成功用例失败: stage=%s err=%s", r.Stage, r.Error)
		}
		if r.Stage != "done" {
			t.Errorf("Stage = %q, want done", r.Stage)
		}
		if !strings.Contains(r.Stdout, "FILE_GATE_OK") {
			t.Errorf("Stdout = %q", r.Stdout)
		}
		if r.ExitCode != 0 {
			t.Errorf("ExitCode = %d, want 0", r.ExitCode)
		}
		if r.CodeLines < 1 || r.CodeSize == 0 {
			t.Errorf("代码度量缺失: lines=%d size=%d", r.CodeLines, r.CodeSize)
		}
	})

	t.Run("语法错_编译阶段拦截", func(t *testing.T) {
		r := f.forgeGate("def broken(:\n    pass\n", "python", "")
		if r.OK {
			t.Fatal("语法错不得返回 OK")
		}
		if r.Stage != "compile" {
			t.Errorf("Stage = %q, want compile", r.Stage)
		}
		if !strings.Contains(r.Error, "syntax check failed") {
			t.Errorf("Error = %q", r.Error)
		}
		if !strings.Contains(r.Diagnostics, "SyntaxError") {
			t.Errorf("Diagnostics 应含 SyntaxError, 实际 %q", r.Diagnostics)
		}
	})

	t.Run("报错有输出_是结果非故障", func(t *testing.T) {
		r := f.forgeGate("import sys\nprint('BEFORE_EXIT')\nsys.exit(7)\n", "python", "")
		if !r.OK {
			t.Fatalf("有输出的运行时报错应视为结果, 实际 OK=false: %s", r.Error)
		}
		if r.ExitCode != 7 {
			t.Errorf("ExitCode = %d, want 7 (退出码必须原样透出)", r.ExitCode)
		}
		if !strings.Contains(r.Error, "exit status 7") {
			t.Errorf("Error 应含 exit status 7, 实际 %q", r.Error)
		}
		if !strings.Contains(r.Stdout, "BEFORE_EXIT") {
			t.Errorf("Stdout = %q", r.Stdout)
		}
	})

	t.Run("报错无输出_非结果", func(t *testing.T) {
		r := f.forgeGate("import sys\nsys.exit(3)\n", "python", "")
		if r.OK {
			t.Error("无输出的非零退出不得判为结果 (无证据)")
		}
		if r.ExitCode != 3 {
			t.Errorf("ExitCode = %d, want 3", r.ExitCode)
		}
	})
}
