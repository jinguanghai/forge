package main

// tui_style.go — 炫酷终端样式层。
// 提供 渐变色发射器 / 圆角卡片 / Banner 生成 / 主题色板。
// 原则: 所有新 TUI 文案统一走本文件, 禁止再手写裸 ANSI。
// 兼容: 全部基于标准 ANSI 16/256 色 + 修饰符, Windows/Unix 通用。

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tuiGradient 为字符串 s 逐字符应用 palette(前景色代码序列) 形成渐变。
func tuiGradient(s string, palette []string) string {
	if len(palette) == 0 {
		return s
	}
	var sb strings.Builder
	i := 0
	for _, r := range []rune(s) {
		// 空白字符不逐字上色, 避免大量的裸颜色码噪声。
		if r == ' ' || r == '\t' || r == '\n' {
			sb.WriteRune(r)
			continue
		}
		sb.WriteString(palette[i%len(palette)])
		sb.WriteRune(r)
		sb.WriteString(ansi.reset)
		i++
	}
	return sb.String()
}

// fg 生成 16 色前景码: n<8 普通(30+n), n>=8 高亮(90+n-8)。
func fg(n int) string {
	if n < 8 {
		return "\033[" + itoa(30+n) + "m"
	}
	return "\033[" + itoa(90+n-8) + "m"
}

// itoa 手写 int→string。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// 科技感霓虹色板 (青→蓝→紫→品红→青)。
var neonPalette = []string{fg(13), fg(5), fg(14), fg(12), fg(6), fg(4)}

// 冷青渐变 (默认主色)。
var coldPalette = []string{fg(6), fg(12), fg(14), fg(4), fg(6)}

// 暖金渐变 (提示/强调)。
var warmPalette = []string{fg(3), fg(11), fg(9), fg(1), fg(3)}

// ─── 圆角卡片 ─────────────────────────

// tuiCard 生成圆角边框卡片。acc 为边框颜色码。
func tuiCard(title string, lines []string, width int, acc string) string {
	if width < 20 {
		width = 20
	}
	inW := width - 4
	var sb strings.Builder
	sb.WriteString(acc + " ╭─" + strings.Repeat("─", inW) + "─╮" + ansi.reset + "\n")
	if title != "" {
		tl := fitWidth(title, inW)
		sb.WriteString(acc + " │ " + tl + strings.Repeat(" ", inW-displayWidth(tl)) + " │" + ansi.reset + "\n")
	}
	for _, ln := range lines {
		cl := fitWidth(ln, inW)
		padding := inW - displayWidth(cl)
		if padding < 0 {
			padding = 0
		}
		sb.WriteString(acc + " │ " + cl + strings.Repeat(" ", padding) + " │" + ansi.reset + "\n")
	}
	sb.WriteString(acc + " ╰─" + strings.Repeat("─", inW) + "─╯" + ansi.reset + "\n")
	return sb.String()
}

// centerText 将 s 在给定显示宽度内居中(左侧补一半空格)。
func centerText(s string, w int) string {
	if w <= 0 {
		return s
	}
	d := displayWidth(s)
	if d >= w {
		return fitWidth(s, w)
	}
	left := (w - d) / 2
	return strings.Repeat(" ", left) + s
}

// ─── Banner 生成 ─────────────────────────

