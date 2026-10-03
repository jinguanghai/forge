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
	// Groups: 每个 glob 各自的份数 —— 判据按 glob 分组判定 (三类报告频率不同,
	// 共享一个上限会互相挤配额)。缺此字段 = 消费者与判据口径脱节, fail-closed。
	Groups map[string]int `json:"groups"`
	// Runs: 跑分目录事实 (题目目录数/体积) —— 判据的第二个容器 (十期新增)。
	Runs *struct {
		Tasks int `json:"tasks"`
		Bytes int `json:"bytes"`
	} `json:"runs"`
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
	// 分组口径: 判据按 glob 各自限份 (总数可以超过单类上限)。
	if len(f.Groups) == 0 {
		t.Fatal("判据未输出分组口径 (groups) —— 消费者与判据脱节即静默放行")
	}
	for glob, n := range f.Groups {
		if n > f.MaxCount {
			t.Errorf("bench/%s 报告 %d 份 > 上限 %d —— 日频产物无轮转会无界累积",
				glob, n, f.MaxCount)
		}
	}
	t.Logf("bench 报告 %d 份 / %.1f KB (每类上限 %d, 分组 %v)",
		f.Scanned, float64(f.Bytes)/1024, f.MaxCount, f.Groups)
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

// TestBenchReportGlobsCoverage 三类基准产物都必须进轮转判据。
//
// 动机 (教训「容器内部也要有判据」): 十期新增 semantics_*.md 与
// dataset_report_*.md 两类日频产物 —— 若判据仍只认 report_*.md,
// 新产物就是无人巡视的容器, 迟早无界累积 (与 .forge/forge-tools 同源)。
// 结构断言钉住 globs 列表本身: 单 glob 结构下多产物只能共享一个上限。
func TestBenchReportGlobsCoverage(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("defense_system/watchlist.py")
	if err != nil {
		t.Fatalf("读取 watchlist.py 失败 (fail-closed): %v", err)
	}
	s := string(raw)
	for _, pat := range []string{"report_*.md", "semantics_*.md", "dataset_report_*.md"} {
		if !strings.Contains(s, pat) {
			t.Errorf("BENCH_LIMITS 缺 %s —— 该产物无轮转判据 (日频产物必然无界累积)", pat)
		}
	}
	if !strings.Contains(s, `"globs":`) {
		t.Error(`BENCH_LIMITS 应使用 "globs" 列表: 多产物共享单 glob 上限会互相挤配额`)
	}
}

// TestBenchRunsLimitsWired 跑分目录判据必须接线 (十期)。
//
// 动机: bench/runs 是跑分产物容器 (每题 12.5 KB 的运行时产物), 无人巡视就会
// 无界累积 —— 与 bench/report_*.md 同源。判据存在 ≠ 被消费, 因此三处都要钉:
//
//	① 判据在单一数据源里 (watchlist.BENCH_RUNS_LIMITS)
//	② 脚本真的消费它 (bench_report_check.py 引用)
//	③ 消费结果真的出现在输出里 (--json 的 runs 段非空)
func TestBenchRunsLimitsWired(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("defense_system/watchlist.py")
	if err != nil {
		t.Fatalf("读取 watchlist.py 失败 (fail-closed): %v", err)
	}
	if !strings.Contains(string(raw), "BENCH_RUNS_LIMITS") {
		t.Error("watchlist.py 缺 BENCH_RUNS_LIMITS —— 跑分产物无判据")
	}
	src, err := os.ReadFile(benchReportRel)
	if err != nil {
		t.Fatalf("读取 %s 失败 (fail-closed): %v", benchReportRel, err)
	}
	if !strings.Contains(string(src), "BENCH_RUNS_LIMITS") {
		t.Errorf("%s 未消费 BENCH_RUNS_LIMITS —— 判据有而处置臂缺", benchReportRel)
	}
	f := benchFactsOf(t)
	if f.Runs == nil {
		t.Fatal("--json 未输出 runs 段 —— 判据未被消费")
	}
	t.Logf("跑分目录: %d 个题目目录 / %.1f KB", f.Runs.Tasks, float64(f.Runs.Bytes)/1024)
}
