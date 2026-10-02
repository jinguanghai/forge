// main_utils.go: 主程序杂项工具函数

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ─── Welcome screen ─────────────────────────────────────────

// displayWidth 计算字符串的终端显示宽度（跳过 ANSI 码，CJK 字符计2列）
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func displayWidth(s string) int {
	// 去掉 ANSI 转义序列
	clean := ansiEscapeRe.ReplaceAllString(s, "")
	w := 0
	for _, r := range clean {
		w += runeWidth(r) // 统一宽度判定（width.go），与行编辑器一致
	}
	return w
}

// fitWidth 将字符串截断到 maxW 显示宽度（超宽时以 … 结尾），保留 ANSI 转义序列。
// 用于欢迎画面等定宽排版：任何超长内容（模型名/接口等）都不会破坏边框。
//
// 安全: 即使输入未超宽, 也必须扫描全串丢弃残缺 ANSI (无 'm' 终结符的序列)。
// 否则残缺 ESC 序列会原样返回 → 终端把后续文本当作 CSI 参数解析 → 颜色错乱/吞输出。
// 测试: TestFitWidth_DropsMalformedEscape (main_helpers_test.go)。
func fitWidth(s string, maxW int) string {
	// 先扫描: 剥离残缺 ANSI, 得到"安全"输入
	safe := stripMalformedEscape(s)
	if displayWidth(safe) <= maxW {
		return safe
	}
	var sb strings.Builder
	w := 0
	needEllipsis := false
	for len(safe) > 0 {
		if safe[0] == 0x1b { // ANSI 转义序列原样保留（不占显示宽度）
			idx := strings.IndexByte(safe, 'm')
			if idx < 0 {
				break
			}
			sb.WriteString(safe[:idx+1])
			safe = safe[idx+1:]
			continue
		}
		r, size := utf8.DecodeRuneInString(safe)
		rw := runeWidth(r)
		if w+rw > maxW-1 {
			needEllipsis = true
			break
		}
		sb.WriteRune(r)
		w += rw
		safe = safe[size:]
	}
	if needEllipsis {
		sb.WriteString("…")
	}
	return sb.String()
}

// stripMalformedEscape 删除所有残缺 ANSI 序列 (无 'm' 终结符的 ESC 序列)。
// 完整序列 (ESC[...m) 原样保留 —— 它们不占可见宽度, 也不应被剥离。
func stripMalformedEscape(s string) string {
	var sb strings.Builder
	for len(s) > 0 {
		if s[0] == 0x1b {
			idx := strings.IndexByte(s, 'm')
			if idx < 0 {
				// 残缺: 整段剩余序列丢弃 (从 ESC 到结尾)
				// 但保留 ESC 之前的可见字符 (sb 中已有)
				// 这里 s[0] 即 ESC, 直接跳出循环, 不再写任何东西
				break
			}
			// 完整序列: 原样写
			sb.WriteString(s[:idx+1])
			s = s[idx+1:]
			continue
		}
		sb.WriteByte(s[0])
		s = s[1:]
	}
	return sb.String()
}

func printWelcome(cfg *Config) {
	// 升级: 调用炫酷横幅, 取代旧的单色 ASCII 框。
	printTuiBanner(cfg)
}

// ─── Commands ───────────────────────────────────────────────

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ─── Holiday table ───────────────────────────────────────────
// isChineseHoliday(id, t) 纯函数: t 这天是否为中国法定节假日 (春节/国庆等)。
// 加载: .forge/holidays_<year>.json (内嵌 holidays 数组, 每项 [YYYY-MM-DD, name])。
// 失败: 文件缺失或格式错 → 返回 false (fail-closed: 节假日判定不到即按"非节假日"算, 不会误抬高高峰)。
//
// 数据源: 国务院 2025-11-04 公布的《关于 2026 年部分节假日安排的通知》;
// 主调档源 api.apihubs.cn 单日抽样校验 (2026-02-17=春节 ✓)。本表硬编码 2026,
// 2027+ 需在主循环启动前扩展对应年的表 (setx FORGE_HOLIDAY_FILE 可指外部表)。
//
// isChristmasHolidayOnly=true 的日期是本地按摩日历节日 (元旦/春节等); 周末 = 周六+周日;
// 调休工作日 (国务院把周末改成工作日的, 例如 2026-02-14) 即使落在周末也不算"休息日"。
type holidayEntry struct {
	date string
	name string
}

