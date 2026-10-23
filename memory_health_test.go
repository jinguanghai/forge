package main

// memory_health_test.go — 六期 DMAIC I3: 记忆体检测试

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemHealthReportOnCurrent(t *testing.T) {
	// 对真实 memory.json 出体检报告 (只读, 不改)
	report := memHealthReport(testRoot(t))
	t.Logf("\n%s", report)
}

func TestMemHealthReportCorrupt(t *testing.T) {
	wd := t.TempDir()
	// 损坏 JSON
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte("{broken"), 0644)
	r := memHealthReport(wd)
	if !strings.Contains(r, "解析失败") && !strings.Contains(r, "不可读") {
		t.Fatalf("损坏 JSON 应报错: %s", r)
	}
	// 缺字段
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(`{"identity":"x"}`), 0644)
	r2 := memHealthReport(wd)
	if !strings.Contains(r2, "必填字段缺失") {
		t.Fatalf("缺字段应报缺失: %s", r2)
	}
	// 过时 gate 引用
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(`{"identity":"x","role":"r","language":"zh","working_dir":"w","gates":"python","axioms":"a","self_governance":{},"key_findings":[{"title":"eprover 经验","content":"..."}]}`), 0644)
	r3 := memHealthReport(wd)
	if !strings.Contains(r3, "已删 gate") {
		t.Fatalf("过时 gate 应被检出: %s", r3)
	}
}

func TestMemHealthReportHealthy(t *testing.T) {
	wd := t.TempDir()
	// gates 由 铸剑炉_GATES 动态生成 —— 手写清单必腐化 (加 gate 即失效)
	body := fmt.Sprintf(`{"identity":"id","role":"role","language":"zh","working_dir":"wd","gates":%q,"axioms":"公理","self_governance":{}}`,
		strings.Join(铸剑炉_GATES, "/"))
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(body), 0644)
	r := memHealthReport(wd)
	if strings.Contains(r, "🔴") || strings.Contains(r, "🟡") {
		t.Fatalf("健康记忆不应报异常: %s", r)
	}
	if !strings.Contains(r, "无异常") {
		t.Fatalf("应报无异常: %s", r)
	}
}

// TestMemHealthLintRejectsCtrlChar: 20261011 实测事故的回归判据 ——
// JSON 文本层合法 (\f 只是两个 ASCII 字节) 而解码后是换页符 0x0C。
// 病根: 传参 JSON 里写 "D:\forge", 路径落库成 "D:<FF>orge"。
func TestMemHealthLintRejectsCtrlChar(t *testing.T) {
	bad := []byte(`{"axioms":"a","self_governance":{},"note":"D:\forge"}`)
	err := memHealthLint(bad)
	if err == nil {
		t.Fatal("含换页符 (合法 JSON 转义) 的记忆必须被拒绝写入")
	}
	if !strings.Contains(err.Error(), "控制字符") {
		t.Fatalf("拒绝理由应指出控制字符: %v", err)
	}
	// 允许清单: \n \t 必须放行 (判据不得过宽)
	// (用非主记忆结构, 避开"必填锚点字段"分支 —— 本用例只测控制字符闸)
	ok := []byte(`{"note":"A\nB\tC"}`)
	if err := memHealthLint(ok); err != nil {
		t.Fatalf("\\n \\t 应放行: %v", err)
	}
}

// TestMemHealthReportCtrlChar: 只读体检也看得见控制字符 (写入前拒绝 + 事后可见, 两侧同判据)
func TestMemHealthReportCtrlChar(t *testing.T) {
	wd := t.TempDir()
	body := fmt.Sprintf(`{"identity":"id","role":"role","language":"zh","working_dir":"wd","gates":%q,"axioms":"公理","self_governance":{},"note":"D:\forge"}`,
		strings.Join(铸剑炉_GATES, "/"))
	os.WriteFile(filepath.Join(wd, "memory.json"), []byte(body), 0644)
	r := memHealthReport(wd)
	if !strings.Contains(r, "非法控制字符") {
		t.Fatalf("体检报告应检出控制字符: %s", r)
	}
}

// TestMemCtrlCharIssuesNested: 嵌套结构 (数组/嵌套对象) 也必须扫到 —— 记忆是分层结构。
func TestMemCtrlCharIssuesNested(t *testing.T) {
	m := map[string]interface{}{
		"lessons": []interface{}{
			map[string]interface{}{"content": "ok"},
			map[string]interface{}{"content": "bad\x0c"},
		},
	}
	bad := memCtrlCharIssues(m, "")
	if len(bad) != 1 || !strings.Contains(bad[0], "0x0C") {
		t.Fatalf("嵌套数组里的控制字符应被检出: %v", bad)
	}
}
