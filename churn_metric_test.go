package main

// churn_metric_test.go — churn 度量段的判据 (20261018)。
//
// 背景: 「分形屎山」模型唯一能转成判据的洞见是「删除率是只堆不拆的早期信号」,
// 而全仓此前 0 处组件读 git numstat —— 信号存在, 但没有任何东西观测它。
// 本次接入 quality/churn_metric.py, 由 quality_report.py 消费。
//
// 判据三件事 (对齐 audit_consumer_test.go 的「埋点必须有消费者」三层):
//   1. 口径自检真的在跑: --churn-selftest 必须转发到 churn_metric.churn_selftest,
//      且**变异必被捕获** —— 只判 rc==0 挡不住「断言被删空后照样返回 0」;
//   2. 报告真的渲染: 端到端跑 quality_report.py, churn 段必须出现在报告里;
//   3. 接线是代码不是注释: quality_report.py 必须真的调用 churn_collect ——
//      模块存在但没接上, 与「没写」等价。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// churnRunReport 跑一次 quality_report.py, 返回合并输出。
func churnRunReport(t *testing.T, args ...string) (string, error) {
	t.Helper()
	py, err := exec.LookPath(guardGatePython())
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	full := append([]string{"-u", filepath.Join("quality", "quality_report.py")}, args...)
	cmd := exec.Command(py, full...)
	// CHURN_REQUIRE_OK=1: 在真实仓库里要求 git 口径必须真跑通 ——
	// 否则「采集命令被改坏 -> 解析为空 -> unavailable」会被当成环境问题放行。
	cmd.Env = append(pythonUTF8Env(), "CHURN_REQUIRE_OK=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestChurnSelfTestCatchesMutation 判据的判据: 破坏口径常量后自检必须报红。
//
// 为什么必须测变异: 自检函数若被删空, fails 恒空, 正常路径照样打印"全通过"且 rc=0
// —— 这是「判据失效」的第一形态(覆盖了开关存在, 没覆盖开关后面有东西)。
// 只有"故意破坏 -> 必须报 FAIL"能分辨断言是否还在。
func TestChurnSelfTestCatchesMutation(t *testing.T) {
	out, err := churnRunReport(t, "--churn-selftest")
	if err != nil {
		t.Fatalf("churn 正常自检应通过, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "组断言全通过") {
		t.Fatalf("自检输出不含通过标记 —— 转发可能失效:\n%s", out)
	}
	for _, mut := range []string{"archive", "exts"} {
		out, err := churnRunReport(t, "--churn-selftest", "--mutate="+mut)
		if err == nil {
			t.Fatalf("mutate=%s 未被捕获(rc=0) —— 对应断言已失效:\n%s", mut, out)
		}
		if !strings.Contains(out, "FAIL:") {
			t.Fatalf("mutate=%s 报红但无 FAIL 明细:\n%s", mut, out)
		}
	}
}

// TestChurnReportRenders churn 段必须真的出现在报告输出里。
//
// git 不可用时报告必须打印"不可用"而不是静默省略 —— 「不可用」与「0」
// 是两件事, 混同即假绿。
func TestChurnReportRenders(t *testing.T) {
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.jsonl")
	line := `{"event":"gate","ts":"2026-10-18T10:00:00+08:00","lang":"python",` +
		`"ok":true,"duration_ms":5,"code_len":3,"input_len":0,"retries":0}`
	if err := os.WriteFile(audit, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("写临时审计流: %v", err)
	}
	out, err := churnRunReport(t, "--audit", audit)
	if err != nil {
		t.Fatalf("quality_report.py 执行失败: %v\n%s", err, out)
	}
	if !strings.Contains(out, "churn 删除/新增比") {
		t.Fatalf("报告缺 churn 段 —— 采集没接进报告:\n%s", out)
	}
	// 二选一必须成立: 有月度数据行, 或显式声明不可用(含原因)。不许两者皆无。
	hasData := strings.Contains(out, "del/add")
	hasUnavail := strings.Contains(out, "不可用: ")
	if !hasData && !hasUnavail {
		t.Fatalf("churn 段既无数据也无不可用原因 —— 静默省略:\n%s", out)
	}
}

// TestChurnModuleIsWired 接线判据: quality_report.py 必须真的调用采集函数。
//
// 与 audit_consumer_test.go 同型: 文本里必须有调用字面量。仅存在模块文件不算接线。
func TestChurnModuleIsWired(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("quality", "quality_report.py"))
	if err != nil {
		t.Fatalf("读 quality_report.py: %v", err)
	}
	s := string(b)
	for _, want := range []string{"import churn_metric", "churn_metric.churn_collect()"} {
		if !strings.Contains(s, want) {
			t.Errorf("quality_report.py 缺 %q —— churn 模块存在但没接上", want)
		}
	}
	// 口径常量必须在模块里(而非散落在报告脚本), 否则口径不可复现
	mb, err := os.ReadFile(filepath.Join("quality", "churn_metric.py"))
	if err != nil {
		t.Fatalf("读 churn_metric.py: %v", err)
	}
	for _, want := range []string{"CHURN_EXTS", "CHURN_EXCLUDE", "_archive/"} {
		if !strings.Contains(string(mb), want) {
			t.Errorf("churn_metric.py 缺口径常量 %q", want)
		}
	}
}
