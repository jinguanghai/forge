// tui_style_test.go: TUI 主题、配色与卡片渲染的行为契约。
//
// 注意: tuiThemeLoad/Save 走相对路径 ".forge/theme" —— 测试必须切到临时目录,
// 否则会覆盖真实主题文件。
package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// withTempCwd 把进程工作目录切到临时目录, 测试结束恢复。
// 仅供依赖相对路径的函数(tuiTheme*)使用。
func withTempCwd(t *testing.T) string {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

func withThemeName(t *testing.T) {
	t.Helper()
	old := tuiThemeName
	t.Cleanup(func() { tuiThemeName = old })
}

func TestTuiStyle_Itoa(t *testing.T) {
	for _, n := range []int{0, 1, 7, 9, 10, 42, 100, 999, 12345, -1, -99, -12345} {
		if got, want := itoa(n), strconv.Itoa(n); got != want {
			t.Errorf("itoa(%d) = %q 期望 %q", n, got, want)
		}
	}
}

func TestTuiStyle_Fg(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "\033[30m"},
		{7, "\033[37m"},
		{8, "\033[90m"},
		{13, "\033[95m"},
	}
	for _, c := range cases {
		if got := fg(c.n); got != c.want {
			t.Errorf("fg(%d) = %q 期望 %q", c.n, got, c.want)
		}
	}
}

func TestTuiStyle_CenterText(t *testing.T) {
	if got := centerText("ab", 0); got != "ab" {
		t.Errorf("w<=0 应原样返回, 实际 %q", got)
	}
	if got := centerText("ab", 6); got != "  ab" {
		t.Errorf("居中填充不符: %q", got)
	}
	// 超宽 → 交给 fitWidth 截断, 宽度不得超 w
	long := centerText(strings.Repeat("x", 20), 8)
	if w := displayWidth(long); w > 8 {
		t.Errorf("超宽未截断: %q 宽度 %d", long, w)
	}
	// 中文按 2 列计
	// 左填充 = (6-4)/2 = 1, 函数不补右侧空格
	if got := centerText("中文", 6); got != " 中文" {
		t.Errorf("中文居中不符: %q", got)
	}
}

func TestTuiStyle_TuiGradient(t *testing.T) {
	if got := tuiGradient("abc", nil); got != "abc" {
		t.Errorf("空 palette 应原样返回, 实际 %q", got)
	}
	pal := []string{fg(13), fg(5)}
	got := tuiGradient("a b", pal)
	if stripANSI(got) != "a b" {
		t.Errorf("上色不得改变文本: %q", got)
	}
	// 空白不逐字上色: 重置码数量 == 非空白字符数
	if n, want := strings.Count(got, ansi.reset), 2; n != want {
		t.Errorf("重置码 %d 个 期望 %d(仅非空白字符上色)", n, want)
	}
	// 同输入同输出(逐字节)
	if again := tuiGradient("a b", pal); again != got {
		t.Errorf("渐变不确定: %q vs %q", got, again)
	}
}

func TestTuiStyle_TuiCard(t *testing.T) {
	got := tuiCard("标题", []string{"l1", "l2"}, 40, fg(13))
	if !strings.Contains(got, "╭") || !strings.Contains(got, "╰") {
		t.Errorf("卡片缺边框: %q", got)
	}
	plain := stripANSI(got)
	for _, want := range []string{"标题", "l1", "l2"} {
		if !strings.Contains(plain, want) {
			t.Errorf("卡片缺 %q: %q", want, plain)
		}
	}
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) != 5 { // 上边框 + 标题 + 2 行 + 下边框
		t.Errorf("卡片行数 = %d 期望 5:\n%s", len(lines), plain)
	}
	// width < 20 时提升到 20(否则内宽为负, Repeat 会 panic)
	narrow := stripANSI(tuiCard("", []string{"x"}, 5, ""))
	if !strings.Contains(narrow, "x") {
		t.Errorf("窄宽度卡片异常: %q", narrow)
	}
}

