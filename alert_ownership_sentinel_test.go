package main

// ── alert_ownership_sentinel_test.go — 告警「归属」与「抢跑」判据 (20261013) ──
//
// 事故 A (越权抹除, 实测): forge_watchdog 的 WATCH_PREFIXES 含 'cache_', 而
//   cache_guard.py 的 metric 是 cache_guard_below_target —— 前缀包含关系。
//   watchdog 每小时一跑, 就把别人 8 秒前刚报的告警判成「未复现 = 已恢复」:
//   10-13 17:45:59 报, 17:46:07 被抹 (存活 8 秒); 10-08 同型 (存活 19 分钟)。
//
// 事故 B (抢跑): 缓存判据拿当天不完整数据判「当天是否达标」——
//   10-03 当天 报→恢复→报→恢复 抖动 2 轮; 10-08 14:44 报 81.99%, 23:44 自愈
//   (全天实为 90.17%, 本就达标); 10-13 00:44 报 61.48% (当时只有 00 时那 168 次
//   断裂请求), 全天实为 83.01%。
//
// 判据为什么必须是死程序: 「谁该结清谁的告警」是跨文件的归属关系, 注释里写清楚
//   不构成约束 —— 改一行前缀即静默失效, 且失效是无声的。
//
// 判据是「代码行」不是「字符串包含」(沿用 hourly_wiring_test.go 的教训):
//   先剥 # 整行注释再断言 —— 解释理由的注释里同样含 'cache_guard_' 字样。

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pyCode 返回剥掉整行注释 (# 开头) 后的 Python 源码。
// 只剥整行注释, 不剥行内注释 —— 断言目标都是独立代码行。
func pyCode(t *testing.T, name string) string {
	t.Helper()
	var out []string
	for _, ln := range strings.Split(readRepoFile(t, name), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// TestAlertSink_ExcludePrefixesWired 共享层必须支持「显式排除」, 且真正参与判定。
func TestAlertSink_ExcludePrefixesWired(t *testing.T) {
	code := pyCode(t, "defense_system/alert_sink.py")
	for _, want := range []string{
		"def open_keys(alerts_path, prefixes, exclude_prefixes=()):",
		"if exclude_prefixes and any(m.startswith(x) for x in exclude_prefixes):",
		"exclude_prefixes=()):",
		"gone |= (open_keys(alerts_path, prefixes, exclude_prefixes) - seen)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("alert_sink.py 缺 %q —— 排除机制被改坏, 越权抹除会复发", want)
		}
	}
}

// TestAlertSink_SelftestHasOverlapCase selftest 必须含「前缀包含关系」用例及其对照组。
// 对照组不可省: 没有它, 用例可能因「反正不会清」而空转 (判据自身要有判据)。
func TestAlertSink_SelftestHasOverlapCase(t *testing.T) {
	code := pyCode(t, "defense_system/alert_sink.py")
	for _, want := range []string{
		"前缀包含关系: 排除项不得被清除",
		"对照组: 不排除时确实越界清除(证明用例非空转)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("alert_sink selftest 缺 %q —— 判据覆盖出现空洞", want)
		}
	}
}

// TestWatchdog_ExcludesOthersMetrics watchdog 必须显式声明「哪些不是我的」。
func TestWatchdog_ExcludesOthersMetrics(t *testing.T) {
	code := pyCode(t, "defense_system/forge_watchdog.py")
	if !strings.Contains(code, "WATCH_EXCLUDE_PREFIXES = ('cache_guard_',)") {
		t.Error("forge_watchdog.py 缺 WATCH_EXCLUDE_PREFIXES 声明")
	}
	if !strings.Contains(code, "exclude_prefixes=WATCH_EXCLUDE_PREFIXES,") {
		t.Error("forge_watchdog.py 的 emit 未传 exclude_prefixes —— 接线断了(声明了不用)")
	}
}

// TestCacheGuardMetrics_UnderWatchdogPrefix 元判据: 排除项必须真实必要。
// 'cache_' 确实覆盖 cache_guard.py 的 metric —— 若哪天它改名到别的前缀下,
// 本判据会红, 提示那条排除已多余 (判据自身区分度自检)。
func TestCacheGuardMetrics_UnderWatchdogPrefix(t *testing.T) {
	code := pyCode(t, "cache_guard.py")
	metrics := []string{"cache_guard_below_target", "cache_guard_dropped", "cache_guard_prefix_changed"}
	found := 0
	for _, m := range metrics {
		if strings.Contains(code, "'"+m+"'") {
			found++
			if !strings.HasPrefix(m, "cache_") {
				t.Errorf("元判据失效: %s 不再以 cache_ 开头, 排除已多余", m)
			}
		}
	}
	if found == 0 {
		t.Error("cache_guard.py 里找不到任何已知 metric 名 —— 判据扫描目标已漂移")
	}
}

// TestWatchdog_CacheJudge_NotToday 缓存判据不得判「今天」(当天数据不完整 = 抢跑)。
func TestWatchdog_CacheJudge_NotToday(t *testing.T) {
	code := pyCode(t, "defense_system/forge_watchdog.py")
	if !strings.Contains(code, "today = tnow.date().isoformat()") {
		t.Error("forge_watchdog.py 缺 today 定义")
	}
	if !strings.Contains(code, "and d < today]") {
		t.Error("缓存判据未排除当天 —— 抢跑会复发 (10-03 当天抖动 2 轮)")
	}
}

// TestAlertSink_HoldSubstringsWired 判据未覆盖的周期不得被撤 (hold_substrings 接线)。
// 实测 20261013 20:44: 缓存判据改成「只判已结束的日」后, 当天的 cache_hit_rate /
// cache_drop 立刻被判「未复现 = 已恢复」撤掉 —— 而判据根本没评估当天。
// 撤告警 = 对外宣称「已恢复」, 实际是「未评估」, 名实不符。
func TestAlertSink_HoldSubstringsWired(t *testing.T) {
	code := pyCode(t, "defense_system/alert_sink.py")
	for _, want := range []string{
		"hold_substrings=()):",
		"def _held(fp):",
		"if fp not in seen and not _held(fp)}",
		"if hold_substrings:",
		"gone = {fp for fp in gone if not _held(fp)}",
		"判据未覆盖的周期: 告警不得被撤",
		"对照组: 不 hold 时确实撤(证明用例非空转)",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("alert_sink.py 缺 %q —— 未评估周期会被误判「已恢复」", want)
		}
	}
	wd := pyCode(t, "defense_system/forge_watchdog.py")
	if !strings.Contains(wd, "hold_substrings=(tnow.date().isoformat(),)") {
		t.Error("forge_watchdog.py 的 emit 未传 hold_substrings —— 当天告警会被误撤")
	}
}

