package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ============================================================================
// hygiene_sentinel_test.go —— 根目录白名单哨兵 (5S/1S「整理」的死程序化)
//
// 为什么需要 (20260928 实测):
//   往根目录扔一个垃圾文件 _probe_junk_test.tmp, 四套守卫全绿 ——
//   forge_guard.py(完整性) / fractal_check.py(结构) / cleanup_backups.py(额度) /
//   git status(入库) 没有任何一个能看见它。垃圾能积累到「被人发现」,
//   机制原因就是「它是隐形的」: 所有守卫都在管「已知模式的已知位置」。
//
// 本哨兵管的是另一件事: 「根目录允许存在什么」。
//   1S 整理 = 区分要与不要。没有这条边界, 2S/3S 都是无本之木
//   (扫得再勤也解决不了乱放)。
//
// 白名单是单一数据源 defense_system/hygiene_whitelist.json,
// 与 defense_system/hygiene_redcard.py (红牌台账/常态巡检) 共用 ——
// 两处各写一份必然腐化。
//
// 判定原则: 白名单外 = 违规, 但违规不等于「删除对象」而是「红牌对象」
// (先贴牌、计时、到期处理), 天然防误删真资产。
// ============================================================================

const hygieneWhitelistFile = "defense_system/hygiene_whitelist.json"

type hygieneQuota struct {
	Pattern string `json:"pattern"`
	Keep    int    `json:"keep"`
	Desc    string `json:"desc"`
	Migrate string `json:"migrate"`
}

type hygieneDeny struct {
	Pattern string `json:"pattern"`
	Hint    string `json:"hint"`
}

type hygieneWhitelist struct {
	Version        int            `json:"version"`
	AllowGlobs     []string       `json:"allow_globs"`
	AllowExact     []string       `json:"allow_exact"`
	QuotaGlobs     []hygieneQuota `json:"quota_globs"`
	DenyGlobs      []hygieneDeny  `json:"deny_globs"`
	AllowDirs      []hygieneDir   `json:"allow_dirs"`
	AllowDirGlobs  []string       `json:"allow_dir_globs"`
	AllowEmptyDirs []string       `json:"allow_empty_dirs"`
}

func loadHygieneWhitelist(t *testing.T) hygieneWhitelist {
	t.Helper()
	raw, err := os.ReadFile(hygieneWhitelistFile)
	if err != nil {
		t.Fatalf("白名单读取失败 (fail-closed, 不放行): %v", err)
	}
	var wl hygieneWhitelist
	if err := json.Unmarshal(raw, &wl); err != nil {
		t.Fatalf("白名单解析失败: %v", err)
	}
	if p := hygieneWhitelistProblem(wl); p != "" {
		t.Fatalf("白名单不可用 (fail-closed, 不放行): %s", p)
	}
	return wl
}

// hygieneWhitelistProblem 校验白名单结构, 返回问题描述 (空串 = 可用)。
//
// 为什么抽成纯函数: fail-closed 本身也是判据, 判据必须能被测到。
// 若把校验直接写在 loadHygieneWhitelist 里(读文件), 就只能靠「改坏真文件」
// 来验证 —— 那既危险又测不全 (空 allow_dirs / 空 allow_globs / 空 allow_exact)。
// 抽出来后 TestHygieneWhitelistFailClosed 可以直接喂畸形结构。
func hygieneWhitelistProblem(wl hygieneWhitelist) string {
	var missing []string
	if len(wl.AllowGlobs) == 0 {
		missing = append(missing, "allow_globs")
	}
	if len(wl.AllowExact) == 0 {
		missing = append(missing, "allow_exact")
	}
	if len(wl.AllowDirs) == 0 {
		missing = append(missing, "allow_dirs")
	}
	if len(wl.AllowDirGlobs) == 0 {
		missing = append(missing, "allow_dir_globs")
	}
	if len(missing) > 0 {
		return "缺少必需字段 " + strings.Join(missing, ", ") +
			" —— 缺字段会让判定失去对照, 静默变成「全部放行」或「全部违规」"
	}
	return ""
}

// hygieneRootFiles 返回根目录下的普通文件名 (不含目录)。
func hygieneRootFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取根目录失败: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// hygieneVerdict 判定单个文件名: (是否允许, 归类说明)。
// 优先级: allow_exact > allow_globs > quota_globs > deny_globs > 未列入。
// quota 必须先于 deny —— 否则 "forge.exe.bak_*"(有额度) 会被
// "*.bak_*"(禁) 误判成违规, 同类规则互相打架。
func hygieneVerdict(name string, wl hygieneWhitelist) (bool, string) {
	low := strings.ToLower(name)
	for _, ex := range wl.AllowExact {
		if low == strings.ToLower(ex) {
			return true, "exact"
		}
	}
	for _, g := range wl.AllowGlobs {
		if ok, _ := filepath.Match(g, name); ok {
			return true, "glob:" + g
		}
	}
	for _, q := range wl.QuotaGlobs {
		if ok, _ := filepath.Match(q.Pattern, name); ok {
			return true, "quota:" + q.Pattern
		}
	}
	for _, d := range wl.DenyGlobs {
		if ok, _ := filepath.Match(d.Pattern, name); ok {
			return false, "已知垃圾模式 " + d.Pattern + " —— " + d.Hint
		}
	}
	return false, "未列入白名单"
}

