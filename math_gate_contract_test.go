package main

// math_gate_contract_test.go — math gate 输入契约回归测试 (20260927 批次A 控制阶段)
//
// 背景 (全部为实测, 非推测):
//  1. fail-open: 旧版仅校验 expr=="", 对 "hello world" 返回
//     {"ok":true,"result":"d*e*h*l**3*o**2*r*w"} —— implicit_multiplication_application
//     把自然语言切碎成单字符符号乘积, 与 gate 首行契约 "There is no third option" 矛盾。
//     该 gate 近 4000 条审计里只用过 23 次, 低使用率掩盖了高严重度缺陷。
//  2. 代码注入: 旧版用 fmt.Sprintf 把 expr/action/expected 拼进 Python 源码,
//     action 字段零转义无白名单。实测 payload
//     'simplify";open(r"...","w").write("pwned");z="' 成功写文件,
//     且 gate 仍返回 {"ok":true,"result":"2"} 退出码 0 —— 注入完全静默。
//  3. 死字段: sympy_raw 恒空(Python 从不输出), latex 白算(算完被 Go 结构体丢弃)。
//
// 本文件把 1/2 钉死为契约, 并保护 3 的修复不被回退。

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// mathGatePath 定位 gate 二进制。缺失即 Fatal —— 「判定缺席不可当成功」
// (与 mustHaveOutputGates 同一原则): gate 被误删/改名必须立刻红, 不能 skip 成绿。
func mathGatePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	exe := "math_gate.exe"
	if runtime.GOOS != "windows" {
		exe = "math_gate"
	}
	p := filepath.Join(wd, ForgeToolsDir, exe)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("math gate 未就位, 死边界不可用: %v", err)
	}
	return p
}

type mathGateOut struct {
	OK        bool   `json:"ok"`
	Result    string `json:"result"`
	Latex     string `json:"latex"`
	Rejected  bool   `json:"rejected"`
	Reason    string `json:"reason"`
	Error     string `json:"error"`
	Validated bool   `json:"validated"`
}

func runMathGate(t *testing.T, req map[string]string) (int, mathGateOut, string) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(mathGatePath(t), string(b))
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var ee *exec.ExitError
		if !errorsAs(runErr, &ee) {
			t.Fatalf("运行 gate 失败: %v", runErr)
		}
		code = ee.ExitCode()
	}
	var out mathGateOut
	if e := json.Unmarshal(so.Bytes(), &out); e != nil {
		t.Fatalf("gate 输出不是 JSON: %q", so.String())
	}
	return code, out, se.String()
}

