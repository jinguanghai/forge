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
	// mutation (20261004): 判据鉴别力探针 —— 已建成但不在调度里 = 只能手动跑。
	// 必需项 = --apply(否则退化成 dry-run, 只列清单不验证) + --rotate(否则每次跑全量,
	// 29 条 x 5s 且每次都要临时改生产源码)。步长具体值与覆盖周期上界由
	// mutation_rotate_sentinel_test.go 解析断言 —— 这里只钉「接线与必需参数存在」。
	"mutation": {"mutation_probe.py", "--apply", "--rotate"},
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
