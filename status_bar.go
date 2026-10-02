package main

import (
	"fmt"
	"strings"
)

// status_bar.go — REPL 底部常驻状态栏 (档位A: 每轮刷新)。
// 每次进入输入前, 在终端可视窗口底部绘制一行状态栏:
//   缓存命中率(近20条) | 会话统计(轮次/令牌/工具/耗时/时长)
// 幂等且失败静默: 非 TTY/无法取终端尺寸时直接返回, 绝不干扰主流程。

// drawStatusBar 绘制一次状态栏, 并把光标移到状态栏上一行供 readLine 起笔。
func drawStatusBar(agent *AgentRunner, workDir string) {
	// 兜底: 确保控制台探针已初始化(修复首轮/非 readLine 触发上下文状态栏缺失)
	ensureConsoleProbe()
	top, bottom, ok := consoleWindowRect()
	if !ok {
		return
	}
	h := bottom - top + 1
	if h < 2 {
		return
	}
	// 腾空输入行: 输出撑满窗口时其末行正落在 bottom-1, 详见 reserveInputRow 注释。
	bottom = reserveInputRow(bottom)
	w := getTermWidth()
	if w <= 0 {
		w = 80
	}

	// 组装可见文本 (纯逻辑已抽到 statusBarText, 便于哨兵测试钉住宽度与去重)
	text := statusBarText(agent, w)

	// 深灰渐变底板 + 彩色文本; 不支持 256 背景时退化为反显。
	//
	// 坑点(实测): 背景块之前是"在 text 末尾追加 ansi.reset"。
	// 而 text 内部的 color() 每个都自带 ansi.reset —— 第一个 reset 会把背景一起清掉。
	// 修法: 每个 color() 之后的 reset 重新接上 bg, 而不是只在文本末尾补一次。
	bg := "\033[48;5;234m"
	bgOn := "\033[0m\033[48;5;234m" // sgr0(重置前景+属性) + 重设背景
	setCursorPos(0, bottom)
	fmt.Print("\033[K")
	// 拼接: bg + text, 但 text 中所有 ansi.reset 后面追加 bgOn 以保住背景连续。
	// 注意: 整段背景底色只设一次足够, 中段只是"颜色属性重置", 不会清掉背景。
	out := bg + strings.ReplaceAll(text, ansi.reset, bgOn)
	fmt.Print(out)
	setCursorPos(0, bottom-1)
}

// statusBarText 组装状态栏可见文本 (含 ANSI 配色; 不含底板与光标控制)。
//
// 抽成纯函数是为了让哨兵测试能直接钉住两件事:
//  1. 段内不得重复出现「模型」—— 旧版 midSeg 与 stats 末尾各一份, 真实会话实测 139 列 > 135 溢出,
//     末尾被截成 "模型:deeps" (主人看不出是截断还是真值)。
//  2. 补齐/截断后显示宽度不超过终端宽度 w —— 不截断时恰好铺满 w, 截断时 <= w。
func statusBarText(agent *AgentRunner, w int) string {
	// 缓存命中率: 优先会话累计, 无则回退近20条(见 statusBarCacheText)
	left := statusBarCacheText(agent, 20)
	attn := contextUsageText(agent)
	stats := "—"
	if agent != nil && agent.stats != nil {
		stats = agent.stats.StringZh()
	}
	// 优先级装配 (2026-10-02 新增「注意力剩余」段): 缓存 > 注意力 > 会话统计。
	// 三段合计在 100 列窄终端放不下, 故由「整体拼接后截尾」改为「按段累加」:
	// 宽度不够时截断当前段(带省略号)并丢弃其后各段 —— 被牺牲的必须是末尾的
	// 「计算耗时/会话时长」(别处可查), 而不是「快忘事了」的预警。
	// 向后兼容: attn 为空(无 cfg/无阈值)时装配结果与旧版逐字节相同。
	// 两色分区: 缓存(绿) | 注意力(绿/黄/红) | 会话统计(dim)。
	segs := []string{" " + left, " " + attn, " " + color(ansi.dim, stats) + " "}
	out := ""
	for _, s := range segs {
		if s == " " || s == "" {
			continue // 注意力段不可用时跳过, 不占宽度
		}
		sep := ""
		if out != "" {
			sep = "  "
		}
		cand := out + sep + s
		if displayWidth(cand) <= w {
			out = cand
			continue
		}
		avail := w - displayWidth(out) - displayWidth(sep)
		if avail < 8 {
			break // 剩余宽度放不下有意义的片段: 丢弃本段及后续 (至少保住前段)
		}
		out += sep + truncateDisplay(s, avail)
		break
	}
	// 宽度预算仍取 w。曾疑心"写满最后一列会触发终端延迟换行→窗口下移→状态栏被顶离底行"
	// 而改成 w-1; 用真实 Go 控制台程序实测(2026-10-01, 铺满 w 列后窗口底行不变 ——
	// 延迟换行被随后的 setCursorPos 定位取消)后回退, 保留原「恰好铺满 w」契约
	// (底板连续, 哨兵 status_bar_layout_test.go 钉住)。
	disp := displayWidth(out)
	if disp > w {
		return truncateDisplay(out, w)
	}
	if disp < w {
		return out + strings.Repeat(" ", w-disp)
	}
	return out
}