// buildBanner 生成启动炫酷横幅(多行, 已含 ANSI)。
func buildBanner(cfg *Config) []string {
	w := getTermWidth()
	if w < 60 {
		w = 60
	}
	if w > 100 {
		w = 100
	}
	inW := w - 6
	logo := "■ 铸剑炉 FORGE ■"
	sub := "LLM 驱动的多语言编译器沙箱 · 多 Gate"
	model := cfg.Model
	if model == "" {
		model = "(未配置)"
	}
	base := cfg.BaseURL
	if base == "" {
		base = "(未配置)"
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	ver := "v" + AppVersion
	logoLine := tuiGradient(centerText(logo, inW), tuiPrimePalette())
	subLine := tuiGradient(centerText(sub, inW), coldPalette)
	info := []string{
		" " + dim("模型") + "  " + bold(model),
		" " + dim("网 关") + "  " + bold(base),
		" " + dim("版 本") + "  " + bold(ver) + "   " + dim("时刻") + "  " + now,
	}
	// 无剑状态位：仅在 FORGE_NOSWORD=1 时显示。此前该功能完全静默（开关三种
	// 取值下横幅输出逐字节相同），用户无法判断是否生效 —— 此处补可感知信号。
	// 注：横幅只经 fmt.Println 走 stdout，不进 ChatMessage，不影响请求前缀缓存。
	if nswEnabled() {
		info = append(info, " "+dim("无 剑")+"  "+bold("已启用")+"   "+dim("算式求值兜底"))
	}
	body := append([]string{"", logoLine, subLine, ""}, info...)
	card := tuiCard("", body, w, tuiAccent())
	return strings.Split(strings.TrimSuffix(card, "\n"), "\n")
}

// printTuiBanner 打印炫酷横幅至 stdout。
func printTuiBanner(cfg *Config) {
	tuiThemeLoad()
	for _, ln := range buildBanner(cfg) {
		fmt.Println(ln)
	}
	fmt.Println()
}

// ─── 缓存趋势 sparkline ─────────────────────────

// cacheSparkline 读取最近 n 条缓存记录, 以 ▁▂▃▄▅▆▇█ 块绘制命中率趋势。
func cacheSparkline(n int) string {
	if n <= 0 {
		n = 20
	}
	blocks := "\u2581\u2582\u2583\u2584\u2585\u2586\u2587\u2588"
	type rec struct{ hit, miss int }
	var rows []rec
	cacheStatMu.Lock()
	f, err := os.Open(cacheStatPath)
	if err != nil {
		cacheStatMu.Unlock()
		return ""
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var s CacheStat
		if json.Unmarshal(sc.Bytes(), &s) != nil {
			continue
		}
		rows = append(rows, rec{s.Hit, s.Miss})
	}
	_ = f.Close()
	cacheStatMu.Unlock()
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	var sb strings.Builder
	for _, r := range rows {
		tot := r.hit + r.miss
		idx := 0
		if tot > 0 {
			ratio := float64(r.hit) / float64(tot)
			idx = int(ratio * float64(8-1))
			if idx > 7 {
				idx = 7
			}
			if idx < 0 {
				idx = 0
			}
		}
		sb.WriteRune(rune(blocks[idx]))
	}
	return sb.String()
}

// ─── 会话结算面板 ─────────────────────────

// sessionSummaryCard 生成会话结束的炫酷结算卡 (数据可视化)。
func sessionSummaryCard(agent *AgentRunner) string {
	if agent == nil || agent.stats == nil {
		return ""
	}
	w := getTermWidth()
	if w < 40 {
		w = 40
	}
	if w > 90 {
		w = 90
	}
	st := agent.stats
	elapsed := time.Since(st.StartTime).Round(time.Second)
	lines := []string{
		" " + bold("轮次") + "  " + itoa(st.Turns) + "    " + bold("令牌") + "  " + itoa(int(st.TotalTokens)),
		" " + bold("工具") + "  " + color(ansi.green, fmt.Sprintf("%d \u2713", st.ToolOK)) + " / " + color(ansi.red, fmt.Sprintf("%d \u2717", st.ToolFail)),
		" " + bold("计算耗时") + "  " + (time.Duration(st.TotalMs) * time.Millisecond).String() + "     " + bold("会话时长") + "  " + elapsed.String(),
		" " + bold("模型") + "  " + st.LastModel,
		"",
		" " + bold("缓存趋势") + "  " + color(ansi.cyan, cacheSparkline(20)),
	}
	return tuiCard("\u1f319 \u4f1a\u8bdd\u7ed3\u7b97", lines, w, tuiAccent())
}

// ─── 主题系统 ─────────────────────────

// tuiThemeName 当前主题名: neon / cold / warm。默认 neon。
var tuiThemeName string

// tuiThemeLoad 从 .forge/theme 读取持久化主题名 (幂等, 失败静默)。
func tuiThemeLoad() string {
	b, err := os.ReadFile(filepath.Join(".forge", "theme"))
	if err == nil {
		s := strings.TrimSpace(string(b))
		if s == "neon" || s == "cold" || s == "warm" {
			tuiThemeName = s
		}
	}
	return tuiThemeName
}

// tuiThemeSave 将当前主题名持久化到 .forge/theme。
func tuiThemeSave() {
	_ = os.MkdirAll(".forge", 0755)
	_ = os.WriteFile(filepath.Join(".forge", "theme"), []byte(tuiThemeName), 0644)
}

// setTheme 切换主题, 返回是否成功。
func setTheme(name string) bool {
	if name != "neon" && name != "cold" && name != "warm" {
		return false
	}
	tuiThemeName = name
	tuiThemeSave()
	return true
}

// tuiAccent 返回当前主题的边框/强调色码。
func tuiAccent() string {
	switch tuiThemeName {
	case "warm":
		return fg(3)
	case "cold":
		return fg(6)
	default:
		return fg(13)
	}
}

// tuiPrimePalette 返回当前主题的主渐变 palette。
func tuiPrimePalette() []string {
	switch tuiThemeName {
	case "warm":
		return warmPalette
	case "cold":
		return coldPalette
	default:
		return neonPalette
	}
}

func tuiThemeInfo() string {
	if tuiThemeName == "" {
		return "主题: neon"
	}
	return "主题: " + tuiThemeName
}
