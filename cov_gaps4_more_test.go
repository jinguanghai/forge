package main

// cov_gaps4_more_test.go — 覆盖率缺口补测 第四批 (跨平台, 20261001)
//
// 覆盖 gate 内联执行核心 forgeGateInline 的参数装配与三条返回路径:
// 无 exec 配置 / 可执行缺失 / 正常执行 / {code}·{forge}·"-"·input 四种装配 / 超时。
// python 缺失时相关用例自动 SKIP (不静默假通过)。

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func covGap4Forge(t *testing.T) *Forge {
	t.Helper()
	return NewForge(t.TempDir(), &Config{})
}

// 空 Exec 的真实行为: execArgs 会被 append(code) 填充成 [code] → 非空 →
// 走进程路径并以"可执行不存在"失败。
//
// 注: forgeGateInline 内 `len(execArgs) == 0 → "no exec command configured"`
// 是**不可达兜底分支** —— 上面的 append(code) 已保证 execArgs 非空。保留它是为了
// 未来若有"既无 {code} 也无 {forge}/- 且不 append"的装配改动时仍有防御; 本用例
// 钉住的是真实可达行为 (不 panic、判失败), 不是那条死分支。
func TestCovGap4_GateInline_EmptyExecFallsBackToCode(t *testing.T) {
	f := covGap4Forge(t)
	res := f.forgeGateInline("code", "testlang", CompilerDef{}, "", time.Now())
	if res.OK {
		t.Fatal("空 Exec 不得判成功")
	}
	if res.Stage != "execute" {
		t.Fatalf("stage 应为 execute, 实际 %q", res.Stage)
	}
	if !strings.Contains(res.Error, "execution failed") {
		t.Fatalf("错误信息不符: %q", res.Error)
	}
	if res.Lang != "testlang" {
		t.Fatalf("lang 未回填: %q", res.Lang)
	}
}

// 可执行不存在: 走 execute 失败路径, 必须带上 stderr/err 文本与退出码字段。
func TestCovGap4_GateInline_ExecMissing(t *testing.T) {
	f := covGap4Forge(t)
	res := f.forgeGateInline("x", "testlang", CompilerDef{Exec: []string{"forge_no_such_exe_zzz"}}, "", time.Now())
	if res.OK {
		t.Fatal("可执行不存在不得判成功")
	}
	if res.Stage != "execute" {
		t.Fatalf("stage 应为 execute, 实际 %q", res.Stage)
	}
	if res.Error == "" {
		t.Fatal("失败必须带错误文本")
	}
	if res.Timeout {
		t.Fatalf("缺失可执行不是超时: %+v", res)
	}
}

func TestCovGap4_GateInline_PythonPaths(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("PATH 无 python, 跳过真实执行路径 (不静默假通过)")
	}
	f := covGap4Forge(t)

	t.Run("code占位符", func(t *testing.T) {
		cd := CompilerDef{Exec: []string{py, "-c", "import sys;print(sys.argv[1])", "{code}"}}
		res := f.forgeGateInline("HELLO-CODE", "testlang", cd, "", time.Now())
		if !res.OK {
			t.Fatalf("应执行成功: %+v", res)
		}
		if !strings.Contains(res.Stdout, "HELLO-CODE") {
			t.Fatalf("{code} 未被替换: %q", res.Stdout)
		}
		if res.Stage != "done" || res.ExitCode != 0 {
			t.Fatalf("成功态字段不符: %+v", res)
		}
	})

	t.Run("forge占位符", func(t *testing.T) {
		cd := CompilerDef{Exec: []string{py, "-c", "print(r'{forge}')"}}
		res := f.forgeGateInline("", "testlang", cd, "", time.Now())
		if !res.OK {
			t.Fatalf("应执行成功: %+v", res)
		}
		if !strings.Contains(res.Stdout, f.workDir) {
			t.Fatalf("{forge} 未替换为工作目录 %q: %q", f.workDir, res.Stdout)
		}
	})

	t.Run("stdin短横线", func(t *testing.T) {
		cd := CompilerDef{Exec: []string{py, "-"}}
		res := f.forgeGateInline("print('STDIN-OK')", "testlang", cd, "", time.Now())
		if !res.OK {
			t.Fatalf("应执行成功: %+v", res)
		}
		if !strings.Contains(res.Stdout, "STDIN-OK") {
			t.Fatalf("代码未经 stdin 送达: %q", res.Stdout)
		}
	})

	t.Run("input环境变量", func(t *testing.T) {
		cd := CompilerDef{Exec: []string{py, "-c", "import os;print(os.environ.get('" + ForgeInputEnv + "',''))"}}
		res := f.forgeGateInline("", "testlang", cd, "IN-DATA-42", time.Now())
		if !res.OK {
			t.Fatalf("应执行成功: %+v", res)
		}
		if !strings.Contains(res.Stdout, "IN-DATA-42") {
			t.Fatalf("input 未经 %s 传入: %q", ForgeInputEnv, res.Stdout)
		}
	})

	t.Run("超时", func(t *testing.T) {
		cd := CompilerDef{
			Exec:        []string{py, "-c", "import time;time.sleep(10)"},
			ExecTimeout: 400 * time.Millisecond,
		}
		res := f.forgeGateInline("", "testlang", cd, "", time.Now())
		if res.OK {
			t.Fatal("超时必须判失败 (部分输出≠成功)")
		}
		if !res.Timeout {
			t.Fatalf("Timeout 标志未置位: %+v", res)
		}
		if res.Diagnostics == "" {
			t.Fatal("超时必须给诊断文本 (否则模型只能盲猜重写)")
		}
		if !strings.Contains(res.Diagnostics, "超时") {
			t.Fatalf("诊断文本应说明超时性质: %q", res.Diagnostics)
		}
	})
}