// contextUsageText 状态栏「注意力剩余」文本段 (含配色); 不可判定时返回 ""。
//
// 口径 (2026-10-02 修正): 剩余% = 100 - contextBudgetUsed(agent)*100/CompactTokenThreshold
//
// 分子必须剔除固定头 —— 修正前用 estimateTokens(agent.history) 全量, 主人实测
// 「重启显示剩7%、聊两轮变0%」: 固定头 = system提示 + 记忆锚点 + 折叠索引, 实测
// 18749 token (system 3764 + 记忆 14687 + 折叠 298), 已占默认阈值 20000 的 93.7%,
// 于是状态栏一开机就报「剩7%」, 而对话内容几乎是空的。旧口径量的是"记忆有多大",
// 不是"注意力还剩多少"; 且固定头受保护不可压缩 (maybeCompact 明确跳过 head),
// 本就不该进对话预算。剔除后刚启动 = 剩100%, 与直觉一致。
//
// 为什么分母取「压缩阈值」而不是模型上下文窗口(1M):
//  1. 1M 窗口下永远显示"剩 99%", 零信息量, 等于没显示;
//  2. CompactTokenThreshold 是系统真正会动手的那条线 —— 越线触发历史摘要压缩,
//     压缩后细节丢失, 那才是主人能感知的"忘事"。故「注意力剩余」= 距离忘事的距离。
//  3. 与 maybeCompact (agent_trim.go) 共用 contextBudgetUsed 同一函数与同一阈值,
//     显示口径与触发口径同源 —— 不会出现"显示还剩一半却已经压缩了"的自相矛盾。
//
// 注意 estimateTokens 是保守上界(ASCII 也按 1 rune=1 token), 故显示偏向"少",
// 对预警而言宁可早报, 方向安全。
//
// 颜色分级: 剩 >50% 绿 / 20~50% 黄 / <20% 红 (红了 = 即将压缩, 该收尾或换会话)。
func contextUsageText(agent *AgentRunner) string {
	if agent == nil || agent.cfg == nil {
		return ""
	}
	limit := agent.cfg.CompactTokenThreshold
	if limit <= 0 {
		return ""
	}
	used := contextBudgetUsed(agent) // 剔除固定头, 与 maybeCompact 触发判据同源
	pct := 100 - used*100/limit
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	label := fmt.Sprintf("注意力 剩%d%%", pct)
	switch {
	case pct < 20:
		return color(ansi.red, label)
	case pct <= 50:
		return color(ansi.yellow, label)
	default:
		return color(ansi.green, label)
	}
}

// statusBarCacheText 计算状态栏缓存命中率文本段。
// 优先当前会话累计命中率(实时, type=cache_usage 驱动), 无则回退近 n 条历史平均,
// 再无命中/未命中样本时显示 "--"。
func statusBarCacheText(agent *AgentRunner, n int) string {
	rate, nRecs, rok := cacheHitRate(n)
	if agent != nil && agent.stats != nil {
		if sr, sok := agent.stats.cacheRate(); sok {
			return color(ansi.green, fmt.Sprintf("缓存命中 %.1f%% 会话", sr))
		}
		if rok {
			return color(ansi.green, fmt.Sprintf("缓存命中 %.1f%% 近%d条", rate, nRecs))
		}
		return color(ansi.dim, "缓存命中 --")
	}
	if rok {
		return color(ansi.green, fmt.Sprintf("缓存命中 %.1f%% 近%d条", rate, nRecs))
	}
	return color(ansi.dim, "缓存命中 --")
}

