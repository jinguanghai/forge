package main

// 鲁棒性测试: 纯函数/小工具的边界场景
// 覆盖: 空/null/超长/Unicode/注入/极端值/畸形输入
// 公理五: 把可判定的地盘收给死程序 —— 这里测的就是「程序死了会怎样」。
//
// 设计原则:
//   1. 不重复已有 happy path
//   2. 每条用例测一个独立边界, 失败时能精确指认
//   3. 极端值用 math.MaxInt64 等显式常量
//   4. NULL 字节 / ANSI 残留 / 超长 1MB / Unicode 混合 都至少有一例

// 覆盖: 鲁棒性测试 — 错误分类与降级(isTimeoutErr / isTransientError / shouldFallback / pickFallback / checkDangerousCode / netEgressHint / gateEnabled / compilerErrorsToJSON / detectSemanticGate)
//
// F2 文件≤500行 上限触发: 原单文件超上限, 拆分到此文件。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestRobustness_IsTimeoutErr_EdgeCases(t *testing.T) {
	if !isTimeoutErr(context.DeadlineExceeded) {
		t.Error("DeadlineExceeded 应判定为超时")
	}
	if !isTimeoutErr(fmt.Errorf("wrap: %w", context.DeadlineExceeded)) {
		t.Error("wrapped DeadlineExceeded 应判定为超时")
	}
	if isTimeoutErr(nil) {
		t.Error("nil 不算超时")
	}
	if isTimeoutErr(errors.New("其他错误")) {
		t.Error("普通 error 不算超时")
	}
	if isTimeoutErr(context.Canceled) {
		t.Error("Canceled 不算超时(DeadlineExceeded 才算)")
	}
	if isTimeoutErr(fmt.Errorf("timeout in text")) {
		t.Error("含『timeout』文本的不算 errors.Is 不命中")
	}
}

func TestRobustness_IsTransientError_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		r    ForgeGateResult
		want bool
	}{
		{"OK", ForgeGateResult{OK: true}, false},                     // OK 不算
		{"Timeout=true", ForgeGateResult{Timeout: true}, true},       // 超时 = 瞬时
		{"EnvFailure=true", ForgeGateResult{EnvFailure: true}, true}, // 环境失败 = 瞬时
		{"compile 阶段非环境", ForgeGateResult{Stage: "compile", ExitCode: 1}, false},
		{"execute 阶段非环境", ForgeGateResult{Stage: "execute", ExitCode: 1}, false}, // execute 一律可缓存
		{"path 阶段未知", ForgeGateResult{Stage: "path", ExitCode: 1}, false},
		{"错误信息含 connection", ForgeGateResult{Stage: "compile", Error: "connection refused"}, false}, // 文本匹配已删
		{"错误信息含 timeout 文本", ForgeGateResult{Stage: "compile", Error: "timeout occurred"}, false},   // 文本不顶
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isTransientError(c.r)
			if got != c.want {
				t.Errorf("isTransientError(%+v) = %v, want %v", c.r, got, c.want)
			}
		})
	}
}

func TestRobustness_ShouldFallback_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		r    ForgeGateResult
		want bool
	}{
		{"OK", ForgeGateResult{OK: true}, false},
		{"OK + EnvFailure", ForgeGateResult{OK: true, EnvFailure: true}, false},           // OK 优先
		{"Timeout=true", ForgeGateResult{Timeout: true}, false},                           // 超时不降级
		{"Timeout + EnvFailure", ForgeGateResult{Timeout: true, EnvFailure: true}, false}, // 超时优先
		{"EnvFailure", ForgeGateResult{EnvFailure: true}, true},
		{"编译失败非环境", ForgeGateResult{Stage: "compile", ExitCode: 1}, false},
		{"执行失败非环境", ForgeGateResult{Stage: "execute", ExitCode: 1}, false},
		{"错误信息含 timeout", ForgeGateResult{Error: "timeout"}, false}, // 不看文本
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldFallback(c.r)
			if got != c.want {
				t.Errorf("shouldFallback(%+v) = %v, want %v", c.r, got, c.want)
			}
		})
	}
}

func TestRobustness_PickFallback_EdgeCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"python", "node"},
		{"node", "python"},
		{"js", "python"},
		{"go", ""}, // go 无降级
		{"", ""},   // 空也走 default
		{"未知", ""},
		{"PYTHON 大写", ""}, // case 敏感
		{"Node 大写", ""},   // case 敏感
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := pickFallback(c.in)
			if got != c.want {
				t.Errorf("pickFallback(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRobustness_CheckDangerousCode_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		kind string
		hit  bool
	}{
		{"空", "", "", false},
		{"rm 嵌入注释", "// rm -rf /", "删除", true},     // regex 在全文, 注释里也命中(防混淆)
		{"rm 嵌入字符串", `s = "rm -rf /"`, "删除", true}, // 字符串里也命中
		{"rm 跨大小写", "RM -RF /", "删除", true},
		{"rm -Rf 单条", "rm -Rf /tmp", "删除", true},
		{"RemoveAll 多参数", `os.RemoveAll(os.Getenv("PATH"))`, "删除", true},
		{"覆盖 memory.json 多重", `open("memory.json", "a")`, "覆盖", true},
		{"覆盖 forge.exe", `open("forge.exe", "w")`, "", false}, // 不在白名单
		{"write file 但非保护名", `os.WriteFile("foo.txt", data, 0644)`, "", false},
		{"Format-Volume 全小写", "format-volume c", "磁盘", true},
		{"Format-Volume 嵌入字符串", `s = "format-volume"`, "磁盘", true},        // 字符串里也命中
		{"git push force 嵌入字符串", `print("git push --force")`, "强推", true}, // 字符串里也命中
		{"taskkill forge 不区分大小写", "TASKKILL /F /IM FORGE.EXE", "自杀", true},
		{"仅 fork bomb 关键", ":(){ :|:& };:", "炸弹", true},
		{"fork bomb 近似(缺 &:)", ":(){ :|: };:", "", false},
		{"超长无危险", strings.Repeat("safe code\n", 10000), "", false},
		{"emoji 嵌入 rm", "echo \U0001F600; rm -rf /", "删除", true},
		{"中文注释含 rm", "// 这是删除 rm -rf / 的代码", "删除", true}, // 仍命中, 因为危险模式匹配 code 全文
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, _, hit := checkDangerousCode(c.in)
			if hit != c.hit {
				t.Errorf("hit = %v, want %v (kind=%q, got kind=%q)", hit, c.hit, c.kind, kind)
			}
			if c.hit && kind != c.kind {
				t.Errorf("kind = %q, want %q", kind, c.kind)
			}
		})
	}
}

