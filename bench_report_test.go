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

// TestBenchRotationApplyWired 轮转处置臂必须接线 (20261004)。
//
// 动机: bench_report_check.py 是**只读判据**, 报红之后没有任何处置动作 ——
// 报告是日频产物 (每天首跑 +1), 判据上限 14 份, 贴边后**永久报红**
// (实测 2026-10-04 当天已 15 份, 靠人工每天手移一份顶着)。
// 红牌常亮 = 告警疲劳, 真告警会被淹没 (与 archive 同型事故: 连续 14 条/小时无人看)。
// 判据存在 ≠ 被消费 ≠ 被调度, 三处都要钉:
//
//	① 判据脚本真的消费处置臂 (源码引用 bench_report_apply)
//	② 处置臂的沙箱用例真跑通 (TestBenchRotationSim: 造 15 份 -> 轮转 -> 幂等 -> 回滚)
//	③ 调度带 --apply (由 hourly_wiring_test.go 的判据表钉住, 缺参数即报红)
func TestBenchRotationApplyWired(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(benchReportRel)
	if err != nil {
		t.Fatalf("读取 %s 失败 (fail-closed): %v", benchReportRel, err)
	}
	s := string(src)
	if !strings.Contains(s, "bench_report_apply") {
		t.Errorf("%s 未接线处置臂 —— 判据只读 = 贴边后永久报红", benchReportRel)
	}
	if !strings.Contains(s, "_apply_rotation(") {
		t.Errorf("%s 缺处置调用点 (_apply_rotation) —— 判据与处置未闭环", benchReportRel)
	}
	if !strings.Contains(s, "--apply") {
		t.Errorf("%s 缺 --apply 开关 —— 默认只读语义要保留, 但处置入口必须存在", benchReportRel)
	}
	if _, err := os.Stat("defense_system/bench_report_apply.py"); err != nil {
		t.Fatalf("处置臂脚本不存在 (接线指向空气): %v", err)
	}
	// 处置臂自身也要有判据向量 (判据自身也要有判据)。
	arm, err := os.ReadFile("defense_system/bench_report_apply.py")
	if err != nil {
		t.Fatalf("读取处置臂失败 (fail-closed): %v", err)
	}
	for _, want := range []string{"def selftest()", "def rollback(", "MAX_ITEMS", "--rollback"} {
		if !strings.Contains(string(arm), want) {
			t.Errorf("处置臂缺 %q —— 四重护栏 (只移不删/熔断/可回滚/幂等) 不完整", want)
		}
	}
}

// TestBenchRotationSim 沙箱端到端: 造 15 份 -> 判据报红 -> --apply 轮转 ->
// 幂等 -> rollback 逐字节还原。
//
// 为什么必须实跑而非观察: 生产里「份数超限 -> 轮转」要等日频产物攒满才走到,
// 而它是唯一会移动文件的路径 (报告可能是唯一副本), 出事代价最高。
// 真实根绝不参与: 沙箱根由 FORGE_BENCH_ROOT 注入, 且跑 --apply 前有 preflight
// 闸门确认注入生效 (解析失败则 fail-closed 退出)。
func TestBenchRotationSim(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(guardGatePython(), "defense_system/bench_report_apply.py", "--sim")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("沙箱用例失败 (fail-closed): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "失败 0 项") {
		t.Errorf("沙箱用例未报「失败 0 项」:\n%s", out)
	}
	if strings.Contains(string(out), "FAIL") {
		t.Errorf("沙箱用例有 FAIL 行:\n%s", out)
	}
	t.Logf("沙箱输出:\n%s", out)
}

// ── T39 (20261010): 生成端「落盘后立即轮转」的接线 ──────────────────────────