// truncateDisplay 按显示宽度截断字符串到不超过 col 列, 截断时自动追加省略号 "…" (1 列)。
//
// 口径: 与 fitWidth 完全一致 (ANSI 转义序列原样保留且不占宽度, 残缺序列丢弃, 末尾补省略号)。
// 20260930: 与 fitWidth 合并 —— 两份逻辑漂移风险大于重复成本, 改用单一源。
// 修复点 (原 bug): ①旧版逐 rune 计宽把 ESC 字节也算 1 列 → 可见内容被提前截断;
//
//	②截断时无省略号 → 主人看不出"deeps"是溢出还是 deepseek-flash。
func truncateDisplay(s string, col int) string {
	if col <= 0 {
		return ""
	}
	return fitWidth(s, col)
}

// maxReserveNewlines 腾行换行数上限。正常场景只需 1~2 个 (光标在窗口底行或其上一行),
// 此上限只用于异常位置(窗口未跟随光标等)下封顶, 避免刷屏式滚动。
const maxReserveNewlines = 8

// reserveInputRow 在绘制状态栏/输入提示符前腾空「输入行」(状态栏上一行)。
//
// 背景 (2026-10-01 截屏实测): 输出撑满可见窗口后, 输出末行正落在输入行上;
// drawStatusBar 随后把光标定位到 bottom-1, readLine 又直接 fmt.Print(prompt) 不清行
// —— prompt 覆盖输出末行开头, 两段文字挤在同一行, 表现为「输出溢出到输入端」
// (截屏原样: 「炉·R » 给出的"像真的"结果比没有判据更危险…」)。
//
// 修法: 用换行触发窗口滚动, 把输出内容整体上移, 在底部腾出空行给输入行。
// 为什么用换行而不是光标定位: 相对/绝对定位只移动光标, 不会滚动窗口内容;
// 换行是唯一能让内容上移、在底部造出空行的终端原语(实测 conhost 下光标位于
// 窗口底行时换行即滚动一行, 且窗口 top/bottom 同步下移)。
//
// 返回滚动后的窗口底行(调用方必须用它重新定位); 控制台 API 不可用时原样返回,
// 绝不干扰主流程。哨兵: status_bar_reserve_test.go。
func reserveInputRow(bottom int) int {
	pos, ok := getCursorPos()
	if !ok {
		return bottom
	}
	n := inputReserveNewlines(int(pos.X), int(pos.Y), bottom)
	if n <= 0 {
		return bottom
	}
	fmt.Print(strings.Repeat("\n", n))
	if _, nb, ok2 := consoleWindowRect(); ok2 {
		return nb
	}
	return bottom
}

// inputReserveNewlines 计算腾空输入行所需的换行数 (纯函数, 便于哨兵测试)。
//
// 模型: 光标是「最后写入位置」, 故光标行之前(含)的行都写过内容 ——
//   - curX == 0 (光标停在行首): 内容末行 = curY-1
//   - curX >  0 (光标停在行中): 内容末行 = curY
//
// 约束: bottom-1 留给输入行, bottom 留给状态栏, 故内容末行必须 <= bottom-2。
// 需上滚 need = 内容末行 - (bottom-2) 行; 而一个换行只有在光标已位于窗口底行时
// 才触发滚动, 所以还要 (bottom-curY) 个换行先把光标送到窗口底行。
func inputReserveNewlines(curX, curY, bottom int) int {
	if curY > bottom {
		return 0 // 光标不在窗口内(用户滚动过窗口): 不做无根据的滚动
	}
	need := curY - bottom + 2
	if curX == 0 {
		need--
	}
	if need <= 0 {
		return 0 // 输入行本就是空行
	}
	n := (bottom - curY) + need
	if n > maxReserveNewlines {
		return maxReserveNewlines
	}
	if n < 0 {
		return 0
	}
	return n
}
