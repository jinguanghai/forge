package main

// cov_ui_theme_more_test.go — TUI 主题/卡片/迷你图 分支补测
// 注意: tuiTheme* 使用相对路径 .forge/theme → 必须 t.Chdir 到临时目录, 绝不污染真实工作目录。

import (
	"os"
	"strings"
	"testing"
)

func TestCovUI_GradientItoaCenter(t *testing.T) {
	pal := []string{"\x1b[31m", "\x1b[32m", "\x1b[34m"}
	if got := tuiGradient("铸剑炉", pal); stripAnsi(got) != "铸剑炉" {
		t.Errorf("tuiGradient 改动了文本: %q", got)
	}
	_ = tuiGradient("", pal)
	_ = tuiGradient("abc", nil)
	for _, n := range []int{0, 1, -1, 9, 10, 999, 100000} {
		if itoa(n) == "" {
			t.Errorf("itoa(%d) 为空", n)
		}
	}
	if itoa(123) != "123" {
		t.Errorf("itoa(123)=%q", itoa(123))
	}
	for _, c := range []struct {
		s string
		w int
	}{{"abc", 10}, {"abc", 3}, {"abc", 1}, {"", 5}, {"中文", 6}} {
		out := centerText(c.s, c.w)
		if out == "" && c.s != "" {
			t.Errorf("centerText(%q,%d) 为空", c.s, c.w)
		}
	}
}

func TestCovUI_CardBannerSparkline(t *testing.T) {
	card := tuiCard("标题", []string{"行一", "行二"}, 40, "\x1b[36m")
	if stripAnsi(card) == "" {
		t.Error("tuiCard 输出为空")
	}
	for _, n := range []int{-5, 0, 1, 7, 30, 100, 100000} {
		if out := cacheSparkline(n); out == "" && n >= 0 {
			t.Errorf("cacheSparkline(%d) 为空", n)
		}
	}
	agent, cfg, _ := newHandleCmdAgent(t)
	out := captureStdout(t, func() { printTuiBanner(cfg) })
	_ = out
	if b := buildBanner(cfg); len(b) == 0 {
		t.Error("buildBanner 返回空")
	}
	if s := sessionSummaryCard(agent); s == "" {
		t.Error("sessionSummaryCard 为空")
	}
}

func TestCovUI_ThemePersistence(t *testing.T) {
	// go.mod 声明 go1.22, t.Chdir 需 go1.24 (go vet 门不过) → 手工 chdir + cleanup 兜底。
	prevWD, wdErr := os.Getwd()
	if wdErr != nil {
		t.Fatalf("获取当前目录失败: %v", wdErr)
	}
	if chErr := os.Chdir(t.TempDir()); chErr != nil {
		t.Fatalf("切换临时目录失败: %v", chErr)
	}
	t.Cleanup(func() { _ = os.Chdir(prevWD) })
	// 默认主题名可能为空(未初始化), 只要不 panic 即可; 持久化路径下面校验
	_ = tuiThemeLoad()
	if !setTheme("cold") {
		t.Error("setTheme(cold) 应成功")
	}
	if got := tuiThemeLoad(); got != "cold" {
		t.Errorf("主题持久化失败: %q", got)
	}
	if setTheme("不存在的主题") {
		t.Error("非法主题名应返回 false")
	}
	tuiThemeSave()
	for _, name := range []string{"neon", "cold", "warm"} {
		if !setTheme(name) {
			t.Errorf("setTheme(%q) 失败", name)
		}
		if acc := tuiAccent(); acc == "" {
			t.Errorf("主题 %q 的 accent 为空", name)
		}
	}
	if info := tuiThemeInfo(); !strings.Contains(info, tuiThemeLoad()) {
		t.Errorf("tuiThemeInfo 与当前主题不一致: %q", info)
	}
	if p := tuiPrimePalette(); len(p) == 0 {
		t.Error("tuiPrimePalette 为空")
	}
}
