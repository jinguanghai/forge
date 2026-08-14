package main

import (
	"strings"
	"testing"
)

// --- truncateAnchor ---

func TestTruncateAnchor_Short(t *testing.T) {
	in := "查询中药 麻黄"
	got := truncateAnchor(in)
	if got != in {
		t.Errorf("short input must pass through unchanged, got %q", got)
	}
}

func TestTruncateAnchor_Long(t *testing.T) {
	in := strings.Repeat("药", 200)
	got := truncateAnchor(in)
	if len([]rune(got)) != 153 { // 150 + "..."
		t.Fatalf("expected 153 runes, got %d (%q)", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("药", 150)) {
		t.Errorf("prefix must be first 150 runes")
	}
}

func TestTruncateAnchor_Exactly150(t *testing.T) {
	in := strings.Repeat("a", 150)
	got := truncateAnchor(in)
	if got != in {
		t.Errorf("150 runes is the boundary, must not truncate, got len %d", len([]rune(got)))
	}
}

func TestTruncateAnchor_MixedRunes(t *testing.T) {
	// 中文+emoji 混排: 按 rune 截断不应切坏 UTF-8 序列
	in2 := strings.Repeat("药", 100) + "😀" + strings.Repeat("方", 100)
	got := truncateAnchor(in2)
	if len([]rune(got)) != 153 {
		t.Fatalf("expected 153 runes (no broken UTF-8), got %d", len([]rune(got)))
	}
	if !strings.HasPrefix(got, "药") {
		t.Errorf("prefix must start with 药")
	}
}

// --- filterValidToolCalls ---

func TestFilterValidToolCalls_DropIncomplete(t *testing.T) {
	acc := []ToolCall{
		{ID: "call_1", Function: FunctionCall{Name: "forge", Arguments: "{}"}},
		{ID: "", Function: FunctionCall{Name: "", Arguments: "partial"}},  // raw delta fragment
		{ID: "call_2", Function: FunctionCall{Name: "", Arguments: "{}"}}, // empty name
		{ID: "", Function: FunctionCall{Name: "forge", Arguments: "{}"}},  // empty ID
	}
	got := filterValidToolCalls(acc)
	if len(got) != 1 {
		t.Fatalf("expected 1 valid call, got %d", len(got))
	}
	if got[0].ID != "call_1" {
		t.Errorf("survivor must be call_1, got %q", got[0].ID)
	}
}

func TestFilterValidToolCalls_AllValid(t *testing.T) {
	acc := []ToolCall{
		{ID: "a", Function: FunctionCall{Name: "forge", Arguments: `{"lang":"python"}`}},
		{ID: "b", Function: FunctionCall{Name: "math", Arguments: "1+1"}},
	}
	got := filterValidToolCalls(acc)
	if len(got) != 2 {
		t.Fatalf("all valid must survive, got %d", len(got))
	}
}

func TestFilterValidToolCalls_Empty(t *testing.T) {
	got := filterValidToolCalls(nil)
	if len(got) != 0 {
		t.Fatalf("nil input -> empty output, got %d", len(got))
	}
	got2 := filterValidToolCalls([]ToolCall{})
	if len(got2) != 0 {
		t.Fatalf("empty input -> empty output, got %d", len(got2))
	}
}

func TestFilterValidToolCalls_DoesNotMutateOrder(t *testing.T) {
	acc := []ToolCall{
		{ID: "x", Function: FunctionCall{Name: "forge", Arguments: "1"}},
		{ID: "", Function: FunctionCall{Name: "", Arguments: ""}},
		{ID: "y", Function: FunctionCall{Name: "sh", Arguments: "ls"}},
	}
	got := filterValidToolCalls(acc)
	if len(got) != 2 || got[0].ID != "x" || got[1].ID != "y" {
		t.Fatalf("order must be preserved, got %+v", got)
	}
}
