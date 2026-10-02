package main

// ============================================================================
// bench_report_test.go —— 基准报告轮转判据的接线哨兵
//
// 缺陷 (20260928 实测):
//   bench/report_*.md 按日累积至 35 份 (08-17 ~ 09-28), 此前【无任何轮转判据】。
//   它与 .forge/ 和 snapshots/ 是同一类问题: 容器只要无人巡视, 迟早变成仓库;
//   日频产物尤其如此 —— 每天 +1 份, 无人喊停。
//
// 修法: 判据单一数据源 watchlist.BENCH_LIMITS, Python 侧 bench_report_check.py
// 消费, hourly 第 11 任务调度; 本文件把三件事钉死(接线只能由死程序判定):
//   1. 份数不超上限 (跑真实脚本, 拿真实 JSON)
//   2. 判据向量全通过 (--selftest, 判据本身可测)
//   3. 判据来自单一数据源 (watchlist.BENCH_LIMITS, 不得有第二份清单)
//
// 为什么是 Go 测试而非 Python 自检: 接线/归属是包级属性, 必须进入 go test
// 回归才有人跑; Python 侧自检函数无人调用就是「备而未用」。
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const benchReportRel = "defense_system/bench_report_check.py"

// benchFacts 是 bench_report_check.py --json 输出的事实子集。
type benchFacts struct {
	Violations []struct {
		Rel string `json:"rel"`
		Why string `json:"why"`
	} `json:"violations"`
	Scanned  int `json:"scanned"`
	MaxCount int `json:"max_count"`
	Bytes    int `json:"bytes"`
}

// benchFactsOf 跑真实脚本拿 JSON 事实 (fail-closed: 跑不起来即 Fatal)。
func benchFactsOf(t *testing.T) benchFacts {
	t.Helper()
	cmd := exec.Command(guardGatePython(), benchReportRel, "--json")
	// PYTHONDONTWRITEBYTECODE: 避免探针在 defense_system/ 留下 __pycache__
	// (那是被 EXCLUDE 的目录, 哨兵看不见 -> 探针自己制造盲区垃圾)。
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bench_report_check.py --json 执行失败 (fail-closed, 不放行): %v\n%s", err, out)
	}
	var f benchFacts
	// 取【第一个】'{' 起的内容: JSON 内含嵌套对象, 用 LastIndex 会从嵌套的
	// '{' 处截断 -> "invalid character ',' after top-level value" (实测踩过)。
	body := out
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		body = out[i:]
	}
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("探针输出不是合法 JSON (fail-closed): %v\n%s", err, out)
	}
	return f
}

// TestBenchReportsWithinLimit 钉住份数不超上限 (硬判据)。
func TestBenchReportsWithinLimit(t *testing.T) {
	t.Parallel()
	f := benchFactsOf(t)
	if f.MaxCount <= 0 {
		t.Fatalf("max_count 非法: %d —— 判据缺失会让判定静默变成「全部放行」", f.MaxCount)
	}
	if len(f.Violations) != 0 {
		t.Errorf("基准报告违规 %d 项: %+v", len(f.Violations), f.Violations)
	}
	if f.Scanned > f.MaxCount {
		t.Errorf("bench 报告 %d 份 > 上限 %d —— 日频产物无轮转会无界累积",
			f.Scanned, f.MaxCount)
	}
	t.Logf("bench 报告 %d 份 / %.1f KB (上限 %d)", f.Scanned, float64(f.Bytes)/1024, f.MaxCount)
}

// TestBenchReportSelftest 判据向量必须全通过 —— 判据本身可测, 不靠"改坏真文件"。
func TestBenchReportSelftest(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(guardGatePython(), benchReportRel, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--selftest 失败 (判据向量不符): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0 失败") {
		t.Errorf("selftest 未报「0 失败」: %s", out)
	}
}

// TestBenchReportSingleSource 判据必须来自 watchlist (两处各写一份必然漂移)。
func TestBenchReportSingleSource(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("defense_system/watchlist.py")
	if err != nil {
		t.Fatalf("读取 watchlist.py 失败 (fail-closed): %v", err)
	}
	if !strings.Contains(string(raw), "BENCH_LIMITS") {
		t.Errorf("watchlist.py 缺 BENCH_LIMITS —— 判据单一数据源失效")
	}
	src, err := os.ReadFile(benchReportRel)
	if err != nil {
		t.Fatalf("读取 %s 失败 (fail-closed): %v", benchReportRel, err)
	}
	if !strings.Contains(string(src), "from watchlist import BENCH_LIMITS") {
		t.Errorf("%s 未从 watchlist import 判据 —— 存在第二份清单", benchReportRel)
	}
}
