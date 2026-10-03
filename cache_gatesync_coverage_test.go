package main

// cache_gatesync_coverage_test.go — 缓存度量与 gate 五处同步覆盖补强 (B2)
//
// 原状: cache_stats.go 6 个函数 (setCacheStatPath/recordCacheStat/isFlashModel/
// cacheHealth/cacheStatsSummary/cacheHitRate) 与 gatesync.go 2 个函数全部零测试引用。
// 两者都是"死程序判定"的产出 (命中率分级 / 五处一致性), 判错会误导治理方向。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setTempCacheStat 把缓存度量文件指向临时目录, 测试结束自动还原。
func setTempCacheStat(t *testing.T) string {
	t.Helper()
	old := cacheStatPath
	path := filepath.Join(t.TempDir(), "cache_stats.jsonl")
	cacheStatPath = path
	t.Cleanup(func() { cacheStatPath = old })
	return path
}

func writeCacheStats(t *testing.T, path string, rows []CacheStat) {
	t.Helper()
	var sb strings.Builder
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestIsFlashModel(t *testing.T) {
	yes := []string{"deepseek-flash", "DeepSeek-V4.1-Flash", "FLASH", "v4-flash"}
	for _, m := range yes {
		if !isFlashModel(m) {
			t.Fatalf("%q 应判为 flash 模型", m)
		}
	}
	no := []string{"deepseek-v4-pro", "", "minimax-m3"}
	for _, m := range no {
		if isFlashModel(m) {
			t.Fatalf("%q 不应判为 flash 模型", m)
		}
	}
}

func TestRecordCacheStat_EmptySkipped(t *testing.T) {
	path := setTempCacheStat(t)
	recordCacheStat("deepseek-flash", 0, 0, 0, "", false)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("全零且非前缀变更的记录不应写文件")
	}
	// 仅前缀变更 (hit/miss 为 0) 必须记账, 否则漂移不可见
	recordCacheStat("deepseek-flash", 0, 0, 0, "abc", true)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("前缀变更记录应写入: %v", err)
	}
}

func TestRecordCacheStat_AndHitRate(t *testing.T) {
	path := setTempCacheStat(t)
	recordCacheStat("deepseek-flash", 80, 20, 0, "h1", false)
	recordCacheStat("deepseek-flash", 90, 10, 0, "h1", false)
	recordCacheStat("deepseek-flash", 50, 50, 0, "h1", false)

	rate, n, ok := cacheHitRate(20)
	if !ok || n != 3 {
		t.Fatalf("应有 3 条样本, 实际 n=%d ok=%v", n, ok)
	}
	want := float64(220) * 100 / float64(300)
	if rate < want-0.01 || rate > want+0.01 {
		t.Fatalf("全量命中率应为 %.2f, 实际 %.2f", want, rate)
	}
	// n 限制只取最近 n 条
	rate2, n2, ok2 := cacheHitRate(2)
	if !ok2 || n2 != 2 {
		t.Fatalf("n=2 应取 2 条, 实际 n=%d ok=%v", n2, ok2)
	}
	want2 := float64(140) * 100 / float64(200)
	if rate2 < want2-0.01 || rate2 > want2+0.01 {
		t.Fatalf("近 2 条命中率应为 %.2f, 实际 %.2f", want2, rate2)
	}
	// 空文件
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok3 := cacheHitRate(20); ok3 {
		t.Fatal("空文件应返回 ok=false")
	}
}

func TestCacheHitRate_NoFile(t *testing.T) {
	_ = setTempCacheStat(t)
	if _, _, ok := cacheHitRate(20); ok {
		t.Fatal("文件不存在应返回 ok=false")
	}
}

func TestCacheHealth_Levels(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	cases := []struct {
		name    string
		rows    []CacheStat
		badge   string
		rootSub string
	}{
		{"绿-基线健康", []CacheStat{{Time: now, Hit: 99, Miss: 1}}, "🟢", "正常"},
		// 黄档根因已与分级对齐 (20260913): 旧实现报"正常"与 🟡 措辞矛盾
		{"黄-偏低", []CacheStat{{Time: now, Hit: 90, Miss: 10}}, "🟡", "命中偏低"},
		{"红-请求体异常", []CacheStat{{Time: now, Hit: 50, Miss: 50}}, "🔴", "请求体不稳定"},
		{"红-前缀频繁变更", []CacheStat{
			{Time: now, Hit: 50, Miss: 50, SysChanged: true},
			{Time: now, Hit: 50, Miss: 50, SysChanged: true},
			{Time: now, Hit: 50, Miss: 50, SysChanged: true},
			{Time: now, Hit: 50, Miss: 50},
		}, "🔴", "前缀频繁变更"},
	}
	for _, c := range cases {
		path := setTempCacheStat(t)
		writeCacheStats(t, path, c.rows)
		got := cacheHealth(20)
		if !strings.Contains(got, c.badge) {
			t.Fatalf("[%s] 分级应为 %s, 实际: %s", c.name, c.badge, got)
		}
		if !strings.Contains(got, c.rootSub) {
			t.Fatalf("[%s] 根因应含 %q, 实际: %s", c.name, c.rootSub, got)
		}
	}
}

