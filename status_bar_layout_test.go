package main

import (
	"strings"
	"testing"
	"time"
)

// status_bar_layout_test.go —— 状态栏「组装层」哨兵。
//
// 动机 (2026-10-01 截屏实测): 状态栏底部被截成 "… | 模型:deeps"。
// 根因不在截断函数, 而在组装层把「模型」写了两份 (midSeg 一份 + stats 末尾一份),
// 合计 139 列 > 终端 135 列 → 溢出 → 末尾被吃掉, 主人看不出是截断还是真值。
//
// 旧测试只覆盖 truncateDisplay (截断函数本身), 组装层无任何判据 ——
// 于是「重复段」这种结构性缺陷可以静默穿过全部测试。本文件补上该层判据。

// newStatusBarTestAgent 构造与截屏现场同量级的会话统计。
func newStatusBarTestAgent() *AgentRunner {
	return &AgentRunner{stats: &SessionStats{
		StartTime:   time.Now().Add(-23*time.Minute - 36*time.Second),
		Turns:       3,
		TotalTokens: 3139,
		TotalMs:     362183, // 6m2.183s
		ToolOK:      23,
		ToolFail:    7,
		LastModel:   "deepseek-flash",
		CacheHit:    988,
		CacheMiss:   12,
	}}
}

// TestStatusBarText_NoModelSegment 哨兵: 状态栏不得再出现「模型」段。
//
// 判据取「0 次」而非「不重复」: 模型名已在启动横幅与会话元信息里出现,
// 状态栏再放一份纯属挤占宽度 —— 旧版两份 (组装层 midSeg + 统计段末尾) 合计把
// 真实会话数据撑到 133 列, 超过终端可视宽度后末尾被吃掉 (截屏: "…| 模型:deeps")。
// 变异验证: 把 midSeg 加回组装层 → 本用例必须转红 (已实测)。
func TestStatusBarText_NoModelSegment(t *testing.T) {
	txt := stripANSI(statusBarText(newStatusBarTestAgent(), 135))
	if n := strings.Count(txt, "模型"); n > 0 {
		t.Errorf("状态栏不应出现「模型」段 (出现 %d 次, 挤占宽度): %q", n, txt)
	}
	if n := strings.Count(txt, "deepseek-flash"); n > 0 {
		t.Errorf("模型名不应出现在状态栏 (出现 %d 次): %q", n, txt)
	}
}

// TestStatusBarText_FitsTerminalWidth 哨兵: 组装结果宽度必须落在终端宽度内。
// 不截断时恰好铺满 w (底板连续); 截断时允许差 1 列 (宽字符被切掉一半)。
func TestStatusBarText_FitsTerminalWidth(t *testing.T) {
	a := newStatusBarTestAgent()
	for _, w := range []int{80, 100, 120, 135, 200} {
		got := displayWidth(statusBarText(a, w))
		if got > w {
			t.Errorf("w=%d: 组装宽度 %d 溢出终端", w, got)
		}
		if got < w-1 {
			t.Errorf("w=%d: 组装宽度 %d 未铺满 (补齐逻辑失效, 底板会断开)", w, got)
		}
	}
}

// TestStatusBarText_RealSessionNotTruncated 回归锚: 截屏现场数据在窄终端下也不得触发截断。
//
// 宽度取 100 列 (保守下界, 常见终端 ≥100 列): 修后内容 90 列 → 完整;
// 若有人把模型段加回组装层 (+22 列 → 112 列) → 立刻截断, 本用例转红。
func TestStatusBarText_RealSessionNotTruncated(t *testing.T) {
	visible := stripANSI(statusBarText(newStatusBarTestAgent(), 100))
	if strings.Contains(visible, "…") {
		t.Errorf("真实会话数据在 100 列仍被截断 (P0-1 回归): %q", visible)
	}
	for _, want := range []string{"缓存命中 98.8%", "轮次:3", "令牌:3139", "工具:23", "会话时长:23m36s"} {
		if !strings.Contains(visible, want) {
			t.Errorf("状态栏缺 %q: %q", want, visible)
		}
	}
}
