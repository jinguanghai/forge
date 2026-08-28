package main

// gate_semantic_test.go — 六期 DMAIC I1: 语义 gate 自动路由测试
// 验证: forgeDetectLang 在无编译器特征时, 纯数学/逻辑表达式路由到 math/logic,
// 且不误伤 python/其他编译器代码。

import "testing"

func TestSemanticGateRouting(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		// ── 数学表达式 → math ──
		{"算术", "2+2", "math"},
		{"混合运算", "3*(x+1)-3*x", "math"},
		{"分数", "1/3 + 2/5", "math"},
		{"幂", "2^10", "math"},
		{"含括号", "(3+4)*5", "math"},
		{"等式验证", "2+2=4", "math"},

		// ── 逻辑表达式 → logic ──
		{"命题逻辑", "a & b", "logic"},
		{"蕴含", "a => b", "logic"},
		{"双蕴含", "a <=> b", "logic"},
		{"析取", "x | y", "logic"},
		{"全称量词", "∀x P(x)", "logic"},

		// ── 不误伤: 编译器/代码保持原路由 ──
		{"python print", "print(1)", "python"},
		{"python import", "import math\nprint(math.pi)", "python"},
		{"python def", "def f():\n    return 1", "python"},
		{"python class", "class A:\n    pass", "python"},
		{"node console", "console.log('x')", "node"},
		{"go package", "package main\nfunc main() {}", "go"},
		{"shell", "echo hi", "sh"},
		{"纯赋值", "x = 5", "python"},
		{"URL 含&", "https://example.com?a=1&b=2", "python"},
		{"多行代码", "x = 1\ny = x + 1", "python"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := forgeDetectLang(c.code, ""); got != c.want {
				t.Errorf("detect(%q) = %q, want %q", c.code, got, c.want)
			}
		})
	}
}

func TestDetectSemanticGateDirect(t *testing.T) {
	// 直接测 detectSemanticGate: 已排除编译器特征后的行为
	if got := detectSemanticGate("2+2"); got != "math" {
		t.Errorf("2+2 → %q, want math", got)
	}
	if got := detectSemanticGate("a => b"); got != "logic" {
		t.Errorf("a => b → %q, want logic", got)
	}
	if got := detectSemanticGate("print(1)"); got != "" {
		t.Errorf("print(1) → %q, want 空(不路由)", got)
	}
	if got := detectSemanticGate("x = 5"); got != "" {
		t.Errorf("x = 5 → %q, want 空(纯赋值不路由)", got)
	}
}