func TestCacheHealth_NoData(t *testing.T) {
	_ = setTempCacheStat(t)
	if got := cacheHealth(20); !strings.Contains(got, "无健康数据") {
		t.Fatalf("无文件应报无健康数据, 实际: %s", got)
	}
	path := setTempCacheStat(t)
	if err := os.WriteFile(path, []byte(`{"time":"t","hit":0,"miss":0}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := cacheHealth(20); !strings.Contains(got, "无命中/未命中样本") {
		t.Fatalf("全零样本应报无样本, 实际: %s", got)
	}
}

// ─── gatesync.go ───────────────────────────────────────────

func setupGateSyncDir(t *testing.T, dropMem, dropReadme, dropPlugin string) string {
	t.Helper()
	dir := t.TempDir()
	gates := currentGates
	if dropMem != "" {
		gates = removeStr(gates, dropMem)
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.json"),
		[]byte(`{"gates":"`+strings.Join(gates, "/")+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	readmeGates := currentGates
	if dropReadme != "" {
		readmeGates = removeStr(readmeGates, dropReadme)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# forge\n"+strings.Join(readmeGates, " ")), 0644); err != nil {
		t.Fatal(err)
	}
	pluginGates := currentGates
	if dropPlugin != "" {
		pluginGates = removeStr(pluginGates, dropPlugin)
	}
	pluginDir := filepath.Join(dir, "dsh-forge-plugins", "plugins", "forge-gates")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "index.js"),
		[]byte("// "+strings.Join(pluginGates, " ")), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func removeStr(list []string, drop string) []string {
	var out []string
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

func TestGateSyncCheck_AllPresent(t *testing.T) {
	dir := setupGateSyncDir(t, "", "", "")
	out := gateSyncCheck(dir, dir)
	if !strings.Contains(out, "五处全部一致") {
		t.Fatalf("五处齐全应报一致, 实际: %s", out)
	}
}

func TestGateSyncCheck_MissingReported(t *testing.T) {
	dir := setupGateSyncDir(t, "tcm", "self", "chain")
	out := gateSyncCheck(dir, dir)
	for _, want := range []string{
		"memory.json.gates 缺: tcm",
		"README 缺 gate 名: self",
		"发布包缺 gate 名: chain",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("应报差异 %q, 实际: %s", want, out)
		}
	}
	if strings.Contains(out, "五处全部一致") {
		t.Fatalf("有差异时不应报一致: %s", out)
	}
}

func TestGateSyncCheck_MissingFiles(t *testing.T) {
	dir := t.TempDir()
	out := gateSyncCheck(dir, dir)
	for _, want := range []string{"无 gates 字段", "README.md 不可读", "index.js 不可读"} {
		if !strings.Contains(out, want) {
			t.Fatalf("应报 %q, 实际: %s", want, out)
		}
	}
}

func TestContainsIssue(t *testing.T) {
	if !containsIssue([]string{"abc", "def"}, "bc") {
		t.Fatal("子串命中应返回 true")
	}
	if containsIssue([]string{"abc"}, "zz") {
		t.Fatal("未命中应返回 false")
	}
	if containsIssue(nil, "x") {
		t.Fatal("空列表应返回 false")
	}
}

// TestGateSyncCheck_ShortNameNotFalselyMatched 短名(如 sh)不得被无关词假命中。
//
// 20260925 实测: ⑤ 原用裸子串匹配, "sh" 被 push/shell/finish 之类词命中 →
// sh 这一面永远不报警 (假通过)。改用词边界判定后两侧都要成立。
func TestGateSyncCheck_ShortNameNotFalselyMatched(t *testing.T) {
	for _, bad := range []string{"push shell finish", "slash", "hashed", "mathematics"} {
		if hasGateToken(bad, "sh") && bad != "mathematics" {
			t.Errorf("%q 不该命中 sh", bad)
		}
	}
	if hasGateToken("mathematics", "math") {
		t.Error("math 被 mathematics 假命中")
	}
	for _, good := range []struct{ data, name string }{
		{"gate: 'sh',", "sh"},
		{"// python go sh node", "sh"},
		{"'math'", "math"},
		{"gate: 'relation'", "relation"},
	} {
		if !hasGateToken(good.data, good.name) {
			t.Errorf("%q 应命中 %s", good.data, good.name)
		}
	}
}

// TestPluginUnpublishedGates_Declared 有意不发布的 gate 必须显式留档(可审计),
// 而发布包核心 gate 绝不能被误列入豁免(否则漏同步将静默)。
func TestPluginUnpublishedGates_Declared(t *testing.T) {
	for _, g := range []string{"tcm", "browser", "self"} {
		if !isPluginUnpublished(g) {
			t.Errorf("%s 应列为有意不发布 (私有域/依赖缺失/安全)", g)
		}
	}
	for _, g := range []string{"python", "go", "node", "math", "logic", "regex", "knowledge", "chain"} {
		if isPluginUnpublished(g) {
			t.Errorf("%s 是发布包核心 gate, 不应被豁免", g)
		}
	}
}

// TestGateSyncCheck_RealEnvironment 真实环境五处同步 —— 用生产配置路径跑一遍。
//
// fixture 测试只证明"逻辑对"; 本用例证明"当前仓库 + 当前发布包真的同步",
// 把 /gatesync 的人工动作固化为死程序判定 (接线哨兵同款思路)。
func TestGateSyncCheck_RealEnvironment(t *testing.T) {
	wd, _ := os.Getwd()
	cfg := DefaultConfig()
	idx := filepath.Join(cfg.PluginReleaseDir, "dsh-forge-plugins", "plugins", "forge-gates", "index.js")
	if _, err := os.Stat(idx); err != nil {
		t.Skipf("发布包不存在(%s), 跳过真实环境检查", idx)
	}
	out := gateSyncCheck(wd, cfg.PluginReleaseDir)
	if !strings.Contains(out, "五处全部一致") {
		t.Fatalf("真实环境五处未同步:\n%s", out)
	}
	t.Logf("真实环境五处同步通过")
}
