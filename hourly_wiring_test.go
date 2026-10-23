package main

// ============================================================================
// hourly_wiring_test.go —— hourly.py 任务接线哨兵 (接线只能由死程序判定)
//
// 为什么单独成文件 (20260928):
//   此前接线断言长在 hygiene_forge_test.go 里, 且只断言 'forge' 一个任务 ——
//   判据写死单条目, 新增任务时哨兵不会提醒。
//   教训「判据/哨兵必须扫全包, 禁写死单文件」已复发 7 次。
//   本文件把判据升级为「全任务表」: wantHourlyTasks 就是调度清单本身,
//   任务增删或指向变化 -> go test 立刻红。
//
// 判据是「代码行」不是「字符串包含」(实测教训):
//   解释接线理由的注释里同样含脚本名 —— 用全文件 strings.Contains 会漏检:
//   实测删掉 TASKS 里的接线行(保留注释)时, 哨兵静默通过。
//   所以先剥注释行, 再解析元组。
// ============================================================================

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// hourlyTaskRe 解析 TASKS 条目: ('name', [cmd...]),
// 须匹配 ']),' 而非 '],' —— 第一版漏了括号, 实测解析出 0 条任务。
var hourlyTaskRe = regexp.MustCompile(`^\('([a-z_]+)',\s*\[(.*)\]\),?$`)

