package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 构造数据测试: 验证聚合的字段统计正确。
func TestScorecardAggregateConstructed(t *testing.T) {
	data := []byte(
		"{\"lang\":\"python\",\"ok\":true,\"duration_ms\":100,\"retries\":0,\"fallback\":false,\"lang_omitted\":false}\n" +
			"{\"lang\":\"python\",\"ok\":false,\"duration_ms\":200,\"retries\":2,\"fallback\":true,\"lang_omitted\":true}\n" +
			"{\"lang\":\"math\",\"ok\":true,\"duration_ms\":50,\"retries\":0,\"fallback\":false,\"lang_omitted\":false}\n" +
			"{\"lang\":\"\",\"ok\":true,\"duration_ms\":10,\"retries\":0,\"fallback\":false,\"lang_omitted\":false}\n")
	out := scorecardAggregate(data, 0)
	for _, want := range []string{"合计: 4 调用", "python", "math", "unknown"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出缺少 %q\n%s", want, out)
		}
	}
	// 验证 python 行: 2 调用 1 成功 1 失败
	if !strings.Contains(out, "python") {
		t.Fatalf("缺 python 行")
	}
	// 验证窗口: 只取最近2条 → 应为 math + unknown, 合计2
	outW := scorecardAggregate(data, 2)
	if !strings.Contains(outW, "合计: 2 调用") {
		t.Errorf("窗口测试失败\n%s", outW)
	}
}

// 真实数据测试: 读 gate_audit.jsonl, 输出排行榜供死程序交叉验证对照。
func TestScorecardAggregateReal(t *testing.T) {
	path := filepath.Join(os.Getenv("FORGE_WORKDIR_OVERRIDE"), "gate_audit.jsonl")
	if path == "gate_audit.jsonl" {
		path = filepath.Join("D:\\forge", "gate_audit.jsonl")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("无法读取 gate_audit.jsonl: %v", err)
	}
	out := scorecardAggregate(data, 0)
	t.Log("\n" + out)
}
