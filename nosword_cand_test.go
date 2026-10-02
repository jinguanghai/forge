package main

// nosword_cand_test.go — 算式候选裁决 (nosword_cand.go) 的结构约束与行为。
// helper 见 nosword_lexer_test.go。

import "testing"

var nswCandDecls = []string{
	"nswIsCandidate", "nswIsCandidateMode", "nswCand", "nswSniffCands", "nswCtxReject",
}

func TestNSWCand_Ownership(t *testing.T) {
	nswTestOwnership(t, "nosword_cand.go", nswCandDecls, "nosword.go", "nosword_eval.go", "nosword_sniff.go")
}

func TestNSWCand_Deterministic(t *testing.T) { nswTestDeterminism(t, "nosword_cand.go") }

func TestNSWCand_EmptyInputRejected(t *testing.T) {
	if nswIsCandidate("") {
		t.Error("空串不应被判为算式候选 (隐式口径)")
	}
	if nswIsCandidateMode("", true) {
		t.Error("空串不应被判为算式候选 (显式口径)")
	}
	if got := nswSniffCands(""); len(got) != 0 {
		t.Errorf("nswSniffCands(\"\") = %v, want 空", got)
	}
}

func TestNSWCand_SniffStable(t *testing.T) {
	const text = "一共 {{35*30}} 元。"
	first := nswSniffCands(text)
	for i := 0; i < 100; i++ {
		if got := nswSniffCands(text); len(got) != len(first) {
			t.Fatalf("第 %d 次候选数漂移: %d vs %d", i, len(got), len(first))
		}
	}
}
