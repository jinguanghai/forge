package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// status_bar_context_test.go —— 状态栏「注意力剩余」段哨兵 (2026-10-02)。
//
// 需求: 底部状态栏显示注意力(上下文)剩余百分比, 让主人在"系统即将压缩历史、
// 开始忘事"之前看到预警。
//
// 口径 (2026-10-02 修正后, 与 status_bar.go contextUsageText 注释同源):
//
//	剩余% = 100 - contextBudgetUsed(agent)*100/CompactTokenThreshold
//	contextBudgetUsed = estimateTokens(history[headLen:])  ← 剔除固定头
//
// 修正动机(主人实测「重启显示剩7%、聊两轮变0%」): 旧口径分子取全量 history,
// 而固定头(system提示+记忆锚点+折叠索引)实测 18749 token, 已占默认阈值 20000 的
// 93.7% —— 一开机就报「剩7%」, 对话内容却几乎是空的。旧口径量的是"记忆有多大"。
//
// 判据分层:
//  1. 核心回归: 固定头不计入 (18749 token 的固定头 + 空对话 → 剩100%)
//  2. 口径: 剩% = 100 - 对话量*100/阈值
//  3. 颜色分级边界: >50 绿 / 20~50 黄 / <20 红
//  4. 降级: agent/cfg/阈值缺失 → 空串且不影响旧版逐字节输出
//  5. 优先级: 窄终端下被截断的必须是末尾会话统计, 注意力段必须完整可见
//  6. 同源: 显示与压缩触发共用 contextBudgetUsed (数值临界 + AST 接线双判据)
//
// 变异验证: ①把 contextBudgetUsed 里的 head 剔除去掉(改回全量) → 第1组转红;
//          ②把 maybeCompact 的触发改回 estimateTokens(a.history) → 第10组转红。

// attnTestLimit 取 config.go 默认值 (AGENT_COMPACT_TOKEN_THRESHOLD, 默认20000)。
const attnTestLimit = 20000

// newAttentionTestAgent 构造 headLen=1、固定头 headTokens 个 rune、对话 dialogTokens 个 rune 的 agent。
// estimateTokens 对每个 rune 计 1 (ASCII 与 CJK 同口径), 故 strings.Repeat("a", n) 恰好 n token。
func newAttentionTestAgent(headTokens, dialogTokens int) *AgentRunner {
	a := newStatusBarTestAgent()
	a.cfg = &Config{CompactTokenThreshold: attnTestLimit}
	a.headLen = 1
	hist := []ChatMessage{{Role: "system", Content: strings.Repeat("h", headTokens)}}
	if dialogTokens > 0 {
		hist = append(hist, ChatMessage{Role: "user", Content: strings.Repeat("a", dialogTokens)})
	}
	a.history = hist
	return a
}

// ── 1. 核心回归: 固定头必须不计入预算 ────────────────────────
//
// 这是主人报的 bug 的哨兵: 固定头 18749 token (真实量级: system 3764 + 记忆 14687
// + 折叠 298) + 空对话 → 旧口径显示「剩7%」(红), 新口径必须显示「剩100%」(绿)。
func TestContextUsageText_IgnoresFixedHead(t *testing.T) {
	a := newAttentionTestAgent(18749, 0)
	raw := contextUsageText(a)
	if got := stripANSI(raw); !strings.Contains(got, "剩100%") {
		t.Errorf("固定头 18749 token + 空对话 应显示剩100%%, 实际 %q (口径未剔除固定头?)", got)
	}
	if !strings.HasPrefix(raw, ansi.green) {
		t.Errorf("空对话应判绿, 实际 %q", raw)
	}
	// 对话 2000 token (约两轮) → 剩90%, 仍绿 (旧口径此时已是 0%/红)
	a2 := newAttentionTestAgent(18749, 2000)
	if got := stripANSI(contextUsageText(a2)); !strings.Contains(got, "剩90%") {
		t.Errorf("固定头 + 对话2000 应显示剩90%%, 实际 %q", got)
	}
	// 边界: 固定头恰好等于阈值, 对话为空 → 仍 100%
	a3 := newAttentionTestAgent(attnTestLimit, 0)
	if got := stripANSI(contextUsageText(a3)); !strings.Contains(got, "剩100%") {
		t.Errorf("固定头==阈值 且空对话 应显示剩100%%, 实际 %q", got)
	}
}