// wantHourlyTasks = 期望的调度清单 (任务名 -> 脚本文件名 + **必需参数**)。
// 这就是判据表: 增删任务/改参数必须同步改这里, 否则测试红 —— 「接线落地」的钉死方式。
//
// 为什么带参数 (20261001): 原判据只记脚本名, 于是「参数」是判据盲区 ——
// 把 cleanup/hygiene/temp 的 --apply 删掉(退化成 dry-run)或把 archive 的 --apply
// 删掉(退化成只读), 哨兵全绿, 而调度实际已「备而未用」。判据必须覆盖到参数这一层。
var wantHourlyTasks = map[string][]string{
	"guard":    {"forge_guard.py", "check"},
	"watchdog": {"forge_watchdog.py"},
	"fractal":  {"fractal_check.py"},
	"cleanup":  {"cleanup_backups.py", "--apply"},
	"hygiene":  {"hygiene_redcard.py", "--apply"},
	"archive":  {"hygiene_redcard.py", "--archive", "--apply"},
	"forge":    {"hygiene_forge_check.py"},
	"temp":     {"forge_temp_check.py", "--apply"},
	"snapshot": {"hygiene_snapshot_check.py"},
	// hardcode (20261008, T27): 生产代码禁盘符硬编码哨兵的调度臂 ——
	// 判据是 Go 测试 (hardcode_path_sentinel_test.go), 调度臂是 .py 包装 (与 semantics 同型)。
	"hardcode": {"hardcode_path_check.py"},
	// pyundef (20261008, T28-②): Python 未定义全局名静态哨兵 ——
	// 「语法检查全绿、执行到那一行才炸」的缺陷形态 (本轮 acceptance_sheet 的 BASE)。
	"pyundef": {"py_undef_check.py"},
	// vpsenv (20261008, T28-①): 配置单一数据源的行为自检 (同批修了环境变量未隔离 -> 恒红)。
	"vpsenv": {"vps_env.py"},
	// snapgen (20261005): 快照【生成臂】—— 判据(snapshot)只读, 生成此前只挂人工按钮;
	// --apply 缺失 = 退化成 dry-run (与 archive/cleanup/bench 的教训同型: 参数是判据盲区)。
	"snapgen": {"snapshot_refresh.py", "--apply"},
	"ignored": {"hygiene_ignored_check.py"},
	// bench (20261004): 判据 + 处置臂 —— 报告是日频产物, 只读判据贴边后永久报红;
	// --apply 缺失 = 退化成只报告 (与 archive/cleanup 的教训同型: 参数是判据盲区)。
	"bench":   {"bench_report_check.py", "--apply"},
	"version": {"version_check.py"},
	"webgw":   {"web_gateway_check.py"},
	// semantics (20261003): gate 语义基准 —— 判据表带脚本名即钉住接线;
	// 它跑的是 go test (非 .py), 因此调度臂是 .py 包装 (TestHourlyScriptsExist 才能覆盖)。
	"semantics": {"bench_semantics_check.py"},
	// secret (20261003): 密钥泄露自检 —— 收窄扫描范围后接入每小时巡检。
	"secret": {"secret_scan.py"},
	// vpslogin (20261003): VPS 登录来源硬判据 (反制体系唯一零误报信号);
	// 网络不可达 rc=2 不判违规, 故判据只需钉住脚本名 (无必需参数)。
	"vpslogin": {"vps_login_check.py"},
	// dep (20261004): 外部依赖自检 —— 判据脚本名即钉住接线(无必需参数)。
	// 与之配对的 Go 哨兵 = deps_sentinel_test.go (go.mod 零第三方 / requirements 钉版本 /
	// gate 产物与源码一致), 两处同源不同档: .py 进每小时巡检, Go 哨兵进 go test。
	"dep": {"dep_check.py"},
	// membudget (20261012, DG-3 L3): 记忆预算巡检 —— 常驻字符/折叠索引有效性/池子上限。
	// 判据与 Go 侧 memory_budget.go 同源; 无必需参数 (脚本自身即完整判据)。
	"membudget": {"memory_budget.py"},
	// mutation (20261004): 判据鉴别力探针 —— 已建成但不在调度里 = 只能手动跑。
	// 必需项 = --apply(否则退化成 dry-run, 只列清单不验证) + --rotate(否则每次跑全量,
	// 29 条 x 5s 且每次都要临时改生产源码)。步长具体值与覆盖周期上界由
	// mutation_rotate_sentinel_test.go 解析断言 —— 这里只钉「接线与必需参数存在」。
	"mutation": {"mutation_probe.py", "--apply", "--rotate"},
	// ledger (20261005, J5): 判据台账新鲜度 —— 新增判据未登记维度即报红。
	// --check 是必需项: 缺了退化成「重新生成并覆盖」, 过期检测失效 (参数是判据盲区)。
	"ledger": {"judgement_ledger.py", "--check"},
	// drift (20261005, J6/J7): 漂移维阈值的元判据 —— 变松/超限幅即报红;
	// 基线 drift_baseline.json + 限幅+不对称迟滞 (往更松必须过人)。
	"drift": {"drift_gate.py", "--check"},
	// gitauto (20261009): 自动存档兜底臂 —— 收尾存档在会话被强杀时不执行, 每小时补一次。
	// 脚本只调 forge.exe --git-autosave (逻辑单一数据源在 git_autosave.go)。
	"gitauto": {"git_autosave_hourly.py"},
	// ossdrift (20261006 T14 上线, 20261007 停用, 20261023 恢复): 开源仓同步漂移判据。
	// --check 是必需项 (缺了退化成默认动作, 语义含糊); 判据与处置臂
	// open_source_sync.ps1 必须同 scope, 两处改一处即失配 (drift 判据会永久报红)。
	"ossdrift": {"open_source_drift.py", "--check"},
}

// parseHourlyTasks 解析 defense_system/hourly.py 的 TASKS 表。
// 只认代码行 —— 注释行不算接线 (元语境)。
func parseHourlyTasks(t *testing.T, raw string) map[string]string {
	t.Helper()
	tasks := map[string]string{}
	inTasks := false
	for _, ln := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(ln)
		if !inTasks {
			if strings.HasPrefix(trimmed, "TASKS = [") {
				inTasks = true
			}
			continue
		}
		if trimmed == "]" {
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := hourlyTaskRe.FindStringSubmatch(trimmed); m != nil {
			tasks[m[1]] = trimmed
		}
	}
	if len(tasks) == 0 {
		t.Fatal("未能解析出任何 TASKS 条目 —— 哨兵失效 (hourly.py 结构变了?)")
	}
	return tasks
}