var holidayTableCache = make(map[string][]holidayEntry)
var holidayTableLoaded = make(map[string]bool)

func loadHolidayTable(year string) []holidayEntry {
	if holidayTableLoaded[year] {
		return holidayTableCache[year]
	}
	holidayTableLoaded[year] = true
	// 路径: .forge/holidays_<year>.json (与 go.mod 同级, exe 同级)
	paths := []string{
		filepath.Join(".forge", "holidays_"+year+".json"),
		filepath.Join("holidays_" + year + ".json"),
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var doc struct {
			Holidays [][]string `json:"holidays"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}
		var entries []holidayEntry
		for _, h := range doc.Holidays {
			if len(h) >= 1 {
				entries = append(entries, holidayEntry{date: h[0], name: safeAt(h, 1)})
			}
		}
		holidayTableCache[year] = entries
		return entries
	}
	holidayTableCache[year] = nil
	return nil
}

// isChineseHoliday 纯函数: t 这天是否为中国法定节假日。
// 失败: 表缺失或日期未登记 → false (与"工作日"一致, 不破坏价格计算)。
func isChineseHoliday(t time.Time) bool {
	year := fmt.Sprintf("%04d", t.Year())
	key := fmt.Sprintf("%04d-%02d-%02d", t.Year(), int(t.Month()), t.Day())
	for _, e := range loadHolidayTable(year) {
		if e.date == key {
			return true
		}
	}
	return false
}

func safeAt(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// reloadHolidaysForTest 重置节假日缓存, 在表更新后可重载。
// 仅供测试 (文件名后缀 _test.go 调用)。
func reloadHolidaysForTest(year string) {
	delete(holidayTableLoaded, year)
	delete(holidayTableCache, year)
}

// ─── Peak-hour reminder ─────────────────────────────────────
// isPeakHourAt 纯函数: t 是否处于 DeepSeek 高峰时段 (价格×2)。
//
// 判据 = 工作日(周一~周五) 且 9:00-12:00 / 14:00-18:00 (右端开区间)。
// 周末不涨价 (官方峰谷定价按工作日计), 故周末一律 false。
// isMiniMaxWindow 复用本判据 —— 单一源, 避免两处时段定义各自漂移。
func isPeakHourAt(t time.Time) bool {
	wd := t.Weekday() // Sunday=0 ... Saturday=6
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	// 节假日按"非高峰"计: DeepSeek 官方定价节假日不设高峰时段 (节假日全天闲时单价)。
	// 表缺失/未登记 → false (fail-closed, 不误抬高高峰)。
	if isChineseHoliday(t) {
		return false
	}
	h := t.Hour()
	return (h >= 9 && h < 12) || (h >= 14 && h < 18)
}

// isPeakHour 返回当前是否为 DeepSeek 高峰时段 (价格×2)。
// 北京时间 9-12 / 14-18 (工作日)。checkPeakHour 与动态模型降档共用。
func isPeakHour() bool { return isPeakHourAt(time.Now()) }

// checkPeakHour 打印 DeepSeek 高峰时段提醒 (北京时间 9-12 / 14-18, 价格×2)。
// 省钱第一杠杆: 高峰价格翻倍, 影响比缓存命中率更大。
func checkPeakHour(cfg *Config) {
	// 高峰时段 + MiniMax 已配置 → 自动路由到 MiniMax, 无需用户躲高峰。
	if cfg != nil && cfg.MiniMaxAPIKey != "" && cfg.MiniMaxBaseURL != "" && cfg.MiniMaxModel != "" && minimaxWindowNow() {
		fmt.Fprintf(os.Stderr, "%s 高峰时段: 已自动路由到 MiniMax M3 省钱 (无需躲高峰)。\n", color(ansi.green, "✓"))
		fmt.Fprintf(os.Stderr, "   高峰(工作日9-12/14-18)=MiniMax-M3, 其余/周末=DeepSeek deepseek-flash。\n")
		return
	}
	h := time.Now().Hour()
	if isPeakHour() {
		fmt.Fprintf(os.Stderr, "%s DeepSeek 高峰时段 (价格×2): 当前 %02d:00 北京时间\n", color(ansi.yellow, "⚠️"), h)
		if cfg == nil || cfg.MiniMaxAPIKey == "" {
			fmt.Fprintf(os.Stderr, "   省钱建议: 非紧急任务请避开 9:00-12:00 / 14:00-18:00。\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "%s 当前非高峰时段 (价格正常)。\n", color(ansi.green, "✓"))
	}
}

// ─── History file ───────────────────────────────────────────

func appendHistory(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(fmt.Sprintf("%d|%s\n", time.Now().Unix(), escapeHistory(line)))
	// Close immediately: on Windows a held handle blocks os.Rename with
	// "used by another process", so the file must be fully closed before
	// any rotation attempt. (defer f.Close() would keep it open here.)
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: close history %s: %v\n", path, err)
	}

	// Rotate if too large (>100KB). Stat via the path, not the handle.
	if info, err := os.Stat(path); err == nil && info.Size() > 100*1024 {
		if err := os.Rename(path, path+".old"); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: failed to rotate log file %s: %v\n", path, err)
		}
	}
}

func readHistory(path string, maxLines int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, "|"); idx > 0 {
			line = line[idx+1:]
		}
		lines = append(lines, unescapeHistory(line))
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

// ─── Input helpers ─────────────────────────────────────────

// escapeHistory escapes newlines so a multi-line input is stored as a
// single history entry (the file is line-oriented).
func escapeHistory(s string) string {
	return strings.ReplaceAll(s, "\n", "\\n")
}

// unescapeHistory reverses escapeHistory.
func unescapeHistory(s string) string {
	return strings.ReplaceAll(s, "\\n", "\n")
}

// isPathLike reports whether s looks like a Windows path (drive letter
// or UNC root). Used to avoid treating trailing backslashes in paths as
// multiline continuations.
func isPathLike(s string) bool {
	if filepath.IsAbs(s) {
		return true
	}
	if len(s) >= 2 && s[1] == ':' &&
		((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) {
		return true
	}
	return false
}

// unbalancedDelimiters reports whether a line has unclosed (), [], {}, or
// quotes. Best-effort: brackets inside strings/comments are tracked so a
// multi-line code snippet continues naturally.
func unbalancedDelimiters(s string) bool {
	stack := []rune{}
	inStr := rune(0) // 0 = not in string; otherwise the quote rune
	escaped := false
	for _, r := range s {
		if inStr != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == inStr {
				inStr = 0
			}
			continue
		}
		switch r {
		case '"', '\'', '`':
			inStr = r
		case '(', '[', '{':
			stack = append(stack, r)
		case ')':
			if len(stack) > 0 && stack[len(stack)-1] == '(' {
				stack = stack[:len(stack)-1]
			} else {
				return false // mismatched closer — treat as complete
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			} else {
				return false
			}
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			} else {
				return false
			}
		}
	}
	return inStr != 0 || len(stack) > 0
}

