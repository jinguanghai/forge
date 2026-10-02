package main

// forge_self_test.go — self gate 核心(错误构造/超时/截断)的哨兵。
//
// 注: selfApplyReplace / selfApplyAppend / locateSelfSource 的用例在
// self_deploy_test.go 中(前缀 TestSelfApply* / TestLocateSelfSource*), 此处不重复。

import (
	"strings"
	"testing"
	"time"
)

func TestForgeSelf_SelfGateErr(t *testing.T) {
	r := selfGateErr(time.Now(), "boom %d", 1)
	if r.OK {
		t.Error("selfGateErr 必须返回 OK=false")
	}
	if r.Lang != "self" || r.Stage != "compile" {
		t.Errorf("Lang/Stage = %q/%q, 期望 self/compile", r.Lang, r.Stage)
	}
	if r.Error != "boom 1" {
		t.Errorf("Error = %q, 期望 \"boom 1\"", r.Error)
	}
	// ExitCode=-1 是"未真正执行"的标记, 与"执行失败(非0)"区分
	if r.ExitCode != -1 {
		t.Errorf("ExitCode = %d, 期望 -1", r.ExitCode)
	}
}

func TestForgeSelf_CompilerTimeout(t *testing.T) {
	d := compilerTimeout("go")
	if d <= 0 {
		t.Errorf("compilerTimeout(\"go\") = %v, 期望 >0", d)
	}
	if got := compilerTimeout("go"); got != 30*time.Second {
		t.Errorf("compilerTimeout(\"go\") = %v, 期望 30s", got)
	}
	// 未知语言必须有兜底超时(0 会让 ctx 立刻超时)
	if got := compilerTimeout("unknownlang"); got <= 0 {
		t.Errorf("未知语言超时 = %v, 期望 >0", got)
	}
}

func TestForgeSelf_TruncateOutput(t *testing.T) {
	if got := truncateOutput("abc"); got != "abc" {
		t.Errorf("短输出不应被截断, 得到 %q", got)
	}
	long := strings.Repeat("x", 20000)
	got := truncateOutput(long)
	if len(got) >= len(long) {
		t.Errorf("长输出未截断: %d >= %d", len(got), len(long))
	}
}
