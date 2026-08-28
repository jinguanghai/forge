package main

// gate_registry_test.go — 三期 DMAIC I3 gate 注册表/配置化测试

import (
	"strings"
	"testing"
	"time"
)

func TestGateEnabled(t *testing.T) {
	// 空列表 = 全部启用
	for _, g := range gateNames() {
		if !gateEnabled(g, nil) {
			t.Fatalf("空列表应启用全部, %s 被禁用", g)
		}
	}
	// 列表过滤
	if gateEnabled("tcm", []string{"math", "logic"}) {
		t.Fatal("tcm 应在禁用列表时禁用")
	}
	if !gateEnabled("math", []string{"math", "logic"}) {
		t.Fatal("math 应在启用列表")
	}
	// 未知 lang (编译器) 不受配置影响
	if !gateEnabled("python", []string{"math"}) {
		t.Fatal("编译器 python 不应受 gate 配置影响")
	}
	if !gateEnabled("go", []string{"math"}) {
		t.Fatal("编译器 go 不应受 gate 配置影响")
	}
}

func TestDescribeGates(t *testing.T) {
	all := describeGates(nil)
	for _, g := range gateRegistry {
		if !strings.Contains(all, g.Description) {
			t.Fatalf("全部启用时缺 %s 描述", g.Name)
		}
	}
	// 禁用 tcm + self 后不应出现
	subset := describeGates([]string{"math", "logic"})
	if strings.Contains(subset, "tcm") || strings.Contains(subset, "中医药") {
		t.Fatalf("禁用 tcm 后不应出现: %s", subset)
	}
	if strings.Contains(subset, "self") {
		t.Fatalf("禁用 self 后不应出现: %s", subset)
	}
	if !strings.Contains(subset, "math") || !strings.Contains(subset, "logic") {
		t.Fatalf("启用项应保留: %s", subset)
	}
}

func TestGateDisabledBlock(t *testing.T) {
	// 禁用 logic → forgeGateSelfHosted 返回"未启用" (拦截发生在执行前)
	f := &Forge{cfg: &Config{GatesEnabled: []string{"math"}}}
	r := f.forgeGateSelfHosted("x = 1", "logic", CompilerDef{}, "", time.Now())
	if r.OK {
		t.Fatal("禁用 gate 不应执行")
	}
	if !strings.Contains(r.Error, "未启用") {
		t.Fatalf("应提示未启用: %s", r.Error)
	}
}

func TestSystemPromptDynamicGates(t *testing.T) {
	// 稳定段守卫: 动态段追加在 systemPrompt 之后, 前缀不变 (空目录避免 memory 注入稀释)
	wd := t.TempDir()
	s := buildSystemPrompt(wd, nil)
	if !strings.HasPrefix(s, systemPrompt) {
		t.Fatal("稳定段 systemPrompt 必须在前缀 (前缀缓存守卫)")
	}
	if !strings.Contains(s, "<gates_enabled>") {
		t.Fatal("应含 <gates_enabled> 动态段")
	}
	// 全部启用: 含 tcm
	if !strings.Contains(s, "中医药") {
		t.Fatal("全启用应含 tcm 规则")
	}
	// 禁用 tcm: 不含
	s2 := buildSystemPrompt(wd, []string{"math", "logic"})
	if strings.Contains(s2, "中医药") {
		t.Fatal("禁用 tcm 后 prompt 不应含其规则")
	}
	if !strings.Contains(s2, "必须实际计算") {
		t.Fatal("启用 math 应含其规则")
	}
	// 稳定段占比仍满足 P5 守卫 (≥40%)
	if ratio := len(systemPrompt) * 100 / len(s); ratio < 40 {
		t.Fatalf("稳定段占比过低: %d%%", ratio)
	}
}

// ── 四期 I: 18→12 裁剪验收 ──────────────────────────────────

// TestTrimmedGateRegistry 验收: gate 注册表 8 个, 不含已删 gate
func TestTrimmedGateRegistry(t *testing.T) {
	names := gateNames()
	if len(names) != 8 {
		t.Fatalf("gate 注册表应为 8 个, got %d: %v", len(names), names)
	}
	for _, gone := range []string{"eprover", "repair", "system"} {
		for _, n := range names {
			if n == gone {
				t.Fatalf("已裁剪 gate %s 仍在注册表", gone)
			}
		}
	}
	// describeGates 输出 8 条 (立项书验收标准 3)
	all := describeGates(nil)
	lines := strings.Count(all, "   - ")
	if lines != 8 {
		t.Fatalf("describeGates 应为 8 条, got %d", lines)
	}
	if strings.Contains(all, "eprover") || strings.Contains(all, "repair") || strings.Contains(all, "system_gate") {
		t.Fatalf("describeGates 含已删 gate: %s", all)
	}
}

// TestTrimmedCompilers 验收: COMPILERS 12 个, 不含已删 6 个
func TestTrimmedCompilers(t *testing.T) {
	if len(铸剑炉_COMPILERS) != 12 {
		t.Fatalf("COMPILERS 应为 12 个, got %d", len(铸剑炉_COMPILERS))
	}
	for _, gone := range []string{"deno", "rust", "tcc", "system", "repair", "eprover"} {
		if _, ok := 铸剑炉_COMPILERS[gone]; ok {
			t.Fatalf("已裁剪 %s 仍在 COMPILERS", gone)
		}
	}
	// 保留 12 个核心面
	for _, keep := range []string{"python", "go", "sh", "node", "math", "logic", "regex", "knowledge", "tcm", "browser", "chain", "self"} {
		if _, ok := 铸剑炉_COMPILERS[keep]; !ok {
			t.Fatalf("应保留的 %s 不在 COMPILERS", keep)
		}
	}
}
