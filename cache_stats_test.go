// cache_stats_test.go: 前缀缓存度量的行为契约。
//
// 防的是"污染与误读":
//   - cacheStatPath 是全局态, 测试必须隔离并在结束后恢复(否则污染真实统计)
//   - 命中率窗口只取最近 n 条 —— 窗口选择直接影响状态栏显示
//   - 无样本时必须 ok=false, 不得用 0 冒充"0% 命中"
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withCacheStatDir 把全局统计路径指向临时目录, 并在测试结束后恢复。
func withCacheStatDir(t *testing.T, dir string) {
	t.Helper()
	cacheStatMu.Lock()
	old := cacheStatPath
	cacheStatMu.Unlock()
	setCacheStatPath(dir)
	t.Cleanup(func() {
		cacheStatMu.Lock()
		cacheStatPath = old
		cacheStatMu.Unlock()
	})
}

// writeCacheStatRows 直接落盘若干条统计行(绕过 recordCacheStat 的零值跳过)。
func writeCacheStatRows(t *testing.T, dir string, rows []CacheStat) {
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
	if err := os.WriteFile(filepath.Join(dir, defaultCacheStatName), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCacheStats_IsFlashModel(t *testing.T) {
	yes := []string{"deepseek-flash", "DEEPSEEK-FLASH", "flash", "Deepseek-Flash"}
	no := []string{"", "deepseek-v4-pro", "deepseek-chat", "gpt-4"}
	for _, m := range yes {
		if !isFlashModel(m) {
			t.Errorf("应判 flash 模型: %q", m)
		}
	}
	for _, m := range no {
		if isFlashModel(m) {
			t.Errorf("不应判 flash 模型: %q", m)
		}
	}
}

func TestCacheStats_RecordAndRead(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)

	recordCacheStat("deepseek-flash", 90, 10, 7, "abc123", false)

	data, err := os.ReadFile(filepath.Join(dir, defaultCacheStatName))
	if err != nil {
		t.Fatalf("未写出统计文件: %v", err)
	}
	var got CacheStat
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &got); err != nil {
		t.Fatalf("统计行不是合法 JSON: %v", err)
	}
	if got.Model != "deepseek-flash" || got.Hit != 90 || got.Miss != 10 || got.SysHash != "abc123" {
		t.Errorf("统计字段不符: %+v", got)
	}
	// 输出 token 必须随行落盘 (此前只记输入侧, 输出成本无度量)
	if got.Out != 7 {
		t.Errorf("输出 token 应回读 7, 实际 %d (字段 %+v)", got.Out, got)
	}
	if got.Time == "" {
		t.Error("缺时间戳")
	}
	if got.SysChanged {
		t.Error("SysChanged 应为 false")
	}
}

func TestCacheStats_RecordSkipsEmpty(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)

	// 零命中零未命中且前缀未变更 → 不落盘(避免窗口内混入空记录)
	recordCacheStat("m", 0, 0, 0, "", false)
	if _, err := os.Stat(filepath.Join(dir, defaultCacheStatName)); err == nil {
		t.Error("零值统计不应落盘")
	}
	// 前缀变更即使无 token 也必须落盘(六西格玛守卫信号)
	recordCacheStat("m", 0, 0, 0, "h", true)
	if _, err := os.Stat(filepath.Join(dir, defaultCacheStatName)); err != nil {
		t.Error("前缀变更事件必须落盘")
	}

	// 仅有输出 token 也必须落盘: 输出是真实消耗(4 元/M), 不得因输入侧为 0 被丢弃
	dir3 := t.TempDir()
	withCacheStatDir(t, dir3)
	recordCacheStat("m", 0, 0, 123, "", false)
	data, err := os.ReadFile(filepath.Join(dir3, defaultCacheStatName))
	if err != nil {
		t.Fatalf("仅输出 token 的记录必须落盘: %v", err)
	}
	var got CacheStat
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &got); err != nil {
		t.Fatalf("落盘行不是合法 JSON: %v", err)
	}
	if got.Out != 123 {
		t.Errorf("Out 应回读 123, 实际 %d", got.Out)
	}
}

