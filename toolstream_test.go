package main

import "testing"

func TestStreamerChunked(t *testing.T) {
	chunks := []string{`{"act`, `ion":"run","code":"import o`, `s\\nprint(hi)"`, `,"lang":"python","inp`, `ut":""}`}
	var st toolCodeStreamer
	got := ""
	for _, c := range chunks {
		got += st.feed(c)
	}
	if got != "import os\\nprint(hi)" {
		t.Fatalf("chunked mismatch: got %q", got)
	}
}

func TestExtractCodeBasic(t *testing.T) {
	if r := extractCodeFromArgs(`{"c":"x","code":"abc"}`); r != "abc" {
		t.Fatalf("basic=%q", r)
	}
	if r := extractCodeFromArgs(`{"code":"a\\nb"}`); r != "a\\nb" {
		t.Fatalf("esc=%q", r)
	}
	if r := extractCodeFromArgs(`{"code":"abc`); r != "abc" {
		t.Fatalf("unclosed=%q", r)
	}
	if r := extractCodeFromArgs(`{"action":"run"}`); r != "" {
		t.Fatalf("nocode=%q", r)
	}
}
