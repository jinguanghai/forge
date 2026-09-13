package main

// peak_hour_test.go — 高峰时段判据哨兵 (20260913)
//
// 背景: isPeakHour 旧实现只判小时不判星期, 周末 9-12/14-18 误报"高峰×2":
//   - pickModel 周末把 pro 门槛 0.6 抬到 0.8 → 中复杂度任务无谓降级到 flash
//   - checkPeakHour 提示用户"躲高峰", 但周末并不涨价 → 误导
// 而同文件的 isMiniMaxWindow 已判周末 → 两处时段定义自相矛盾。
// 现两函数同源 (isMiniMaxWindow 复用 isPeakHourAt), 本测试把"同源"固化为死程序判定。

import (
	"testing"
	"time"
)

func TestIsPeakHourAt_WeekdayWeekend(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"周一09:00 高峰起点", time.Date(2026, 8, 31, 9, 0, 0, 0, time.Local), true},
		{"周一11:59 高峰末", time.Date(2026, 8, 31, 11, 59, 0, 0, time.Local), true},
		{"周一12:00 右开区间不含", time.Date(2026, 8, 31, 12, 0, 0, 0, time.Local), false},
		{"周一13:59 午休", time.Date(2026, 8, 31, 13, 59, 0, 0, time.Local), false},
		{"周一14:00 高峰起点", time.Date(2026, 8, 31, 14, 0, 0, 0, time.Local), true},
		{"周一17:59 高峰末", time.Date(2026, 8, 31, 17, 59, 0, 0, time.Local), true},
		{"周一18:00 右开区间不含", time.Date(2026, 8, 31, 18, 0, 0, 0, time.Local), false},
		{"周一08:59 未开盘", time.Date(2026, 8, 31, 8, 59, 0, 0, time.Local), false},
		{"周五10:00 工作日高峰", time.Date(2026, 8, 28, 10, 0, 0, 0, time.Local), true},
		// 回归: 旧实现在以下两条误报 true
		{"周六10:00 周末不涨价", time.Date(2026, 8, 29, 10, 0, 0, 0, time.Local), false},
		{"周日15:00 周末不涨价", time.Date(2026, 8, 30, 15, 0, 0, 0, time.Local), false},
		{"周六00:00 周末全天", time.Date(2026, 8, 29, 0, 0, 0, 0, time.Local), false},
		{"周日23:00 周末全天", time.Date(2026, 8, 30, 23, 0, 0, 0, time.Local), false},
	}
	for _, c := range cases {
		if got := isPeakHourAt(c.t); got != c.want {
			t.Errorf("[%s] %s -> want %v got %v", c.name, c.t.Format("2006-01-02 15:04 Mon"), c.want, got)
		}
	}
}

// 单一源哨兵: 任意时刻, "DeepSeek 是否涨价" 与 "是否切 MiniMax" 必须同真同假。
// 两者判据曾各自漂移 (20260913), 本测试使其在结构上不可能再漂移。
func TestPeakAndMiniMaxWindow_SameSource(t *testing.T) {
	base := time.Date(2026, 8, 24, 0, 0, 0, 0, time.Local) // 周一
	for d := 0; d < 14; d++ {
		for h := 0; h < 24; h++ {
			tt := base.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour)
			if isPeakHourAt(tt) != isMiniMaxWindow(tt) {
				t.Fatalf("判据漂移: %s isPeakHourAt=%v isMiniMaxWindow=%v",
					tt.Format("2006-01-02 15:04 Mon"), isPeakHourAt(tt), isMiniMaxWindow(tt))
			}
		}
	}
}

// 门槛切换机制: 非高峰(含周末) 用 0.6 阈值 (中复杂度升级 pro),
// 工作日高峰用 0.8 (留在 flash 省钱)。周末修正后不再误抬门槛。
func TestPickModel_PeakThresholdSwitch(t *testing.T) {
	old := peakHourNow
	defer func() { peakHourNow = old }()
	cfg := &Config{RouterMode: RouterAuto, ModelFlash: "flash", ModelPro: "pro"}
	task := "并发安全缓存" // 复杂度应落在 [0.6, 0.8)
	score := classifyTask(task)
	if score < 0.6 || score >= 0.8 {
		t.Fatalf("用例任务复杂度 %.2f 已不在 [0.6,0.8) 区间, 请更新本用例任务串", score)
	}
	peakHourNow = func() bool { return false } // 非高峰 / 周末
	if got := pickModel(cfg, task); got != "pro" {
		t.Fatalf("非高峰(门槛0.6)应升级 pro, 实际 %s", got)
	}
	peakHourNow = func() bool { return true } // 工作日高峰
	if got := pickModel(cfg, task); got != "flash" {
		t.Fatalf("高峰(门槛0.8)应留在 flash, 实际 %s", got)
	}
}