func TestRobustness_NetEgressHint_EdgeCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"print('hello')", ""},
		{"import requests", "py-requests"},
		{"import urllib.request", "py-urllib"},
		{"from aiohttp import ClientSession", ""},   // py-http 模式只覆盖 import aiohttp / import http.client
		{"const fetch = require('node-fetch')", ""}, // node-fetch 不在 node-net
		{"const x = require('axios')", ""},
		{"curl https://api.x.com", "sh-net"},
		{"wget https://example.com", "sh-net"},
		{"powershell Invoke-WebRequest", ""},
		{"# import os (Python)", ""},
		{"import requests\nimport urllib", "py-requests"}, // 仅 import urllib 不在 py-urllib 模式
		{"import socket", "py-socket"},
		{"from socket import httpclient", "py-socket"},
		{"http.Get", "go-net"},
		{"net.Dial", "go-net"},
		{"require('https')", "node-net"},
		{"fetch(url)", "node-net"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := netEgressHint(c.in)
			if got != c.want {
				t.Errorf("netEgressHint(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// ─── gateEnabled / exeSuffix / atoi / compilerErrorsToJSON ──────────

func TestRobustness_GateEnabled_EdgeCases(t *testing.T) {
	cases := []struct {
		name    string
		gate    string
		enabled []string
		want    bool
	}{
		{"空 enabled 全开", "python", []string{}, true},
		{"nil enabled 全开", "python", nil, true},
		{"含此 gate", "python", []string{"python", "go"}, true},
		{"不含此 gate (非注册表)", "unknown", []string{"python"}, true},
		{"不含此 gate (注册表)", "math", []string{"go"}, false}, // math 在注册表, 不在 enabled → false
		{"中文 gate", "中药", []string{}, true},
		{"空字符串 gate", "", []string{}, true},
		{"nil vs 空", "", []string{"a"}, true},
		{"case 敏感", "PYTHON", []string{"python"}, true}, // case 敏感
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gateEnabled(c.gate, c.enabled)
			if got != c.want {
				t.Errorf("gateEnabled(%q, %v) = %v, want %v", c.gate, c.enabled, got, c.want)
			}
		})
	}
}

func TestRobustness_CompilerErrorsToJSON_EdgeCases(t *testing.T) {
	// nil
	if got := compilerErrorsToJSON(nil); got != "" {
		t.Errorf("nil 应返回空串, got %q", got)
	}
	// 空 slice
	if got := compilerErrorsToJSON([]CompilerError{}); got != "" {
		t.Errorf("空 slice 应返回空串, got %q", got)
	}
	// 单条
	js := compilerErrorsToJSON([]CompilerError{{Lang: "go", Line: 1, Col: 2, Msg: "m"}})
	if js == "" {
		t.Fatal("非空应返回 JSON")
	}
	var parsed []CompilerError
	if err := json.Unmarshal([]byte(js), &parsed); err != nil {
		t.Fatalf("无效 JSON: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Lang != "go" || parsed[0].Line != 1 {
		t.Errorf("JSON 内容错: %+v", parsed)
	}
	// 中文 msg
	js = compilerErrorsToJSON([]CompilerError{{Lang: "go", Msg: "中文错误"}})
	if !strings.Contains(js, "中文错误") {
		t.Errorf("中文 msg 丢失: %q", js)
	}
}

// ─── detectSemanticGate / forgeDetectLang ──────────────────────────

func TestRobustness_DetectSemanticGate_EdgeCases(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"1+2", "math"},
		{"3*4-1", "math"},
		{"x = 5", ""}, // 没有运算符, 不路 math
		{"x^2", "math"},
		{"a & b", "logic"},
		{"a | b", "logic"},
		{"a => b", "logic"},
		{"forall x", ""}, // logic ops regex 不覆盖 "forall"
		{"exists y", ""}, // 同上
		// 不应路由
		{"import os", ""},
		{"def foo():", ""},
		{"print(1)", ""},
		{"var x = 1", ""},
		{"return 1", ""},
		{"# comment", ""},
		{"a; b", ""},
		{"a {b}", ""},
		{"a (b)", ""}, // 不应被关键词匹配, 但 goto 里有 "(" 触发? 看 detectSemanticGate 实现
		{"1", ""},     // 无运算符无 routing
		{"a", ""},     // 无运算符无 routing
		// 多行(应不路由)
		{"1 + 2\n3 + 4", ""},
		// 嵌入分号
		{"x=1;y=2", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := detectSemanticGate(c.in)
			if got != c.want {
				t.Errorf("detectSemanticGate(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
