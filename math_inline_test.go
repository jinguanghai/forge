package main

// math_inline_test.go — math 内联快速路径契约测试 (P0, 20261001)
//
// 分两层:
//   A. 单元层: 内联求值结果 vs sympy 实测固化值 (快, 覆盖全部语义锚点)
//   B. A/B 层: 内联输出 vs 真 spawn math_gate.exe 输出, 逐字节比对 (慢, 防语义漂移)
//
// A/B 层是核心防线: 内联的唯一正当性来自「与 sympy 逐字节一致」。
// 表内期望值全部来自实测 (20261001 会话对 math_gate.exe 的批量调用), 非推测。

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mathInlineTestForge 构造带真实 toolsDir 的 Forge —— 内联的前置条件是死边界二进制
// 就位 (见 mathInlineBinaryPresent), 空 toolsDir 会走「二进制缺失」分支而禁用内联。
func mathInlineTestForge(t *testing.T) *Forge {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return &Forge{toolsDir: filepath.Join(wd, ForgeToolsDir)}
}

// mathInlineAB 内联子集的语义锚点表。result/latex 为 sympy 实测输出。
var mathInlineAB = []struct{ expr, result, latex string }{
	{"2+2", "4", "4"},
	{"3*7", "21", "21"},
	{"10/4", "5/2", `\frac{5}{2}`},
	{"1/3", "1/3", `\frac{1}{3}`},
	{"(2/3)^3", "8/27", `\frac{8}{27}`},
	{"2^10", "1024", "1024"},
	{"2**10", "1024", "1024"},
	{"100-37", "63", "63"},
	{"-5+3", "-2", "-2"},
	{"5-8", "-3", "-3"},
	{"(1+2)*3", "9", "9"},
	{"2^0", "1", "1"},
	{"10/5", "2", "2"},
	{"7/2+1/3", "23/6", `\frac{23}{6}`},
	{"2^3^2", "512", "512"},
	{"1+2*3", "7", "7"},
	{"6/4", "3/2", `\frac{3}{2}`},
	{"-2^2", "-4", "-4"},
	{"(-2)^2", "4", "4"},
	{"1-1", "0", "0"},
	{"0*5", "0", "0"},
	{"1000000*1000000", "1000000000000", "1000000000000"},
	{"2^63", "9223372036854775808", "9223372036854775808"},
	{"10^20", "100000000000000000000", "100000000000000000000"},
	{"3/1", "3", "3"},
	{"8/4+2", "4", "4"},
	{"1/2+1/2", "1", "1"},
	{"-1/3", "-1/3", `- \frac{1}{3}`},
	{"-5/2", "-5/2", `- \frac{5}{2}`},
	{"0/5", "0", "0"},
	{"0^0", "1", "1"},
	{"2^-1", "1/2", `\frac{1}{2}`},
	{"1/3+1/6", "1/2", `\frac{1}{2}`},
	{"2%5", "2", "2"},
	{"1/-2", "-1/2", `- \frac{1}{2}`},
	{"(2^3)^2", "64", "64"},
	{"+3", "3", "3"},
	{"0%3", "0", "0"},
	{"2^100", "1267650600228229401496703205376", "1267650600228229401496703205376"},
	{"99999999999999999999+1", "100000000000000000000", "100000000000000000000"},
	{"3/4*4/3", "1", "1"},
	{"(1/3)/(1/3)", "1", "1"},
	{"1 - 1", "0", "0"},
	{"2 ^ 10", "1024", "1024"},
	{"2*3+4*5", "26", "26"},
	{"1/(1/3)", "3", "3"},
	{"10/3", "10/3", `\frac{10}{3}`},
	{"-10/4", "-5/2", `- \frac{5}{2}`},
	{"2^-2", "1/4", `\frac{1}{4}`},
	{"1/3-1/3", "0", "0"},
	{"1000000007*1000000009", "1000000016000000063", "1000000016000000063"},
	{"7/3/2", "7/6", `\frac{7}{6}`},
	{"2*2^3", "16", "16"},
	{"(1+1)^(1+1)", "4", "4"},
	{"3^3^3", "7625597484987", "7625597484987"},
	{"-7%3", "2", "2"},
	{"7%-3", "-2", "-2"},
	{"(-7)%3", "2", "2"},
	{"2 + 2", "4", "4"},
	{"2++3", "5", "5"},
	{"--3", "3", "3"},
	{"5", "5", "5"},
}