// ── T54 (20261015): 告警「证据与判定同源」判据 ──
//
// 事故: forge_watchdog.check_cache 的 detail 取用了上方 for 循环的残留变量 a ——
//   列表推导式不把 a 泄漏到外层, 于是 detail.hit/miss 恒等于「最后一行」的数据,
//   与当前遍历的日期不同步。实测 10-15 18:44 那条: value=0.8718 (正确) 而 detail
//   反算 98.5% (错), 判定对、证据错, 静默躺了 1 天才被肉眼撞见。
// 判据为什么要死程序: 「哪个变量跟哪个日期同源」是数据流关系, 注释里写清楚不构成
//   约束 —— 加一行 detail 取用残留变量即静默失效, 且失效是无声的 (value 仍是对的)。

// TestWatchdog_AlertDetailSameSource 证据字段必须与判定值同源。
func TestWatchdog_AlertDetailSameSource(t *testing.T) {
	code := pyCode(t, "defense_system/forge_watchdog.py")
	if !strings.Contains(code, "hit, miss = agg[(d, m)][0], agg[(d, m)][1]") {
		t.Error("forge_watchdog.py 的 detail 未从 agg 现取 hit/miss —— 残留变量污染会复发 (T54)")
	}
	if !strings.Contains(code, "'hit': hit, 'miss': miss") {
		t.Error("detail 未使用同源变量 hit/miss")
	}
	// 反例守卫: 残留变量形态不得回潮 (a 只允许以聚合单元 cell 存在)
	for _, bad := range []string{"'hit': a[0]", "'miss': a[1]", "for d, a in recent"} {
		if strings.Contains(code, bad) {
			t.Errorf("残留变量形态回潮: %q —— 取到的是「最后一行」的数据", bad)
		}
	}
}

// TestWatchdog_AlertConsistencyWired 自洽性判据必须接线, 且汇总 key 固定。
// key 固定不可省: 带数量的 key 每轮都变 = 每轮都是新告警, 去重失效 (刷屏)。
func TestWatchdog_AlertConsistencyWired(t *testing.T) {
	code := pyCode(t, "defense_system/forge_watchdog.py")
	for _, want := range []string{
		"def audit_alerts(alerts):",
		"all_alerts = audit_alerts(all_alerts)",
		"'key': 'watchdog_alert_inconsistent'",
		"if a.get('resolved'):",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("forge_watchdog.py 缺 %q", want)
		}
	}
}

// TestWatchdog_AlertConsistencySelftest 判据自身要有判据: 正例 + 反例 + 对照组。
// 缺反例可能「反正不报」空转, 缺对照组可能「反正恒报」空转 —— 两种空转都能骗过正例。
func TestWatchdog_AlertConsistencySelftest(t *testing.T) {
	cmd := exec.Command(guardGatePython(), "-u",
		filepath.Join("defense_system", "forge_watchdog.py"), "--selftest")
	cmd.Env = pythonUTF8Env() // 剥离 PYTHONUTF8 的环境下中文输出按 GBK 落地 -> 判据假红
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--selftest 失败: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "PASS") {
		t.Errorf("selftest 未 PASS: %s", out)
	}
}