// ─── Help ───────────────────────────────────────────────────

func printHelp() {
	fmt.Println("铸剑炉 " + versionString() + " — LLM 驱动的多语言编译器沙箱")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  forge.exe              交互模式（默认）")
	fmt.Println("  forge.exe --reasoning  显示LLM推理/思考过程")
	fmt.Println("  forge.exe -r           同 --reasoning")
	fmt.Println("  forge.exe --help       显示本帮助")
	fmt.Println("  forge.exe --version    显示版本")
	fmt.Println("  forge.exe \"查询\"      运行单次查询（非交互）")
	fmt.Println()
	fmt.Println("示例:")
	fmt.Println("  forge.exe \"列出当前目录的文件\"")
	fmt.Println("  forge.exe -r \"解释架构\"")
	fmt.Println()
	fmt.Println("环境变量:")
	fmt.Println("  DEEPSEEK_API_KEY     DeepSeek API密钥")
	fmt.Println("  DEEPSEEK_MODEL       固定模型 (设置后路由为 fixed 模式)")
	fmt.Println("  DEEPSEEK_MODEL_FLASH  轻量模型 (默认 deepseek-flash)")
	fmt.Println("  DEEPSEEK_MODEL_PRO    重量模型 (默认 deepseek-flash, V4-Pro 2026-09-14 下线)")
	fmt.Println("  DEEPSEEK_MODEL_VISION 视觉模型 (识图, 默认 deepseek-flash)")
	fmt.Println("  DEEPSEEK_ROUTER       路由模式 auto|flash|pro|fixed (默认 auto)")
	fmt.Println("  FORGE_MAX_CODE_SIZE  最大代码长度 (默认 512KB)")
	fmt.Println("  FORGE_TOOL_TIMEOUT   工具超时 (默认 60s)")
	fmt.Println("  回合数限制已取消: 主循环无轮数上限, 由连续失败/重复调用/上下文取消兜底")
}