// mathInlineMustFallback 必须回退的输入 —— 每一条都有明确的回退理由。
var mathInlineMustFallback = []struct{ expr, why string }{
	{"0.1+0.2", "小数点: 浮点域精度语义归 sympy"},
	{"2.5*4", "小数点"},
	{"3^0.5", "小数点 + 非整数指数"},
	{"0.5/0.25", "小数点"},
	{"1e3", "科学计数法(字母)"},
	{"1/0", "除零: sympy 返回 zoo, 内联不下结论"},
	{"0^-1", "零的负指数: zoo"},
	{"5%0", "模零: sympy 报 error"},
	{"4^(1/2)", "指数非整数"},
	{"2^10001", "指数超界(防 DoS)"},
	{"2^-10001", "指数超界"},
	{"(2)(3)", "隐式乘法: 解析器只认显式算符"},
	{"2(3)", "隐式乘法"},
	{"2 3", "尾随垃圾(隐式乘法)"},
	{"sqrt(2)", "函数: 符号域"},
	{"x^2-4", "符号域"},
	{"hello world", "自然语言"},
	{"结果等于2", "中文"},
	{"1+1;2+2", "分号"},
	{"2***3", "畸形算符"},
	{"(1+2", "括号不配平"},
	{"1+2)", "尾随垃圾"},
	{"1_000", "下划线"},
	{"", "空输入"},
	{strings.Repeat("1+", 300) + "1", "超长(>400 字符)"},
}

// TestMathInlineMatchesSympyAnchors 单元层: 内联结果必须与 sympy 实测锚点逐字节一致。
func TestMathInlineMatchesSympyAnchors(t *testing.T) {
	for _, c := range mathInlineAB {
		gotR, gotL, ok := mathInlineEval(c.expr)
		if !ok {
			t.Fatalf("应内联却被拒: %q", c.expr)
		}
		if gotR != c.result || gotL != c.latex {
			t.Fatalf("与 sympy 锚点不符: %q -> (%q, %q), 期望 (%q, %q)",
				c.expr, gotR, gotL, c.result, c.latex)
		}
	}
}

// TestMathInlineMustFallback 回退闸: 不在子集内的一律 ok=false, 由 spawn 兜底。
func TestMathInlineMustFallback(t *testing.T) {
	for _, c := range mathInlineMustFallback {
		if r, l, ok := mathInlineEval(c.expr); ok {
			t.Fatalf("必须回退却内联了 (%s): %q -> (%q, %q)", c.why, c.expr, r, l)
		}
	}
}

// mathGateRaw 直接跑 gate 拿原始 stdout 字符串 (逐字节比对用)。
func mathGateRaw(t *testing.T, req map[string]string) (int, string) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(mathGatePath(t), string(b))
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("运行 gate 失败: %v", err)
		}
	}
	return code, so.String()
}

// TestMathInlineABAgainstSympyGate A/B 层: 内联输出与真 spawn 输出逐字节相等。
// 这是内联的正当性来源 —— 一旦 sympy 侧语义变化(升级/改动 gate), 本测试立即红。
func TestMathInlineABAgainstSympyGate(t *testing.T) {
	// 代表性子集: 覆盖整数/分数/负分数/幂右结合/一元负号/取模/大整数/空格形态
	pick := []string{"2+2", "10/4", "(2/3)^3", "2^3^2", "-2^2", "-1/3",
		"-7%3", "7%-3", "2^100", "0^0", "1/3+1/6", "10/3", "2**10", "5"}
	f := mathInlineTestForge(t)
	for _, expr := range pick {
		code, raw := mathGateRaw(t, map[string]string{"expr": expr})
		if code != 0 {
			t.Fatalf("gate 非零退出: %q -> %d %s", expr, code, raw)
		}
		res, ok := f.mathInlineGate(expr, time.Now())
		if !ok {
			t.Fatalf("应内联却被拒: %q", expr)
		}
		if res.Stdout != raw {
			t.Fatalf("内联与 sympy 输出不一致 (逐字节):\n  expr   = %q\n  inline = %q\n  sympy  = %q",
				expr, res.Stdout, raw)
		}
	}
}