func hourlyTaskNames(tasks map[string]string) string {
	var ns []string
	for n := range tasks {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return strings.Join(ns, ",")
}

// TestHourlyTasksWired 钉住调度清单: 任务齐全 + 指向正确 + 无未登记任务。
func TestHourlyTasksWired(t *testing.T) {
	raw, err := os.ReadFile("defense_system/hourly.py")
	if err != nil {
		t.Fatalf("hourly.py 读取失败 (fail-closed): %v", err)
	}
	tasks := parseHourlyTasks(t, string(raw))
	for name, want := range wantHourlyTasks {
		if len(want) == 0 {
			t.Errorf("判据表 %q 为空 —— 判据失效", name)
			continue
		}
		cmd, ok := tasks[name]
		if !ok {
			t.Errorf("任务未接线: %q (已解析 %d 个: %s)", name, len(tasks), hourlyTaskNames(tasks))
			continue
		}
		for _, tok := range want {
			if !strings.Contains(cmd, tok) {
				t.Errorf("任务 %q 缺少必需项 %q (脚本名或参数): %s", name, tok, cmd)
			}
		}
	}
	if len(tasks) != len(wantHourlyTasks) {
		t.Errorf("TASKS 条目数 %d != 判据表 %d —— 新增任务须同步登记判据 (已解析: %s)",
			len(tasks), len(wantHourlyTasks), hourlyTaskNames(tasks))
	}
	t.Logf("hourly 已接线 %d 个任务: %s", len(tasks), hourlyTaskNames(tasks))
}

// TestHourlyScriptsExist 钉住「接线指向的文件真实存在」——
// 指向不存在的脚本 = 静默失效: 调度器每次都失败, 但没人看日志。
func TestHourlyScriptsExist(t *testing.T) {
	for name, want := range wantHourlyTasks {
		if len(want) == 0 {
			continue
		}
		p := filepath.Join("defense_system", want[0])
		if _, err := os.Stat(p); err != nil {
			t.Errorf("任务 %q 指向的脚本不存在: %s (%v)", name, p, err)
		}
	}
}

// ---- T59-① 日志粒度判据的接线 (20261016) ----
//
// 背景: defense_system/hourly.py 的环形日志曾混用两种粒度 —— 旧数据来自
// read().split('\n') (元素 = 文本行), 新数据来自逐任务 append(out) (元素 = 整块输出)。
// 实测后果: KEEP=200 对旧数据是「200 行」、对新数据是「200 块」, 每跑一轮把上一轮
// 200 行砍到 ~121 行, 最老运行还被从块中间切断 (实测日志首块起于第 3 行)。
//
// 修法 = merge_log 统一摊平成行。本判据钉住四件事 (注释里提及不算, 先剥注释):
//  1) 粒度统一入口 merge_log 存在
//  2) 旧写法 (old + lines)[-KEEP:] 不得回归
//  3) 自检 selftest 存在, 且在 main() 里 fail-closed 可达 (判据不在生产路径上 = T53 的坑)
//  4) main() 提供 --selftest 入口 (否则自检只能靠读代码, 没人跑)
//
// 为什么是 Go 判据而不是只靠 Python selftest: selftest 是判据的判据 (向量在 hourly.py 内),
// 本判据钉的是「那套自检有没有接线、旧写法有没有偷偷回来」—— 两者是不同层的防线。

// pyCodeLines 剥 Python 整行注释 (# 开头) —— 注释里提到旧写法不算回归。
func pyCodeLines(src string) string {
	var b strings.Builder
	for _, ln := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String()
}

// pyFuncBody 取 Python 函数体 (从 def 名( 到下一个顶层 def 或文件尾) —— 纯函数, 便于自证。
// 用途: 判「append_log 真的调了 merge_log」「merge_log 真的调了 flatten_lines」,
// 只查函数存在不够 —— 存在但被绕过的入口 = 判据形同虚设 (T53 的形态)。
func pyFuncBody(code, def string) string {
	i := strings.Index(code, def)
	if i < 0 {
		return ""
	}
	rest := code[i+len(def):]
	if j := strings.Index(rest, "\ndef "); j >= 0 {
		return rest[:j]
	}
	return rest
}

// hourlyLogGranularityProblem 判定 hourly.py 的日志粒度判据是否接线 (纯函数, 空串 = 合格)。
func hourlyLogGranularityProblem(src string) string {
	code := pyCodeLines(src)
	if !strings.Contains(code, "def merge_log(") {
		return "缺少 merge_log: 日志粒度统一入口不存在"
	}
	if !strings.Contains(code, "def flatten_lines(") {
		return "缺少 flatten_lines: 行/块摊平入口不存在"
	}
	if strings.Contains(code, "(old + lines)[-KEEP:]") {
		return "旧写法 (old + lines)[-KEEP:] 回归: 粒度混用会把上一轮日志从块中间切断"
	}
	if body := pyFuncBody(code, "def append_log("); body == "" || !strings.Contains(body, "merge_log(") {
		return "append_log 未使用 merge_log: 粒度统一入口被绕过 (直接切片淘汰 = 粒度错位回归)"
	}
	if body := pyFuncBody(code, "def merge_log("); body == "" || !strings.Contains(body, "flatten_lines(") {
		return "merge_log 未调用 flatten_lines: 新数据未摊平, 粒度错位回归"
	}
	if !strings.Contains(code, "def selftest(") {
		return "缺少 selftest: 判据没有判据"
	}
	i := strings.Index(code, "def main(")
	if i < 0 {
		return "找不到 def main( —— hourly.py 结构变了?"
	}
	body := code[i:]
	for _, want := range []string{"selftest()", "--selftest", "return 2"} {
		if !strings.Contains(body, want) {
			return "main() 缺少 " + want + ": 自检必须 fail-closed 且可显式调用"
		}
	}
	return ""
}

func TestHourlyLogGranularityWired(t *testing.T) {
	b, err := os.ReadFile("defense_system/hourly.py")
	if err != nil {
		t.Fatalf("hourly.py 读取失败 (fail-closed): %v", err)
	}
	if prob := hourlyLogGranularityProblem(string(b)); prob != "" {
		t.Error(prob)
	}
	t.Logf("hourly.py 日志粒度判据已接线: merge_log + selftest + main fail-closed")
}

// TestHourlyLogGranularitySelfCheck 是判据自身的判据:
// 变异向量必须被检出 (否则「旧写法不得回归」恒绿), 注释向量必须判绿 (否则剥注释失效即假红)。
func TestHourlyLogGranularitySelfCheck(t *testing.T) {
	const good = "def flatten_lines(lines):\n    pass\ndef merge_log(a, b):\n    return flatten_lines(b)\ndef append_log(lines):\n    return merge_log('', lines)\ndef selftest():\n    pass\ndef main():\n    selftest()\n    return 2\n    x = '--selftest'\n"
	if prob := hourlyLogGranularityProblem(good); prob != "" {
		t.Errorf("合规样本被误判: %s", prob)
	}
	if prob := hourlyLogGranularityProblem(good + "def append_log(lines):\n    return (old + lines)[-KEEP:]\n"); prob == "" {
		t.Error("旧写法样本未报红 —— 判据无鉴别力")
	}
	if prob := hourlyLogGranularityProblem(strings.Replace(good, "    selftest()\n", "", 1)); prob == "" {
		t.Error("main 未调用 selftest 的样本未报红 —— 接线判据失效")
	}
	if prob := hourlyLogGranularityProblem("# 旧写法 (old + lines)[-KEEP:] 已废弃\n" + good); prob != "" {
		t.Errorf("注释里提及旧写法被误判为回归 (剥注释失效): %s", prob)
	}
	// 「入口存在但被绕过」必须被检出 —— 只查 def 存在会让判据形同虚设。
	bypass := strings.Replace(good, "    return flatten_lines(b)\n", "    return []\n", 1)
	if prob := hourlyLogGranularityProblem(bypass); prob == "" {
		t.Error("merge_log 未调用 flatten_lines 的样本未报红 —— 判据只看函数存在, 无鉴别力")
	}
	bypass2 := strings.Replace(good, "    return merge_log('', lines)\n", "    return (old + lines)[-KEEP:]\n", 1)
	if prob := hourlyLogGranularityProblem(bypass2); prob == "" {
		t.Error("append_log 绕过 merge_log 的样本未报红 —— 粒度错位可回归而无人知")
	}
}
