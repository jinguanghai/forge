package main

// ── cache_health_sentinel_test.go — 成本真相判据 (P2-3, 20261004) ──
//
// 背景 (实测, 见 TODO P2-3): 缓存率与单位成本 r = -0.947 (最强因子);
//   <80% / 80-95% / >=95% = 13.02 / 4.00 / 2.40 元/千 (5.42 倍)。
//   结论: 贵的不是「改地图」, 是「会话碎」。
//
// 缺口: cacheHealth 的分档线 (绿 >=98 / 黄 85~98 / 红 <85) 无判据钉住 ——
//   cache_gatesync_coverage_test.go 的 TestCacheHealth_Levels 用的是 99% / 90% / 50%
//   这类远离分档线的样本, 阈值被改坏 (如 98 改 95) 三条用例照样全绿。
//   本哨兵补的正是"分档线本身": 98 与 85 两侧各取 0.1 个百分点, 任何漂移即报红。
//
// 不重复已有判据 (同一契约两处维护 = 迟早两处不一致):
//   根因与分档同源 / 黄档不得报"正常" → TestCacheHealth_Levels 已覆盖
//   无数据与全零样本的显式可见        → TestCacheHealth_NoData 已覆盖
//   本文件只留"分档线"这一条空白。夹具复用 setTempCacheStat / writeCacheStats。

import (
	"strings"
	"testing"
)

// TestCacheHealth_BadgeLineExact 分档线判据: 98 / 85 两条线两侧徽章必须不同。
// 边界两侧各取 0.1 个百分点 —— 阈值只要动一点, 必有一侧落错档。
func TestCacheHealth_BadgeLineExact(t *testing.T) {
	cases := []struct {
		name  string
		hit   int
		miss  int
		badge string
		tier  string
	}{
		{"绿线上方 98.0%", 980, 20, "🟢", "绿"},
		{"绿线下方 97.9%", 979, 21, "🟡", "黄"},
		{"黄线上方 85.0%", 850, 150, "🟡", "黄"},
		{"黄线下方 84.9%", 849, 151, "🔴", "红"},
	}
	for _, c := range cases {
		path := setTempCacheStat(t)
		writeCacheStats(t, path, []CacheStat{{Time: "2026-10-04T12:00:00+08:00", Hit: c.hit, Miss: c.miss}})
		got := cacheHealth(20)
		if !strings.Contains(got, c.badge) {
			t.Errorf("%s (hit=%d miss=%d): 期望%s徽章 %q, 实际: %s",
				c.name, c.hit, c.miss, c.tier, c.badge, got)
		}
	}
}
