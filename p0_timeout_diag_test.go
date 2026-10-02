package main

// p0_timeout_diag_test.go — P0(20260930) 超时可观测性改造的哨兵。
//
// 背景(六西格玛实测): 近 14 天 gate 缺陷 116 条中 90 条(77.6%)是超时, 且反馈
// 只剩一句 "context deadline exceeded" —— 根因是管道下 python 默认块缓冲, 超时时
// stdout 一个字都拿不到, 模型看不到卡在哪, 只能盲猜重写。
//
// 本文件钉住三件事:
//  ① python 执行必须带 -u (值级断言, 防退化 —— 删掉 -u 即报红)
//  ② 超时一律 OK=false(即使有部分输出), 且诊断走 Diagnostics 不被渲染层吞掉
//  ③ 超时前的部分输出必须真实可见(端到端真跑)

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestP0_PythonExecUnbuffered 值级哨兵: python gate 的 Exec 必须含 -u。
// 只断言语义(有 -u)而非整条命令 —— 允许将来插入其他参数。
func TestP0_PythonExecUnbuffered(t *testing.T) {
	comp, ok := 铸剑炉_COMPILERS["python"]
	if !ok {
		t.Fatal("铸剑炉_COMPILERS 缺 python")
	}
	if len(comp.Exec) == 0 || comp.Exec[0] != "python" {
		t.Fatalf("python Exec 首元素 = %v, want python", comp.Exec)
	}
	found := false
	for _, a := range comp.Exec[1:] {
		if a == "-u" {
			found = true
		}
	}
	if !found {
		t.Errorf("python Exec 缺 -u (超时时将拿不到部分输出, 反馈退化为 context deadline exceeded): %v", comp.Exec)
	}
}

// TestP0_SelfHostedPythonUnbuffered selfHosted gate(script 型, 如 browser/media)
// 走的是 forge_gate_host.go 里的 python 调用 —— 它不在编译器表里, 只能扫源码钉住。
func TestP0_SelfHostedPythonUnbuffered(t *testing.T) {
	b, err := os.ReadFile("forge_gate_host.go")
	if err != nil {
		t.Fatalf("读 forge_gate_host.go: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, `f.newCmd(ctx, "python", "-u", absPath, code)`) {
		t.Error("forge_gate_host.go 的 script 型 gate 未带 -u (超时可观测性退化)")
	}
}

func TestP0_TimeoutDiagContent(t *testing.T) {
	empty := timeoutDiag("python", 30*time.Second, "", "")
	for _, want := range []string{"python", "30s", "预算烧完", "卡死"} {
		if !strings.Contains(empty, want) {
			t.Errorf("空输出诊断缺 %q: %s", want, empty)
		}
	}
	withOut := timeoutDiag("python", 30*time.Second, "STEP1\nSTEP2\n", "")
	for _, want := range []string{"12 字节", "不代表任务完成", "拆小分批"} {
		if !strings.Contains(withOut, want) {
			t.Errorf("有输出诊断缺 %q: %s", want, withOut)
		}
	}
	if strings.Contains(withOut, "卡死") {
		t.Errorf("有部分输出时不应断言「卡死在首次输出之前」: %s", withOut)
	}
}

// TestP0_TimeoutNotOK_RealRun 端到端: 真跑一个会超时的 python 脚本。
// 钉住「-u 让部分输出可见」+「超时即使有输出也判失败」两条契约。
func TestP0_TimeoutNotOK_RealRun(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实超时执行")
	}
	f := &Forge{workDir: t.TempDir(), ctx: context.Background()}
	comp := 铸剑炉_COMPILERS["python"]
	comp.ExecTimeout = 1200 * time.Millisecond // 只改执行预算, 其余保持真实配置
	code := "import time\nprint('STEP1_OK', flush=True)\ntime.sleep(30)\nprint('NEVER')\n"
	r := f.forgeGateFile(code, "python", comp, "", time.Now())

	if r.OK {
		t.Fatalf("超时必须判失败(旧实现把「超时+有输出」判成 OK=true): %+v", r)
	}
	if !r.Timeout {
		t.Errorf("Timeout 标记缺失: %+v", r)
	}
	if !strings.Contains(r.Stdout, "STEP1_OK") {
		t.Errorf("超时前的部分输出必须可见(-u 生效), Stdout=%q", r.Stdout)
	}
	if !strings.Contains(r.Diagnostics, "预算烧完") {
		t.Errorf("超时诊断缺失, Diagnostics=%q", r.Diagnostics)
	}
	if strings.Contains(r.Stdout, "NEVER") {
		t.Errorf("脚本未跑完却出现尾部输出: %q", r.Stdout)
	}
	// 渲染层: 头部必须说「失败」, 且诊断不能被「Stderr 非空则丢弃 Error」规则吞掉
	rendered := f.formatResult(r)
	if !strings.Contains(rendered, "失败") {
		t.Errorf("渲染层未标注失败:\n%s", rendered)
	}
	if !strings.Contains(rendered, "预算烧完") {
		t.Errorf("渲染层丢失诊断:\n%s", rendered)
	}
}

// TestP0_DiagnosticsSurvivesStderr 渲染规则: Stderr 非空时 Error 会被丢弃,
// 诊断必须走 Diagnostics 才不会被吞掉(P0 改动正是把超时诊断放这里)。
func TestP0_DiagnosticsSurvivesStderr(t *testing.T) {
	out := (&Forge{}).formatResult(ForgeGateResult{
		OK: false, Lang: "python", Stage: "execute",
		Error:       "python execution failed: context deadline exceeded",
		Stderr:      "some real stderr text",
		Diagnostics: "DIAG_MARKER_UNIQUE",
	})
	if !strings.Contains(out, "DIAG_MARKER_UNIQUE") {
		t.Errorf("Diagnostics 被渲染层吞掉:\n%s", out)
	}
}
