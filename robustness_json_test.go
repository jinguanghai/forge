package main

// 鲁棒性测试: 纯函数/小工具的边界场景
// 覆盖: 空/null/超长/Unicode/注入/极端值/畸形输入
// 公理二: 把可判定的地盘收给死程序 —— 这里测的就是「程序死了会怎样」。
//
// 设计原则:
//   1. 不重复已有 happy path
//   2. 每条用例测一个独立边界, 失败时能精确指认
//   3. 极端值用 math.MaxInt64 等显式常量
//   4. NULL 字节 / ANSI 残留 / 超长 1MB / Unicode 混合 都至少有一例

// 覆盖: 鲁棒性测试 — JSON 解析(validGateJSON / parseGateReject / NULL 字节注入)
//
// F2 文件≤500行 上限触发: 原单文件超上限, 拆分到此文件。

import (
	"fmt"
	"strings"
	"testing"
)

func TestRobustness_ValidGateJSON_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"空", "", false},
		{"仅空白", "   ", false},
		{"合法", `{"a":1}`, true},
		{"合法多字段", `{"a":1,"b":"x","c":[1,2,3]}`, true},
		{"合法嵌套", `{"a":{"b":1}}`, true},
		{"含前空白", `   {"a":1}`, true},
		{"含换行", "\n{\"a\":1}", true},
		{"不合法", `{"a":}`, false},
		{"数组开头", `[1,2,3]`, false}, // validGateJSON 要求是对象
		// 真实 NULL 字面量不被 Go 接受; 改为构建 []byte 注入
		// (见下 TestRobustness_ValidGateJSON_NULLInjection),
		{"字面 false", "false", false},
		{"字面 null", "null", false},
		{"字符串", `"x"`, false},
		{"数字", "123", false},
		{"半截", "{ json: [1,2", false},
		{"JSON 注释", `// comment\n{"a":1}`, false},
		{"超长合法", `{"a":"` + strings.Repeat("x", 10000) + `"}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("validGateJSON panic: %v", r)
				}
			}()
			got := validGateJSON(c.in)
			if got != c.want {
				t.Errorf("validGateJSON(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ─── NULL 字节注入测试 (Go 源代码不允许 NUL 字面量) ─────────────────

func TestRobustness_ValidGateJSON_NULLInjection(t *testing.T) {
	cases := []string{
		string([]byte{0x00}),
		string([]byte{'{', 0x00, '}'}),
		string([]byte{'{', '"', 'a', 0x00, '"', ':', '1', '}'}),
		strings.Repeat(string([]byte{0x00}), 100),
	}
	for _, in := range cases {
		t.Run(fmt.Sprintf("len=%d", len(in)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("validGateJSON panic: %v", r)
				}
			}()
			validGateJSON(in) // 只测不 panic
		})
	}
}

func TestRobustness_ParseApproval_NULLInjection(t *testing.T) {
	cases := []string{
		string([]byte{'y', 0x00}),
		string([]byte{0x00, 'y'}),
		string([]byte{'y', 0x00, 'n'}),
		string([]byte{'y', 0x07, 0x08}),
		strings.Repeat(string([]byte{0x00}), 100),
	}
	for _, in := range cases {
		t.Run(fmt.Sprintf("len=%d", len(in)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parseApproval panic: %v", r)
				}
			}()
			parseApproval(in) // 只测不 panic
		})
	}
}

func TestRobustness_ParseGateReject_EdgeCases(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantOK     bool
		wantReason string
	}{
		{"空", "", false, ""},
		{"仅空白", "   ", false, ""},
		{"合法拒绝", `{"rejected":true,"reason":"y"}`, true, "y"},
		{"拒绝无 reason", `{"rejected":true}`, true, ""},
		{"拒绝用 error 字段", `{"rejected":true,"error":"err1"}`, true, "err1"},
		{"未拒绝", `{"rejected":false,"reason":"x"}`, false, ""},
		{"无 rejected 字段", `{"a":1}`, false, ""},
		{"数组开头", `[1,2,3]`, false, ""},
		{"不合法 JSON", `{rejected:true}`, false, ""},
		{"嵌套拒绝", `{"rejected":{"v":true}}`, false, ""}, // 不是 bool
		{"含 reason 特殊字符", `{"rejected":true,"reason":"中文 with 🚀"}`, true, "中文 with 🚀"},
		{"超长 reason", `{"rejected":true,"reason":"` + strings.Repeat("x", 300) + `"}`, true, strings.Repeat("x", 300)},
		{"包含 key=rejected 的不是拒绝", `{"reason":"rejected"}`, false, ""},
		{"仅 boolean 字符串", "true", false, ""},
		// NULL 字面量在 Go 源码不接受, 详见 NULLInjection
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parseGateReject panic: %v", r)
				}
			}()
			ok, reason := parseGateReject(c.in)
			if ok != c.wantOK || reason != c.wantReason {
				t.Errorf("parseGateReject(%q) = (%v,%q), want (%v,%q)", c.in, ok, reason, c.wantOK, c.wantReason)
			}
		})
	}
}

// ─── cacheTTLForLang ──────────────────────────────────────────────────