// TestHygieneRootWhitelist 哨兵: 根目录不得出现白名单外文件。
func TestHygieneRootWhitelist(t *testing.T) {
	wl := loadHygieneWhitelist(t)
	files := hygieneRootFiles(t)
	if len(files) == 0 {
		t.Fatal("根目录未发现任何文件, 哨兵失效")
	}

	// 自检: 白名单必须真的匹配到东西 (防路径写错 / 白名单被清空后静默失效)
	matched := 0
	for _, f := range files {
		if ok, _ := hygieneVerdict(f, wl); ok {
			matched++
		}
	}
	if matched < 10 {
		t.Fatalf("白名单仅匹配到 %d/%d 个文件, 疑似失效 (检查 %s 与测试 cwd)",
			matched, len(files), hygieneWhitelistFile)
	}

	var bad []string
	for _, f := range files {
		if ok, why := hygieneVerdict(f, wl); !ok {
			bad = append(bad, fmt.Sprintf("%s  [%s]", f, why))
		}
	}
	if len(bad) > 0 {
		t.Errorf("根目录出现白名单外文件 %d 个 (5S/1S 整理违规):\n  %s\n"+
			"处理: 临时产物 → .forge-temp/ ; 历史资产 → _archive/ ; 机制脚本 → defense_system/ ;\n"+
			"      确属项目本体 → 加白名单条目 %s (需 git 可见, 白名单只增不减即腐化)",
			len(bad), strings.Join(bad, "\n  "), hygieneWhitelistFile)
		return // 有违规时不打印「全部在白名单内」—— 否则日志自相矛盾, 误导排查
	}
	t.Logf("根目录 %d 个文件, 全部在白名单内", len(files))
}

// TestHygieneQuotaRespected 哨兵: 备份类文件的份数不得超过各自声明的上限。
// 「有版本意义的备份」(保留 1) 与「可再生编译产物」(保留 0) 是两种东西,
// 用同一个数字盖住必然错一边 —— 故逐类显式声明 keep。
func TestHygieneQuotaRespected(t *testing.T) {
	wl := loadHygieneWhitelist(t)
	files := hygieneRootFiles(t)
	if len(wl.QuotaGlobs) == 0 {
		t.Fatal("配额清单为空, 哨兵失效")
	}
	for _, q := range wl.QuotaGlobs {
		var hits []string
		for _, f := range files {
			if ok, _ := filepath.Match(q.Pattern, f); ok {
				hits = append(hits, f)
			}
		}
		if len(hits) > q.Keep {
			t.Errorf("配额超限: %s 现存 %d 份 > 上限 %d (%s)\n  命中: %s\n"+
				"处理: 轮转阈值走 backupKeepCount, 残留由 cleanup_backups.py 清理",
				q.Pattern, len(hits), q.Keep, q.Desc, strings.Join(hits, ", "))
		}
	}
}

// TestHygieneWhitelistNoDuplicate 哨兵: 白名单内部不得自相矛盾
// (同一文件名同时出现在 allow_exact 与 deny_globs / 配额模式重复)。
// 白名单是唯一判据, 判据自相矛盾 = 判定结果取决于遍历顺序 = 不可复现。
func TestHygieneWhitelistNoDuplicate(t *testing.T) {
	wl := loadHygieneWhitelist(t)

	seen := map[string]string{}
	for _, ex := range wl.AllowExact {
		k := strings.ToLower(ex)
		if prev, dup := seen[k]; dup {
			t.Errorf("allow_exact 重复条目: %q 与 %q", prev, ex)
		}
		seen[k] = ex
	}

	qseen := map[string]bool{}
	for _, q := range wl.QuotaGlobs {
		if q.Pattern == "" {
			t.Error("quota_globs 存在空 pattern")
		}
		if q.Keep < 0 {
			t.Errorf("quota %s 的 keep 为负: %d", q.Pattern, q.Keep)
		}
		if qseen[q.Pattern] {
			t.Errorf("quota_globs 重复 pattern: %s", q.Pattern)
		}
		qseen[q.Pattern] = true
	}

	// 同一文件名不得既被 allow_exact 精确允许, 又被 deny 模式命中
	for _, ex := range wl.AllowExact {
		for _, d := range wl.DenyGlobs {
			if ok, _ := filepath.Match(d.Pattern, ex); ok {
				t.Errorf("白名单自相矛盾: %q 在 allow_exact 里, 却被 deny_globs 的 %q 命中",
					ex, d.Pattern)
			}
		}
	}
}

// TestHygieneWhitelistFailClosed 哨兵: 白名单结构残缺时必须被拦下。
// 缺字段的危险不在「报错」而在「不报错」: 少一个 allow_dirs, 「未登记目录」
// 判定就失去对照, 静默变成「全部违规」(刷屏后被人加豁免而失效) 或
// 「全部放行」(哨兵形同虚设)。两种极端都比直接失败更糟。
func TestHygieneWhitelistFailClosed(t *testing.T) {
	full := loadHygieneWhitelist(t)
	if p := hygieneWhitelistProblem(full); p != "" {
		t.Fatalf("真实白名单被判为不可用: %s", p)
	}

	// 逐字段制造残缺, 每个都必须被检出
	cases := []struct {
		name   string
		mutate func(w *hygieneWhitelist)
	}{
		{"缺 allow_globs", func(w *hygieneWhitelist) { w.AllowGlobs = nil }},
		{"缺 allow_exact", func(w *hygieneWhitelist) { w.AllowExact = nil }},
		{"缺 allow_dirs", func(w *hygieneWhitelist) { w.AllowDirs = nil }},
		{"缺 allow_dir_globs", func(w *hygieneWhitelist) { w.AllowDirGlobs = nil }},
	}
	for _, c := range cases {
		w := full
		c.mutate(&w)
		if p := hygieneWhitelistProblem(w); p == "" {
			t.Errorf("fail-closed 失效: %s 未被检出", c.name)
		}
	}
}
