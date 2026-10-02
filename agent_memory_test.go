// agent_memory_test.go — 记忆锚点装配的确定性与剔除契约。
//
// 本文件的断言对象是"前缀缓存的地基": reorderMemoryJSON 的确定性序、
// buildMemoryTailText 的动态字段剔除、memoryTailDiff 的字段级差异。
// 这三者任一失效 → DeepSeek 前缀缓存命中率断崖, 且症状是"钱变多"而非"报错"。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestMemory(t *testing.T, dir, content string) {
	t.Helper()
	p := memoryFilePath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
}

func TestReorderMemoryJSON_StableOrderThenExtraSorted(t *testing.T) {
	in := []byte(`{"zeta":"z","axioms":"a","alpha":"x","identity":"i"}`)
	out := string(reorderMemoryJSON(in))
	if !json.Valid([]byte(out)) {
		t.Fatalf("输出不是合法 JSON: %s", out)
	}
	iID := strings.Index(out, `"identity"`)
	iAx := strings.Index(out, `"axioms"`)
	iAl := strings.Index(out, `"alpha"`)
	iZe := strings.Index(out, `"zeta"`)
	for _, c := range []struct {
		n string
		i int
	}{{"identity", iID}, {"axioms", iAx}, {"alpha", iAl}, {"zeta", iZe}} {
		if c.i < 0 {
			t.Fatalf("字段 %s 丢失: %s", c.n, out)
		}
	}
	if iID > iAx {
		t.Errorf("稳定字段未按 memStableOrder 排序 (identity 应在 axioms 前): %s", out)
	}
	if !(iAx < iAl && iAl < iZe) {
		t.Errorf("未知字段未按字典序追加尾部 (axioms<alpha<zeta): %s", out)
	}
}

func TestReorderMemoryJSON_Deterministic(t *testing.T) {
	in := []byte(`{"b":1,"a":2,"identity":"i","c":3}`)
	first := string(reorderMemoryJSON(in))
	for i := 0; i < 50; i++ {
		if got := string(reorderMemoryJSON(in)); got != first {
			t.Fatalf("第 %d 次输出不同 —— 非确定性序会打断前缀缓存:\n %s\n %s", i, got, first)
		}
	}
}

func TestReorderMemoryJSON_InvalidPassthrough(t *testing.T) {
	in := []byte("这不是 JSON")
	if got := reorderMemoryJSON(in); string(got) != string(in) {
		t.Errorf("非法输入应原样返回 (不破坏注入), got %q", got)
	}
}

func TestCurrentActiveTask_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := currentActiveTask(dir); got != "" {
		t.Errorf("无 memory.json 时应返回空串, got %q", got)
	}
}

func TestCurrentActiveTask_ReadsField(t *testing.T) {
	dir := t.TempDir()
	writeTestMemory(t, dir, `{"identity":"x","active_task":"B3 批5"}`)
	if got := currentActiveTask(dir); got != "B3 批5" {
		t.Errorf("active_task = %q, want %q", got, "B3 批5")
	}
}

func TestBuildMemoryTailText_StripsDynamicFields(t *testing.T) {
	dir := t.TempDir()
	writeTestMemory(t, dir, `{"identity":"i","key_findings":[{"a":1}],"folded_memory":"x"}`)
	out := buildMemoryTailText(dir)
	for _, f := range dynamicMemoryFields {
		if strings.Contains(out, `"`+f+`"`) {
			t.Errorf("动态字段 %s 未剔除, 会打断前缀缓存: %s", f, out)
		}
	}
	if !strings.Contains(out, `"identity"`) {
		t.Errorf("稳定字段丢失: %s", out)
	}
}

func TestMemoryTailDiff_AddedChangedRemoved(t *testing.T) {
	old := `{"identity":"i","role":"r","gates":"g"}`
	nw := `{"identity":"i","role":"R","axioms":"a"}`
	diff := memoryTailDiff(old, nw)
	if !strings.Contains(diff, "role: ") {
		t.Errorf("变化字段未报告: %q", diff)
	}
	if !strings.Contains(diff, "axioms: ") {
		t.Errorf("新增字段未报告: %q", diff)
	}
	if !strings.Contains(diff, "gates: <已删除>") {
		t.Errorf("删除字段未报告: %q", diff)
	}
	if strings.Contains(diff, "identity") {
		t.Errorf("未变化字段不应出现 (省 token): %q", diff)
	}
}