func TestCacheStats_HitRate(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)

	// 无文件 → ok=false
	if _, n, ok := cacheHitRate(20); ok || n != 0 {
		t.Errorf("无文件应 ok=false n=0, 实际 ok=%v n=%d", ok, n)
	}

	writeCacheStatRows(t, dir, []CacheStat{
		{Model: "a", Hit: 90, Miss: 10},
		{Model: "a", Hit: 100, Miss: 0},
	})
	rate, n, ok := cacheHitRate(20)
	if !ok || n != 2 {
		t.Fatalf("ok=%v n=%d 期望 true/2", ok, n)
	}
	want := 190.0 * 100 / 200.0
	if rate < want-0.01 || rate > want+0.01 {
		t.Errorf("命中率 = %.2f 期望 %.2f", rate, want)
	}

	// 有记录但零 token → ok=false 但 nRecs>0 (不得用 0 冒充命中率)
	dir2 := t.TempDir()
	withCacheStatDir(t, dir2)
	writeCacheStatRows(t, dir2, []CacheStat{{Model: "a", Hit: 0, Miss: 0, SysHash: "h", SysChanged: true}})
	rate2, n2, ok2 := cacheHitRate(20)
	if ok2 {
		t.Error("零 token 样本应 ok=false")
	}
	if rate2 != 0 || n2 != 1 {
		t.Errorf("零 token 样本 rate=%v n=%d 期望 0/1", rate2, n2)
	}
}

func TestCacheStats_HitRateWindow(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)
	writeCacheStatRows(t, dir, []CacheStat{
		{Model: "a", Hit: 0, Miss: 100}, // 旧: 全未命中
		{Model: "a", Hit: 100, Miss: 0}, // 新: 全命中
	})
	// n=1 只取最后一条 → 100%
	rate, n, ok := cacheHitRate(1)
	if !ok || n != 1 {
		t.Fatalf("n=1 应只取 1 条, 实际 n=%d ok=%v", n, ok)
	}
	if rate != 100 {
		t.Errorf("窗口内命中率 = %v 期望 100", rate)
	}
	// n<=0 走默认窗口(20) → 两条都算 → 50%
	rate, n, ok = cacheHitRate(0)
	if !ok || n != 2 {
		t.Fatalf("n=0 应用默认窗口, 实际 n=%d", n)
	}
	if rate != 50 {
		t.Errorf("默认窗口命中率 = %v 期望 50", rate)
	}
}

func TestCacheStats_HealthLevels(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)

	if got := cacheHealth(20); !strings.Contains(got, "无健康数据") {
		t.Errorf("无文件应报无健康数据, 实际 %q", got)
	}

	// 绿档: 100% 且前缀稳定
	writeCacheStatRows(t, dir, []CacheStat{{Model: "a", Hit: 99, Miss: 1}})
	got := cacheHealth(20)
	if !strings.Contains(got, "99.0%") {
		t.Errorf("应报 99.0%%: %q", got)
	}
	if !strings.Contains(got, "根因: 正常") {
		t.Errorf("99%% 应判正常: %q", got)
	}

	// 黄档: 90% 且前缀稳定 → 根因须给排查方向, 不得仍写"正常"
	writeCacheStatRows(t, dir, []CacheStat{{Model: "a", Hit: 90, Miss: 10}})
	got = cacheHealth(20)
	if !strings.Contains(got, "90.0%") || !strings.Contains(got, "前缀稳定但命中偏低") {
		t.Errorf("90%% 应给黄档根因: %q", got)
	}

	// 红档: 50% 且前缀稳定
	writeCacheStatRows(t, dir, []CacheStat{{Model: "a", Hit: 50, Miss: 50}})
	got = cacheHealth(20)
	if !strings.Contains(got, "50.0%") || !strings.Contains(got, "请求体不稳定") {
		t.Errorf("50%% 应给红档根因: %q", got)
	}

	// 前缀频繁变更优先级最高(近 n 条半数以上变更)
	writeCacheStatRows(t, dir, []CacheStat{{Model: "a", Hit: 99, Miss: 1, SysChanged: true}})
	got = cacheHealth(20)
	if !strings.Contains(got, "前缀频繁变更") {
		t.Errorf("前缀变更应优先报: %q", got)
	}

	// 只有零 token 样本 → 无命中/未命中样本
	writeCacheStatRows(t, dir, []CacheStat{{Model: "a", Hit: 0, Miss: 0, SysChanged: true}})
	got = cacheHealth(20)
	if !strings.Contains(got, "前缀频繁变更") && !strings.Contains(got, "无命中/未命中样本") {
		t.Errorf("零 token 样本应可判定: %q", got)
	}
}