func TestTuiStyle_CacheSparkline(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)

	// 无数据 → 空串(调用方据此决定是否显示)
	if got := cacheSparkline(5); got != "" {
		t.Errorf("无数据应返回空串, 实际 %q", got)
	}

	writeCacheStatRows(t, dir, []CacheStat{
		{Model: "m", Hit: 100, Miss: 0}, // 全命中 → 最高块
		{Model: "m", Hit: 0, Miss: 100}, // 全未命中 → 最低块
		{Model: "m", Hit: 50, Miss: 50}, // 半命中 → 中间块
	})
	got := cacheSparkline(5)
	runes := []rune(got)
	if len(runes) != 3 {
		t.Fatalf("火花线长度 = %d 期望 3: %q", len(runes), got)
	}
	// 确定性: 同数据两次调用逐字节相同
	if again := cacheSparkline(5); again != got {
		t.Errorf("火花线不确定: %q vs %q", got, again)
	}
	// 不同命中率必须产出不同字符, 否则图表没有区分度
	if runes[0] == runes[1] {
		t.Errorf("全命中与全未命中产出相同字符 %q —— 图表失去区分度", runes[0])
	}

	// 必须是真实块字符 U+2581~U+2588。
	// 早期实现 blocks[idx] 按【字节】索引 string(▁ = E2 96 81), 输出 U+00E2 / U+0096
	// 这类字符 → 火花线在终端显示为乱码, 图表信息全丢。
	for i, r := range runes {
		if r < 0x2581 || r > 0x2588 {
			t.Errorf("第 %d 个字符 %q (U+%04X) 不是块字符 —— 火花线会显示为乱码", i, string(r), r)
		}
	}
	// 方向: 全命中=最高块 █, 全未命中=最低块 ▁(反了图表就读不出来)
	if runes[0] != '█' {
		t.Errorf("全命中应为最高块 █, 实际 %q", string(runes[0]))
	}
	if runes[1] != '▁' {
		t.Errorf("全未命中应为最低块 ▁, 实际 %q", string(runes[1]))
	}

	// 窗口只取最近 n 条
	if w := []rune(cacheSparkline(1)); len(w) != 1 {
		t.Errorf("窗口 n=1 应只取 1 条, 实际 %d", len(w))
	}
}

func TestTuiStyle_ThemeRoundTrip(t *testing.T) {
	withTempCwd(t)
	withThemeName(t)

	for _, name := range []string{"neon", "cold", "warm"} {
		if !setTheme(name) {
			t.Fatalf("setTheme(%q) 应成功", name)
		}
		if tuiThemeName != name {
			t.Errorf("主题未生效: %q", tuiThemeName)
		}
		// 落盘后重新加载应保持
		tuiThemeName = ""
		if got := tuiThemeLoad(); got != name {
			t.Errorf("重载后 = %q 期望 %q", got, name)
		}
		b, err := os.ReadFile(filepath.Join(".forge", "theme"))
		if err != nil {
			t.Fatalf("主题文件未写出: %v", err)
		}
		if strings.TrimSpace(string(b)) != name {
			t.Errorf("主题文件内容 = %q 期望 %q", string(b), name)
		}
	}

	// 非法主题名必须拒绝且不改动当前主题
	setTheme("cold")
	if setTheme("bogus") {
		t.Error("非法主题名应被拒绝")
	}
	if tuiThemeName != "cold" {
		t.Errorf("拒绝后主题被改动: %q", tuiThemeName)
	}
}

func TestTuiStyle_ThemeAccentAndPalette(t *testing.T) {
	withTempCwd(t)
	withThemeName(t)

	setTheme("cold")
	if got := tuiAccent(); got != fg(6) {
		t.Errorf("cold 强调色 = %q 期望 %q", got, fg(6))
	}
	if len(tuiPrimePalette()) != len(coldPalette) {
		t.Errorf("cold 调色板长度不符")
	}
	setTheme("warm")
	if got := tuiAccent(); got != fg(3) {
		t.Errorf("warm 强调色 = %q 期望 %q", got, fg(3))
	}
	// 空主题名 → 默认 neon
	tuiThemeName = ""
	if got := tuiAccent(); got != fg(13) {
		t.Errorf("默认强调色 = %q 期望 %q", got, fg(13))
	}
	if len(tuiPrimePalette()) != len(neonPalette) {
		t.Errorf("默认调色板应为 neon")
	}
	if !strings.Contains(tuiThemeInfo(), "neon") {
		t.Errorf("空主题应报 neon: %q", tuiThemeInfo())
	}
	tuiThemeName = "cold"
	if !strings.Contains(tuiThemeInfo(), "cold") {
		t.Errorf("主题信息不符: %q", tuiThemeInfo())
	}
}

func TestTuiStyle_BuildBanner(t *testing.T) {
	withTempCwd(t)
	withThemeName(t)
	cfg := &Config{WorkDir: t.TempDir()}
	lines := buildBanner(cfg)
	if len(lines) == 0 {
		t.Fatal("横幅为空")
	}
	plain := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "铸剑炉 FORGE") {
		t.Errorf("横幅缺标识: %q", plain)
	}
	if !strings.Contains(plain, "未配置") {
		t.Errorf("空 Model/BaseURL 应显示未配置: %q", plain)
	}
	if !strings.Contains(plain, "v"+AppVersion) {
		t.Errorf("横幅缺版本号: %q", plain)
	}
	// 同输入同输出(除时刻行), 至少不得 panic 且非空
	if again := buildBanner(cfg); len(again) != len(lines) {
		t.Errorf("横幅行数不稳定: %d vs %d", len(again), len(lines))
	}
}
