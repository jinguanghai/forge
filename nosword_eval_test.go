package main

// nosword_eval_test.go — 求值入口与数字格式化 (nosword_eval.go) 的结构约束与行为。
// helper 见 nosword_lexer_test.go。

import "testing"

var nswEvalFileDecls = []string{
	"nswFloatExactMax", "nswParenBalanced", "nswEval", "nswEvalExplicit",
	"nswEvalMode", "nswFmtNum", "nswFracDigits", "nswHasDanglingDot",
}

func TestNSWEvalFile_Ownership(t *testing.T) {
	nswTestOwnership(t, "nosword_eval.go", nswEvalFileDecls, "nosword.go", "nosword_lexer.go", "nosword_cand.go")
}

func TestNSWEvalFile_NoPackageVar(t *testing.T) { nswTestNoPackageVar(t, "nosword_eval.go") }

func TestNSWEvalFile_Deterministic(t *testing.T) { nswTestDeterminism(t, "nosword_eval.go") }

func TestNSWEvalFile_ParenBalanced(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"()", true}, {"(())", true}, {"(1+2)", true},
		{"(", false}, {")", false}, {"(()", false},
	}
	for _, c := range cases {
		if got := nswParenBalanced(c.in); got != c.want {
			t.Errorf("nswParenBalanced(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNSWEvalFile_FmtNumStable(t *testing.T) {
	for _, v := range []float64{0, 1, -1, 0.5, 1e9, 1.0 / 3.0, 123456.789} {
		a, b := nswFmtNum(v), nswFmtNum(v)
		if a != b {
			t.Errorf("nswFmtNum(%v) 两次结果不同: %q vs %q", v, a, b)
		}
		if a == "" {
			t.Errorf("nswFmtNum(%v) 返回空串", v)
		}
	}
}
