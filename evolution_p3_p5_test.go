package main

import (
	"strings"
	"testing"
)

// P3: 慢 gate (knowledge/browser/tcm) 缓存 TTL=1800s, 其余 600s。
func TestSlowGateLongerCacheTTL(t *testing.T) {
	slow := []string{"knowledge", "browser", "tcm"}
	fast := []string{"python", "math", "sh", "go", "node", "regex", "chain", "self", "logic"}
	for _, l := range slow {
		if got := cacheTTLForLang(l); got != 1800 {
			t.Errorf("慢 gate %s: TTL=%d, want 1800", l, got)
		}
	}
	for _, l := range fast {
		if got := cacheTTLForLang(l); got != 600 {
			t.Errorf("普通 gate %s: TTL=%d, want 600", l, got)
		}
	}
}

// P5: system prompt 稳定段(systemPrompt 常量)必须占据前缀开头 → 前缀缓存可命中段在前。
func TestSystemPromptStableSegmentFirst(t *testing.T) {
	s := buildSystemPrompt(t.TempDir(), nil) // 空目录: 无 memory 注入, 只测稳定段占比
	if !strings.HasPrefix(s, systemPrompt) {
		t.Fatalf("system prompt 必须以稳定段 systemPrompt 常量开头 (前缀缓存守卫)")
	}
	ratio := len(systemPrompt) * 100 / len(s)
	if ratio < 40 {
		t.Errorf("稳定段占比过低: %d%% (<40%%)", ratio)
	}
}