// rotateBenchReports 生成端落盘后的轮转接线。
//
// 缺陷 (T39 实测): 判据 (本文件 TestBenchReportsWithinLimit) 实时读目录,
// 而处置 (bench_report_check.py --apply) 由 hourly 按小时跑 —— 日报 19:39 生成
// 把份数顶到 15, 下一次轮转尚未跑, 这个窗口期内任何全量测试必红。
// 根因不是「阈值不对」, 是判据与处置的时间错位: 判据即时, 处置小时级。
//
// 为什么是「落盘后」而不是「落盘前」(TODO T39 倾向①的原文是"先轮转再落盘"):
//
//	--apply 的语义是「把份数压回上限」, 不是「压到上限-1」。
//	先轮转: 15 -> 14, 再落盘 -> 又回到 15, 判据照红 —— 顺序反了治不了病。
//	落盘后: 15 -> 14, 判据读时恒 <= 上限。
//
// 为什么 Go 只做接线、不自己实现轮转: 轮转逻辑与阈值只有一份
// (watchlist.BENCH_LIMITS + bench_report_apply.py); 复制一份必然漂移,
// 漂移即「清理阈值与实际不符」。
//
// 副作用: 会移动 bench/ 最旧报告到 _archive/ (与 hourly 同一动作: 幂等 /
// 只移不删 / 可 --rollback)。TestBench 本来就写真实 bench/ 目录 (报告是真实
// 产物而非测试夹具), 故此处不做沙箱隔离; 沙箱端到端由 TestBenchRotationSim 覆盖。
//
// 失败不 fail 测试: 处置是 best-effort, 判据仍独立报红 —— 处置失败不得把
// 判据一起带绿 (检测与处置分权)。
func rotateBenchReports(t *testing.T, why string) {
	t.Helper()
	cmd := exec.Command(guardGatePython(), benchReportRel, "--apply")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("[T39] bench 轮转(%s)未成功, 交 hourly/判据处置: %v\n%s", why, err, out)
		return
	}
	t.Logf("[T39] bench 轮转(%s): %s", why, strings.TrimSpace(string(out)))
}

// TestBenchRotationWiredToGenerators 钉住「生成端真的会轮转」(T39)。
//
//	① 行为: 接线可跑通, 且跑完份数 <= 上限 / 违规 0 (幂等: 未超限时不动 ——
//	   若它反而制造违规, 这条会红)
//	② 接线: 两个生成端落盘段都调用它, 剥注释后断言 (注释里提一句不算接线,
//	   这是实测过的漏检形态)
//
// 为什么 ② 只能读源码文本: 「调用点存在」是包级接线属性, 轮转幂等时没有
// 任何运行时可观测信号 (跑与不跑输出相同) —— 无信号可判, 只能静态钉住。
func TestBenchRotationWiredToGenerators(t *testing.T) {
	rotateBenchReports(t, "接线自检")
	f := benchFactsOf(t)
	if len(f.Violations) != 0 {
		t.Errorf("轮转后仍有违规: %+v", f.Violations)
	}
	for g, n := range f.Groups {
		if n > f.MaxCount {
			t.Errorf("轮转后 %s 仍超上限: %d > %d", g, n, f.MaxCount)
		}
	}
	for _, rel := range []string{"bench_test.go", "bench_semantics_test.go"} {
		src, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("读 %s 失败 (fail-closed): %v", rel, err)
		}
		if !strings.Contains(stripComments(string(src)), "rotateBenchReports(") {
			t.Errorf("%s 落盘段未调用 rotateBenchReports —— 生成端不轮转, 判据与处置的时间错位会复发", rel)
		}
	}
}

// ── 语义基准调度臂的「判据的判据」(20261012) ────────────────────────────────

// benchSemanticsCheckRel 是 gate 语义基准的调度臂 (hourly 任务 semantics)。
const benchSemanticsCheckRel = "defense_system/bench_semantics_check.py"

// TestBenchSemanticsCheckSelftest 钉住调度臂的失败项解析口径。
//
// 缺陷 (20261012 实测): bench_semantics_check.py 把 go test -v 输出里**所有**
// `bench_semantics_test.go:` 前缀且含 `]` 的行当失败项 —— 而 -v 下 t.Logf 与
// t.Errorf 的行格式完全相同, 于是【通过测试的普通日志】被展示成失败项:
// hourly 每次打印「失败项: [T39] bench 轮转(TestBenchSemantics): 基准报告:
// 25 份 / 38.2 KB」, 而同一份输出里 rc=0 且 41/41 通过 —— 长期假红 = 狼来了,
// 真失败会被淹掉 (检测层正确, 展示层说谎)。修法=按 `=== RUN` / `--- FAIL`
// 分块归属, 只有以 `--- FAIL` 收尾的块内的行才算失败行。
//
// 为什么要 Go 侧再钉一道: 口径的判据 (Python --selftest) 无人调用即
// 「备而未用」—— 接线/归属是包级属性, 必须进 go test 回归才有人跑。
func TestBenchSemanticsCheckSelftest(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(guardGatePython(), benchSemanticsCheckRel, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--selftest 失败 (失败项解析口径不符): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0 失败") {
		t.Errorf("selftest 未报「0 失败」: %s", out)
	}
}