func TestCacheStats_Summary(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)

	if got := cacheStatsSummary(); !strings.Contains(got, "暂无缓存统计数据") {
		t.Errorf("无文件应报暂无统计, 实际 %q", got)
	}

	writeCacheStatRows(t, dir, []CacheStat{
		{Model: "deepseek-flash", Hit: 90, Miss: 10, SysHash: "h1"},
		{Model: "deepseek-flash", Hit: 80, Miss: 20, SysHash: "h2", SysChanged: true},
		{Model: "other", Hit: 10, Miss: 0},
	})
	got := cacheStatsSummary()
	for _, want := range []string{"请求数: 3", "输入 tokens: 210", "按模型:", "deepseek-flash", "other", "前缀指纹"} {
		if !strings.Contains(got, want) {
			t.Errorf("汇总缺 %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "历史不同指纹 2 个") {
		t.Errorf("应统计出 2 个不同前缀指纹:\n%s", got)
	}
}

func TestCacheStats_PathForWrite(t *testing.T) {
	cacheStatMu.Lock()
	old := cacheStatPath
	cacheStatPath = defaultCacheStatName
	cacheStatMu.Unlock()
	t.Cleanup(func() {
		cacheStatMu.Lock()
		cacheStatPath = old
		cacheStatMu.Unlock()
	})

	// 未显式设置 → 回退环境变量(测试隔离出口)
	t.Setenv("FORGE_CACHE_STATS_PATH", filepath.Join(t.TempDir(), "iso.jsonl"))
	if got := cacheStatPathForWrite(); !strings.HasSuffix(got, "iso.jsonl") {
		t.Errorf("应回退环境变量, 实际 %q", got)
	}
	// 显式设置优先于环境变量
	dir := t.TempDir()
	setCacheStatPath(dir)
	if got := cacheStatPathForWrite(); got != filepath.Join(dir, defaultCacheStatName) {
		t.Errorf("显式路径应优先, 实际 %q", got)
	}
}

// TestCacheStats_CostDimension 钉住成本口径: 量纲(美元/百万 tokens) + 输出侧计价 + 历史行兼容。
//
// 防的是"看起来像真的"全假阳性: 价格常量单位是 美元/百万 tokens, 若直接乘 token 数,
// 结果放大 1e6 倍 —— 实测 24199 行真实数据旧式显示 $157,133,048.22, 真实仅 $11.66。
// 期望值一律用字面量手算, 不用常量自证(否则常量写错用例照样绿)。
func TestCacheStats_CostDimension(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)
	// 各 1 百万 tokens (flash): 命中 0.0028 + 未命中 0.14 + 输出 0.56 = 0.7028 美元
	writeCacheStatRows(t, dir, []CacheStat{
		{Model: "deepseek-flash", Hit: 1_000_000, Miss: 1_000_000, Out: 1_000_000},
	})
	got := cacheStatsSummary()
	for _, want := range []string{
		"输出 tokens: 1000000", // 输出侧计数
		"输入 $0.1428",         // 1M×0.0028 + 1M×0.14
		"输出 $0.5600",         // 1M×0.56
		"= $0.7028",          // 合计
		"估算节省: $0.1372",      // 无缓存 2M×0.14=0.28 减实际 0.1428
	} {
		if !strings.Contains(got, want) {
			t.Errorf("成本汇总缺 %q:\n%s", want, got)
		}
	}
	// 量纲反例: 旧实现(未除 1e6)会输出 702800
	if strings.Contains(got, "702800") || strings.Contains(got, "142800") {
		t.Errorf("成本被放大 1e6 倍(量纲错误):\n%s", got)
	}

	// 历史行(无 out 字段)不得被计入输出侧
	dir2 := t.TempDir()
	withCacheStatDir(t, dir2)
	writeCacheStatRows(t, dir2, []CacheStat{{Model: "deepseek-flash", Hit: 1_000_000}})
	got2 := cacheStatsSummary()
	if !strings.Contains(got2, "输出 tokens: 0") || !strings.Contains(got2, "输出 $0.0000") {
		t.Errorf("历史行缺 out 字段应计 0 输出:\n%s", got2)
	}

	// pro 档输出价 1.89 美元/百万
	dir3 := t.TempDir()
	withCacheStatDir(t, dir3)
	writeCacheStatRows(t, dir3, []CacheStat{{Model: "deepseek-v4-pro", Miss: 1_000_000, Out: 1_000_000}})
	got3 := cacheStatsSummary()
	if !strings.Contains(got3, "输出 $1.8900") {
		t.Errorf("pro 输出价应为 $1.89/百万:\n%s", got3)
	}
}