// ── 2. 口径: 剩% = 100 - 对话量*100/阈值 ────────────────────
func TestContextUsageText_Percent(t *testing.T) {
	cases := []struct {
		dialog int
		want   string
	}{
		{0, "剩100%"},
		{5000, "剩75%"},
		{12000, "剩40%"},
		{17000, "剩15%"},
		{attnTestLimit, "剩0%"},
		{30000, "剩0%"}, // 超阈值 clamp 到 0, 不得出现负数
	}
	for _, c := range cases {
		got := stripANSI(contextUsageText(newAttentionTestAgent(0, c.dialog)))
		if !strings.Contains(got, c.want) {
			t.Errorf("dialog=%d: 期望含 %q, 实际 %q", c.dialog, c.want, got)
		}
	}
}

// ── 3. 颜色分级边界: >50 绿 / 20~50 黄 / <20 红 ─────────────
func TestContextUsageText_ColorGrade(t *testing.T) {
	cases := []struct {
		dialog int
		want   string
	}{
		{5000, ansi.green},   // 75%
		{9800, ansi.green},   // 51% → 绿
		{10000, ansi.yellow}, // 50% 边界 → 黄 (>50 才是绿)
		{16000, ansi.yellow}, // 20% 边界 → 黄
		{16200, ansi.red},    // 19% → 红
		{20000, ansi.red},    // 0% → 红
	}
	for _, c := range cases {
		got := contextUsageText(newAttentionTestAgent(0, c.dialog))
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("dialog=%d: 期望色 %q 起头, 实际 %q", c.dialog, c.want, got)
		}
	}
}

// ── 4. 降级: 不可判定时返回空串 ─────────────────────────────
func TestContextUsageText_Degrade(t *testing.T) {
	if got := contextUsageText(nil); got != "" {
		t.Errorf("nil agent 应返回空串, 实际 %q", got)
	}
	a := newStatusBarTestAgent() // 无 cfg
	if got := contextUsageText(a); got != "" {
		t.Errorf("无 cfg 应返回空串, 实际 %q", got)
	}
	for _, lim := range []int{0, -1} {
		b := newAttentionTestAgent(0, 5000)
		b.cfg = &Config{CompactTokenThreshold: lim}
		if got := contextUsageText(b); got != "" {
			t.Errorf("阈值=%d 应返回空串, 实际 %q", lim, got)
		}
	}
}

// ── 5. 段存在: 注意力段必须出现在组装结果里 ─────────────────
func TestStatusBarText_AttentionPresent(t *testing.T) {
	visible := stripANSI(statusBarText(newAttentionTestAgent(0, 5000), 135))
	if !strings.Contains(visible, "注意力 剩75%") {
		t.Errorf("135 列下注意力段缺失: %q", visible)
	}
	if !strings.Contains(visible, "缓存命中") {
		t.Errorf("缓存段不应被挤掉: %q", visible)
	}
}

// ── 6. 宽度契约: 带注意力段时仍必须「恰好铺满 w」────────────
func TestStatusBarText_AttentionFits(t *testing.T) {
	a := newAttentionTestAgent(0, 5000)
	for _, w := range []int{60, 80, 100, 120, 135, 200} {
		got := displayWidth(statusBarText(a, w))
		if got > w {
			t.Errorf("w=%d: 宽度 %d 溢出", w, got)
		}
		if got < w-1 {
			t.Errorf("w=%d: 宽度 %d 未铺满 (底板会断开)", w, got)
		}
	}
}

// ── 7. 优先级: 窄终端牺牲末尾会话统计, 保住预警段 ───────────
func TestStatusBarText_AttentionPriority(t *testing.T) {
	a := newAttentionTestAgent(0, 17000) // 剩15%, 红
	for _, w := range []int{60, 80, 100} {
		visible := stripANSI(statusBarText(a, w))
		if !strings.Contains(visible, "注意力 剩15%") {
			t.Errorf("w=%d: 注意力预警段被挤掉 (优先级失效): %q", w, visible)
		}
		if !strings.Contains(visible, "缓存命中") {
			t.Errorf("w=%d: 缓存段被挤掉: %q", w, visible)
		}
	}
	// 极窄终端 (40 列): 仍须保住注意力段 (预警优先于统计)
	visible := stripANSI(statusBarText(a, 40))
	if !strings.Contains(visible, "注意力") {
		t.Errorf("40 列下注意力段被挤掉: %q", visible)
	}
}

