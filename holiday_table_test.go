package main

import (
	"testing"
	"time"
)

func init() {
	// 测试启动时强制重载节假日表 (生产缓存可能含旧表)
	reloadHolidaysForTest("2026")
}

func TestIsChineseHoliday_2026(t *testing.T) {
	cases := []struct {
		name string
		date string
		want bool
	}{
		{"元旦1/1", "2026-01-01", true},
		{"元旦1/2", "2026-01-02", true},
		{"元旦1/3", "2026-01-03", true},
		{"元旦后1/4", "2026-01-04", false},
		{"除夕2/16", "2026-02-16", true},
		{"春节2/17", "2026-02-17", true},
		{"春节2/23末", "2026-02-23", true},
		{"春节后2/24", "2026-02-24", false},
		{"清明4/4", "2026-04-04", true},
		{"清明后4/7", "2026-04-07", false},
		{"劳动5/1", "2026-05-01", true},
		{"劳动5/5末", "2026-05-05", true},
		{"劳动5/6后", "2026-05-06", false},
		{"端午6/19", "2026-06-19", true},
		{"端午6/21末", "2026-06-21", true},
		{"端午6/22后", "2026-06-22", false},
		{"中秋9/25", "2026-09-25", true},
		{"中秋9/27末", "2026-09-27", true},
		{"中秋9/28后", "2026-09-28", false},
		{"国庆10/1", "2026-10-01", true},
		{"国庆10/7末", "2026-10-07", true},
		{"国庆10/8后", "2026-10-08", false},
	}
	for _, c := range cases {
		tm, err := time.Parse("2006-01-02", c.date)
		if err != nil {
			t.Fatalf("invalid date %s: %v", c.date, err)
		}
		if got := isChineseHoliday(tm); got != c.want {
			t.Errorf("[%s] %s -> want %v got %v", c.name, c.date, c.want, got)
		}
	}
}

func TestIsPeakHourAt_HolidayOverride(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		// 春节 2026-02-17 周二 10:00: 旧实现 true, 新实现 false
		{"2026春节(周二)10:00", time.Date(2026, 2, 17, 10, 0, 0, 0, time.Local), false},
		{"2026春节(周三)14:30", time.Date(2026, 2, 18, 14, 30, 0, 0, time.Local), false},
		{"2026春节后(周二)10:00", time.Date(2026, 2, 24, 10, 0, 0, 0, time.Local), true},
		{"2026劳动节(周五)10:00", time.Date(2026, 5, 1, 10, 0, 0, 0, time.Local), false},
		{"2026国庆(周四)10:00", time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local), false},
		{"2026国庆后(周四)10:00", time.Date(2026, 10, 8, 10, 0, 0, 0, time.Local), true},
	}
	for _, c := range cases {
		if got := isPeakHourAt(c.t); got != c.want {
			t.Errorf("[%s] %s -> want %v got %v", c.name, c.t.Format("2006-01-02 15:04 Mon"), c.want, got)
		}
	}
}

// 2027 年无节假日表 → fail-closed 返回 false, 价格仍按工作日高峰计算 (false negative 在表缺失时,
// 宁可多付钱也别漏付; 加表前不会"抬价格")。
func TestIsChineseHoliday_FailClosed(t *testing.T) {
	tm := time.Date(2027, 2, 17, 10, 0, 0, 0, time.Local)
	if isChineseHoliday(tm) {
		t.Errorf("2027 无节假日表, 应 fail-closed=false, got true")
	}
	if !isPeakHourAt(tm) {
		t.Errorf("2027 无节假日表, 周三 10:00 应判 true (高峰), got false")
	}
}

// 节假日判据注入后, 周末 + 节假日 + 调休表 全在一处定义; 主测试钉死同源, 防止两处再各自漂移.
func TestPeakAndMiniMaxWindow_HolidayAware(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for d := 0; d < 365; d++ {
		tt := base.AddDate(0, 0, d)
		h10 := tt.Add(10 * time.Hour)
		if isPeakHourAt(h10) != isMiniMaxWindow(h10) {
			t.Fatalf("判据漂移: %s isPeakHourAt=%v isMiniMaxWindow=%v",
				h10.Format("2006-01-02 15:04 Mon"), isPeakHourAt(h10), isMiniMaxWindow(h10))
		}
	}
}