// errorsAs 是 errors.As 的薄封装, 保持本文件 import 面最小。
func errorsAs(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// TestMathGateRejectsNaturalLanguage 钉死 fail-open 缺陷。
func TestMathGateRejectsNaturalLanguage(t *testing.T) {
	cases := []struct{ name, expr string }{
		{"英文自然语言", "hello world"},
		{"英文单词", "hello"},
		{"中文自然语言", "结果等于2"},
		{"中文乘式", "价格*数量"},
		{"中文短句", "完全不是算式"},
		{"反斜杠", `x\`},
		{"双引号", `a"b`},
		{"单引号", `a'b`},
		{"dunder 逃逸", `__import__(1)`},
		{"分号语句", "1+1;2+2"},
		{"反引号", "1+1`"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, out, _ := runMathGate(t, map[string]string{"expr": c.expr})
			if code == 0 {
				t.Fatalf("非法输入必须非零退出: expr=%q", c.expr)
			}
			if out.OK {
				t.Fatalf("非法输入不得判 ok:true (fail-open): expr=%q out=%+v", c.expr, out)
			}
			if !out.Rejected || out.Reason == "" {
				t.Fatalf("拒绝必须带 rejected:true + reason: expr=%q out=%+v", c.expr, out)
			}
			if !out.Validated {
				t.Fatalf("拒绝也必须带 validated:true (审计探针字段): %+v", out)
			}
		})
	}
}

// TestMathGateNoCodeInjection 钉死注入缺陷: action 与 expr 两条面都必须被拒,
// 且 payload 绝不可被执行(以 marker 文件是否存在为客观证据)。
func TestMathGateNoCodeInjection(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "pwned.txt")
	marker2 := filepath.Join(dir, "pwned2.txt")

	actionPayloads := []string{
		`simplify";open(r"` + marker + `","w").write("pwned");z="`,
		`simplify";print("X");z="`,
		`;import os`,
		`simplify
open(r"` + marker2 + `","w").write("x")`,
		`SIMPLIFY`,
		`simplify `,
	}
	for _, p := range actionPayloads {
		code, out, _ := runMathGate(t, map[string]string{"expr": "1+1", "action": p})
		if code == 0 || out.OK {
			t.Fatalf("action 注入/非白名单必须被拒: %q -> exit=%d out=%+v", p, code, out)
		}
	}

	// expr 侧注入面: 引号 / 反斜杠 / 下划线 / 分号 均不得进入 Python 源码。
	exprPayloads := []string{
		`");open(r"` + marker + `","w").write("pwned");("#`,
		`__import__("os")`,
		`1+1\`,
		`1+1";print(1);"`,
	}
	for _, p := range exprPayloads {
		code, out, _ := runMathGate(t, map[string]string{"expr": p})
		if code == 0 || out.OK {
			t.Fatalf("expr 注入面必须被拒: %q -> exit=%d out=%+v", p, code, out)
		}
	}

	for _, m := range []string{marker, marker2} {
		if _, err := os.Stat(m); err == nil {
			t.Fatalf("注入 payload 被执行了: %s 被创建", m)
		}
	}
}

// TestMathGateAcceptsValidExpressions 保证拒绝权没有误伤正常算式,
// 并保护两处功能回归: ^ 归一化为 ** (旧版在 Go 侧做过, v2 一度漏掉 → 2^10 算成 8),
// 以及 latex 落地(旧版 Python 算完被 Go 丢弃 = 白算)。
func TestMathGateAcceptsValidExpressions(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 math gate 真实子进程端到端 (13 个 case 各起一次 sympy)")
	}
	cases := []struct{ expr, want, action string }{
		{"1+1", "2", ""},
		{"2^10", "1024", ""},
		{"2**10", "1024", ""},
		{"3*(x+1)-3*x", "3", ""},
		{"1/3+2/5", "11/15", ""},
		{"sqrt(16)", "4", ""},
		{"sin(pi/2)", "1", ""},
		{"(7-2)*6", "30", ""},
		{"x^2-4", "[-2, 2]", "solve"},
		{"x^3", "3*x**2", "diff"},
		{"x^2-1", "(x - 1)*(x + 1)", "factor"},
		// 求值型表达式: parse 阶段就返回 list 的形态, 必须原样输出而非进符号变换
		// (旧版 simplify(list) → AttributeError: 'list' object has no attribute 'replace')
		{"solve(x^2-4, x)", "[-2, 2]", ""},
		{"solve(x^2-9, x)", "[-3, 3]", ""},
	}
	for _, c := range cases {
		req := map[string]string{"expr": c.expr}
		if c.action != "" {
			req["action"] = c.action
		}
		code, out, stderr := runMathGate(t, req)
		if code != 0 || !out.OK {
			t.Fatalf("合法算式被误伤: expr=%q action=%q exit=%d out=%+v stderr=%s",
				c.expr, c.action, code, out, stderr)
		}
		if !strings.Contains(out.Result, c.want) {
			t.Errorf("expr=%q 结果错: got %q want 含 %q", c.expr, out.Result, c.want)
		}
		if out.Latex == "" {
			t.Errorf("latex 未落地(白算回归): expr=%q out=%+v", c.expr, out)
		}
		if !out.Validated {
			t.Errorf("成功结果必须带 validated:true: %+v", out)
		}
	}
}

// TestParseGateReject 是 parseGateReject 的单元测试:
// 必须结构化判定, 不得退化成「文本里出现 rejected 就算」。
func TestParseGateReject(t *testing.T) {
	cases := []struct {
		name, in   string
		wantOK     bool
		wantReason string
	}{
		{"结构化拒绝", `{"ok":false,"rejected":true,"reason":"expr 非法"}`, true, "expr 非法"},
		{"拒绝无 reason 回退 error", `{"rejected":true,"error":"fallback"}`, true, "fallback"},
		{"正常结果", `{"ok":true,"result":"2"}`, false, ""},
		{"非 JSON", `not json`, false, ""},
		{"空", ``, false, ""},
		{"数组", `[1,2]`, false, ""},
		// 反例: 文本里出现 rejected 字样但没有结构化字段 → 不算拒绝(防文本嗅探)
		{"文本嗅探反例", `{"ok":false,"error":"rejected by peer"}`, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := parseGateReject(c.in)
			if got != c.wantOK {
				t.Fatalf("parseGateReject(%q) = %v, want %v", c.in, got, c.wantOK)
			}
			if c.wantOK && reason != c.wantReason {
				t.Fatalf("reason = %q, want %q", reason, c.wantReason)
			}
		})
	}
}

// TestAuditGateRecordsRejection 钉死 A3 埋点:
// 主动拒绝必须在审计里单独可数, 否则会被 IFR-1「失败率」统计吞掉,
// 「输入被拒」与「闸门坏了」两种信号无法区分。
func TestAuditGateRecordsRejection(t *testing.T) {
	t.Setenv("FORGE_GATE_AUDIT", "1")
	dir := t.TempDir()
	f := &Forge{workDir: dir}

	f.auditGate("math", false, false, time.Now(), 0, 5, 0, "", &ForgeGateResult{
		OK: false, Lang: "math", Stage: "execute",
		GateRejected: true, RejectReason: "expr 含非算式字符 '这'",
	})
	// 反例: 普通执行失败不得带 gate_rejected 字段
	f.auditGate("python", false, false, time.Now(), 0, 5, 0, "", &ForgeGateResult{
		OK: false, Lang: "python", Stage: "execute", Error: "boom",
	})

	raw, err := os.ReadFile(filepath.Join(dir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("审计行数 = %d, want 2: %q", len(lines), string(raw))
	}
	var rej map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &rej); err != nil {
		t.Fatal(err)
	}
	if rej["gate_rejected"] != true {
		t.Errorf("拒绝记录缺 gate_rejected=true: %v", rej)
	}
	if rej["reject_reason"] == nil || rej["reject_reason"] == "" {
		t.Errorf("拒绝记录缺 reject_reason: %v", rej)
	}
	var normal map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &normal); err != nil {
		t.Fatal(err)
	}
	if _, has := normal["gate_rejected"]; has {
		t.Errorf("普通失败不得带 gate_rejected: %v", normal)
	}
}