// TestMathInlineGateJSONInput JSON 形态输入: simplify 走内联, 其余 action 回退。
func TestMathInlineGateJSONInput(t *testing.T) {
	f := mathInlineTestForge(t)
	cases := []struct {
		code   string
		inline bool
		result string
	}{
		{`{"expr":"2+2"}`, true, "4"},
		{`{"expr":"1/3+2/5","action":"simplify"}`, true, "11/15"},
		{`{"expr":"x^2-4","action":"solve"}`, false, ""},
		{`{"expr":"x^3","action":"diff"}`, false, ""},
		{`{"expr":"1+1","action":"evaluate"}`, false, ""},
		{`{"expr":"2+2","action":"equals","expected":"4"}`, false, ""},
		{`{"expr":"sqrt(2)","action":"simplify"}`, false, ""},
	}
	for _, c := range cases {
		res, ok := f.mathInlineGate(c.code, time.Now())
		if ok != c.inline {
			t.Fatalf("内联判定不符: %s -> inline=%v, 期望 %v", c.code, ok, c.inline)
		}
		if ok && !strings.Contains(res.Stdout, `"result":"`+c.result+`"`) {
			t.Fatalf("内联结果不符: %s -> %s", c.code, res.Stdout)
		}
	}
}

// TestMathInlineOutputShape 输出契约: 与 math_gate emitJSON 同形(字段序/无 HTML 转义/尾随换行)。
func TestMathInlineOutputShape(t *testing.T) {
	f := mathInlineTestForge(t)
	res, ok := f.mathInlineGate("10/4", time.Now())
	if !ok {
		t.Fatal("应内联")
	}
	want := "{\"ok\":true,\"result\":\"5/2\",\"latex\":\"\\\\frac{5}{2}\",\"validated\":true}\n"
	if res.Stdout != want {
		t.Fatalf("输出形态不符:\n  got  = %q\n  want = %q", res.Stdout, want)
	}
	if res.Lang != "math" || res.Stage != "done" || !res.OK {
		t.Fatalf("结果元数据不符: %+v", res)
	}
}

// TestMathInlineSwitchOff 开关: FORGE_MATH_INLINE=0 时全量回退。
func TestMathInlineSwitchOff(t *testing.T) {
	t.Setenv("FORGE_MATH_INLINE", "0")
	f := mathInlineTestForge(t)
	if _, ok := f.mathInlineGate("2+2", time.Now()); ok {
		t.Fatal("开关关闭时不得内联")
	}
	t.Setenv("FORGE_MATH_INLINE", "1")
	if _, ok := f.mathInlineGate("2+2", time.Now()); !ok {
		t.Fatal("开关开启时应内联")
	}
}

// TestMathInlinePureFunction 确定性: 同输入多次求值结果恒等 (死程序判定的前提)。
func TestMathInlinePureFunction(t *testing.T) {
	const expr = "1/3+1/6-2/9*3"
	first, firstL, ok := mathInlineEval(expr)
	if !ok {
		t.Fatal("应内联")
	}
	for i := 0; i < 200; i++ {
		r, l, ok := mathInlineEval(expr)
		if !ok || r != first || l != firstL {
			t.Fatalf("非确定性: 第 %d 次 (%q,%q) != (%q,%q)", i, r, l, first, firstL)
		}
	}
}

// TestMathInlineDisabledWhenBinaryMissing 新契约: 死边界二进制缺失时内联必须禁用。
// 理由: 内联是「spawn 的等价替代」而非「绕过死边界」—— 二进制缺失仍判成功会把
// 环境故障 (gate 被删/路径漂移) 静默掩盖, 与 cov_gates_deadboundary_test.go 的
// 哨兵语义直接冲突 (实测: 该哨兵在接入内联后立刻报红, 本契约即由此而来)。
func TestMathInlineDisabledWhenBinaryMissing(t *testing.T) {
	f := &Forge{toolsDir: filepath.Join(t.TempDir(), "no_such_tools")}
	if _, ok := f.mathInlineGate("2+2", time.Now()); ok {
		t.Fatal("死边界二进制缺失时内联必须禁用, 否则环境故障被静默掩盖")
	}
}
