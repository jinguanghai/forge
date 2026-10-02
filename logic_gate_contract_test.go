package main

// logic_gate_contract_test.go — logic gate 输入契约回归测试 (20260927 批次B / B5)
//
// 背景 (实测):
//  1. 契约不匹配无指引: 输入 {"type":"sat","code":"(assert (= (+ 1 1) 2))"} 被原样
//     塞进 s.add((assert ...)) → "Z3 error: File 'C:\Users\...\Temp\logic_gate_2322463757.py',
//     line 5"。LLM 对「logic gate / z3 SAT」的默认先验是 SMT-LIB, 而本 gate 要的是
//     z3py 表达式 —— 契约不匹配必须显式拒绝并给指引, 不能让调用方去猜。
//  2. 实现细节泄漏: 错误信息里带着生成的临时脚本绝对路径(含主机用户名与随机目录名),
//     对调用方零价值, 却把内部实现与主机信息一起吐给模型。
//
// 与 math gate 共用同一拒绝契约(rejected/reason/validated), 主程序侧的
// parseGateReject 因此对两个 gate 通用 —— 契约统一是「拒绝可被类型化计数」的前提。

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func logicGatePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	exe := "logic_gate.exe"
	if runtime.GOOS != "windows" {
		exe = "logic_gate"
	}
	p := filepath.Join(wd, ForgeToolsDir, exe)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("logic gate 未就位, 死边界不可用: %v", err)
	}
	return p
}

type logicGateOut struct {
	OK        bool   `json:"ok"`
	Verdict   string `json:"verdict"`
	Model     string `json:"model"`
	Error     string `json:"error"`
	Rejected  bool   `json:"rejected"`
	Reason    string `json:"reason"`
	Validated bool   `json:"validated"`
}

func runLogicGate(t *testing.T, req map[string]string) (int, logicGateOut) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(logicGatePath(t), string(b))
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		ee, ok := runErr.(*exec.ExitError)
		if !ok {
			t.Fatalf("运行 gate 失败: %v (stderr=%s)", runErr, se.String())
		}
		code = ee.ExitCode()
	}
	var out logicGateOut
	if e := json.Unmarshal(so.Bytes(), &out); e != nil {
		t.Fatalf("gate 输出不是 JSON: %q", so.String())
	}
	return code, out
}

// TestLogicGateRejectsSMTLib 钉死「契约不匹配必须显式拒绝并给指引」。
func TestLogicGateRejectsSMTLib(t *testing.T) {
	cases := []string{
		"(assert (= (+ 1 1) 2))",
		"(declare-const x Int)",
		"(set-logic QF_LIA)",
		"(check-sat)",
	}
	for _, code := range cases {
		t.Run(code, func(t *testing.T) {
			exit, out := runLogicGate(t, map[string]string{"type": "sat", "code": code})
			if exit == 0 || out.OK {
				t.Fatalf("SMT-LIB 输入必须被拒绝: code=%q exit=%d out=%+v", code, exit, out)
			}
			if !out.Rejected || out.Reason == "" {
				t.Fatalf("拒绝必须带 rejected:true + reason: %+v", out)
			}
			// 指引必须包含可执行的正确写法, 而不是只说"错了"
			if !strings.Contains(out.Reason, "z3py") || !strings.Contains(out.Reason, "code") {
				t.Errorf("拒绝理由必须给出正确写法指引: %q", out.Reason)
			}
			if !out.Validated {
				t.Errorf("拒绝也必须带 validated:true: %+v", out)
			}
		})
	}
}

// TestLogicGateErrorHidesTempPath 钉死「错误信息不得泄漏主机路径」。
func TestLogicGateErrorHidesTempPath(t *testing.T) {
	exit, out := runLogicGate(t, map[string]string{"type": "sat", "code": "x +"})
	if exit == 0 {
		t.Fatalf("语法错误必须非零退出: %+v", out)
	}
	if out.Error == "" {
		t.Fatal("语法错误必须回传可诊断的错误信息")
	}
	for _, leak := range []string{"AppData", "Temp", "C:\\", "Users", "logic_gate_"} {
		if strings.Contains(out.Error, leak) {
			t.Errorf("错误信息泄漏实现细节 %q: %q", leak, out.Error)
		}
	}
	if !strings.Contains(out.Error, "<logic_gate.py>") {
		t.Errorf("应保留可定位的占位文件名: %q", out.Error)
	}
	// 错误本身仍必须可诊断(z3 的真实报错不能被吞掉)
	if !strings.Contains(out.Error, "SyntaxError") {
		t.Errorf("真实错误信息被吞: %q", out.Error)
	}
}

// TestLogicGateAcceptsZ3PyForms 保证拒绝权没有误伤正常形式。
func TestLogicGateAcceptsZ3PyForms(t *testing.T) {
	cases := []struct {
		name, typ, code, want string
	}{
		{"表达式 unsat", "sat", "x > 0, y > 0, x + y < 0", "unsat"},
		{"表达式 sat", "sat", "x > 0, y > 0, x + y > 0", "sat"},
		{"布尔表达式", "sat", "p | q, ~p", "sat"},
		{"证明", "prove", "claim = x + 1 > x", "proved"},
		{"等价", "equivalence", "a = x + y\nb = y + x", "equivalent"},
		{"type 省略默认 sat", "", "x > 0", "sat"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exit, out := runLogicGate(t, map[string]string{"type": c.typ, "code": c.code})
			if exit != 0 || !out.OK {
				t.Fatalf("正常形式被误伤: type=%q code=%q exit=%d out=%+v", c.typ, c.code, exit, out)
			}
			if out.Verdict != c.want {
				t.Errorf("verdict = %q, want %q", out.Verdict, c.want)
			}
			if !out.Validated {
				t.Errorf("成功结果必须带 validated:true: %+v", out)
			}
		})
	}
}

// TestLogicGateRejectsBadInput 钉死入口校验: 空 code / 未知 type。
func TestLogicGateRejectsBadInput(t *testing.T) {
	cases := []struct{ name, typ, code string }{
		{"空 code", "sat", ""},
		{"纯空白 code", "sat", "   \n  "},
		{"未知 type", "bogus", "x > 0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exit, out := runLogicGate(t, map[string]string{"type": c.typ, "code": c.code})
			if exit == 0 || out.OK {
				t.Fatalf("非法输入必须被拒绝: %+v", out)
			}
			if !out.Rejected || out.Reason == "" {
				t.Fatalf("拒绝必须带 rejected:true + reason: %+v", out)
			}
		})
	}
}