// ── 8. 向后兼容: 无 cfg 时输出与旧版一致 ────────────────────
// (status_bar_layout_test.go 的 100 列不截断判据依赖此)
func TestStatusBarText_NoAttentionWhenUnavailable(t *testing.T) {
	visible := stripANSI(statusBarText(newStatusBarTestAgent(), 100))
	if strings.Contains(visible, "注意力") {
		t.Errorf("无 cfg 时不应出现注意力段: %q", visible)
	}
	if strings.Contains(visible, "…") {
		t.Errorf("无 cfg 时 100 列不应截断: %q", visible)
	}
}

// ── 9. 同源(数值): 显示归零的临界 == 压缩触发的临界 ─────────
//
// 判据: contextBudgetUsed 就是状态栏的分子, 也是 maybeCompact 的触发量 ——
// 临界点两侧 (阈值-1 → 剩1%; 阈值 → 剩0%) 必须严格对应。
func TestContextBudgetUsed_MatchesTriggerBoundary(t *testing.T) {
	head := 18749 // 真实固定头量级, 证明固定头不参与临界
	a := newAttentionTestAgent(head, attnTestLimit-1)
	if got := contextBudgetUsed(a); got != attnTestLimit-1 {
		t.Fatalf("contextBudgetUsed=%d, want %d (固定头必须不计入)", got, attnTestLimit-1)
	}
	if got := stripANSI(contextUsageText(a)); !strings.Contains(got, "剩1%") {
		t.Errorf("对话=阈值-1 应显示剩1%%, 实际 %q", got)
	}
	b := newAttentionTestAgent(head, attnTestLimit)
	if got := contextBudgetUsed(b); got != attnTestLimit {
		t.Fatalf("contextBudgetUsed=%d, want %d", got, attnTestLimit)
	}
	if got := stripANSI(contextUsageText(b)); !strings.Contains(got, "剩0%") {
		t.Errorf("对话=阈值 应显示剩0%%, 实际 %q", got)
	}
}

// ── 10. 同源(接线): maybeCompact 必须调用 contextBudgetUsed ──
//
// 接线只能由死程序判定 (公理七): 注释/文档里写"同源"不算证据。
// AST 扫描天然剥离注释与字符串, 故注释里提及旧函数名不会造成假阳性。
// 变异验证: 把触发改回 est := estimateTokens(a.history) 并删掉 contextBudgetUsed
// 调用 → 本用例转红。
func TestCompactTriggerUsesContextBudget(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "agent_trim.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 agent_trim.go 失败: %v", err)
	}
	foundFn, foundCall := false, false
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fd.Name.Name == "contextBudgetUsed" {
			foundFn = true
		}
		if fd.Name.Name != "maybeCompact" || fd.Body == nil {
			return true
		}
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			if ce, ok := m.(*ast.CallExpr); ok {
				if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "contextBudgetUsed" {
					foundCall = true
				}
			}
			return true
		})
		return true
	})
	if !foundFn {
		t.Errorf("agent_trim.go 未定义 contextBudgetUsed (状态栏与触发的共同口径函数)")
	}
	if !foundCall {
		t.Errorf("maybeCompact 未调用 contextBudgetUsed —— 显示口径与触发口径已分叉 (显示归零 ≠ 触发压缩)")
	}
}

// ── 11. 越界防御: headLen 异常时不得 panic ──────────────────
func TestContextBudgetUsed_HeadLenOutOfRange(t *testing.T) {
	a := newAttentionTestAgent(10, 100)
	a.headLen = 999 // 异常形态 (历史比固定头还短)
	if got := contextBudgetUsed(a); got != 0 {
		t.Errorf("headLen 越界应 clamp 到 len(history) 得 0, 实际 %d", got)
	}
	a.headLen = 0 // 未初始化 → 兜底 1 (至少保护 system)
	if got := contextBudgetUsed(a); got != 100 {
		t.Errorf("headLen=0 应兜底为 1 (只算对话), 实际 %d", got)
	}
	a.history = nil
	if got := contextBudgetUsed(a); got != 0 {
		t.Errorf("空历史应得 0, 实际 %d", got)
	}
	if got := contextBudgetUsed(nil); got != 0 {
		t.Errorf("nil agent 应得 0, 实际 %d", got)
	}
}

// ── 12. 诊断输出: 打印各宽度下的真实渲染 (-v 可见) ───────────
func TestStatusBarText_AttentionDiagnostic(t *testing.T) {
	for _, d := range []int{0, 5000, 17000, 20000} {
		a := newAttentionTestAgent(18749, d)
		for _, w := range []int{40, 60, 100, 135} {
			t.Logf("dialog=%d w=%d: %q", d, w, stripANSI(statusBarText(a, w)))
		}
	}
}
