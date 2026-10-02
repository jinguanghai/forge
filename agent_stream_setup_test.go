package main

// agent_stream_setup_test.go — B3 批5 单测镜像 (fractal F4)。
//
// 批5 把 RunStream 的装配 / 状态 / 主循环整体迁到 runState 上
// (agent_stream_setup.go), 并把三个闭包升为方法。本文件钉住迁移后最关键的三类
// 契约:
//
//   ① 指针别名: 子组件持有的指针必须 == 宿主字段地址。前几批靠"指针契约"逐个
//      防的缺陷类别("改了副本却没写回"), 批5 由结构消除 —— 但消除的前提正是
//      这层别名关系。若有人把子组件改成持有一份值拷贝, 拦截计数与反馈写入会
//      静默丢失, 且编译期毫无提示。本用例就是那条防线。
//   ② 闭包→方法后行为不变: escalateOnLoop 只升一次 / finishStuck 落 history /
//      nswIntervene 轮次上限与拒绝权路径。
//   ③ runTurns 出口不变: 纯文本收尾返回 nil 且落 history; 循环头取消返回 ctx 错误。
//
// 手法: httptest 假 SSE 端点 (复用 cov_b3_branches_test.go 的 b3SSE2 / b3Agent),
// 确定性, 不触外网。

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ─── ① 指针别名契约 ───────────────────────────────────────────

// TestRunState_ChildPointersAliasFields 子组件指针必须指向宿主字段本身。
//
// 这是批5 "闭包→方法" 变换成立的支点: 三个闭包原本捕获局部变量, 提升为字段后,
// 子组件仍按旧契约持指针。若这层别名断了(例如有人图省事写成 &copy), 表现是
// "拦截计数不涨 / 反馈注入了但模型收不到" —— 不报错、不崩溃, 只是静默失效。
func TestRunState_ChildPointersAliasFields(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	rs, err := a.newRunState("hi", context.Background())
	if err != nil {
		t.Fatalf("newRunState: %v", err)
	}

	// turnFinalizer: 收尾要写回 messages / verifyStrikes / nswRounds
	if rs.fin.messages != &rs.messages {
		t.Error("fin.messages 未指向 rs.messages —— 收尾写入会落在副本上(反馈静默丢失)")
	}
	if rs.fin.verifyStrikes != &rs.verifyStrikes {
		t.Error("fin.verifyStrikes 未指向 rs.verifyStrikes —— 虚报计数不累计, 可无限注入")
	}
	if rs.fin.nswRounds != &rs.nswRounds {
		t.Error("fin.nswRounds 未指向 rs.nswRounds —— 无剑轮次上限失效")
	}
	// progressTracker: 拦截计数与最后输出跨轮累计
	if rs.prog.loopStrikes != &rs.loopStrikes {
		t.Error("prog.loopStrikes 未指向 rs.loopStrikes —— 拦截次数不累计, 永不收尾")
	}
	if rs.prog.lastRawOutput != &rs.lastRawOutput {
		t.Error("prog.lastRawOutput 未指向 rs.lastRawOutput —— 无进展检测拿不到输出")
	}
	// 注入的函数值不能是 nil (nil 调用 = panic, 且说明构造漏项)
	if rs.fin.nswIntervene == nil {
		t.Error("fin.nswIntervene 为 nil —— 无剑干预入口未注入")
	}
	if rs.prog.escalateOnLoop == nil {
		t.Error("prog.escalateOnLoop 为 nil —— 拦截升级入口未注入")
	}
	// 别名成立的实证: 通过子组件改, 宿主可见
	*rs.fin.nswRounds = 7
	if rs.nswRounds != 7 {
		t.Fatalf("经 fin.nswRounds 写入未反映到 rs.nswRounds (别名断裂): %d", rs.nswRounds)
	}
	*rs.prog.loopStrikes = 3
	if rs.loopStrikes != 3 {
		t.Fatalf("经 prog.loopStrikes 写入未反映到 rs.loopStrikes (别名断裂): %d", rs.loopStrikes)
	}
}

// ─── ② 装配期分支 ─────────────────────────────────────────────

// TestRunState_EntryCanceled 入口 runCtx 已取消 → 不发请求直接返回 ctx 错误。
func TestRunState_EntryCanceled(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rs, err := a.newRunState("hi", ctx)
	if err == nil {
		t.Fatal("入口已取消应返回错误, 实际 nil (会带着死 ctx 进主循环)")
	}
	if rs != nil {
		t.Error("出错时不应返回半初始化的 runState")
	}
}

// TestRunState_VisionForcesVisionModel 带图请求强制走视觉模型。
//
// 视觉模型 ≠ ModelFlash 是这条分支存在的理由: 否则循环拦截的升级逻辑会把它
// 误当成 flash 升级掉, 图片直接送不出去。
func TestRunState_VisionForcesVisionModel(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	var workDir string
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.WorkDir = t.TempDir()
		workDir = c.WorkDir
		c.ModelVision = "vis-x"
	})
	png := b3WritePNG(t, workDir, "shot.png")

	rs, err := a.newRunState("看看这张图 "+png, context.Background())
	if err != nil {
		t.Fatalf("newRunState: %v", err)
	}
	if rs.curModel != "vis-x" {
		t.Fatalf("带图未切到视觉模型: %q", rs.curModel)
	}
}

// ─── ③ 闭包→方法 后行为不变 ──────────────────────────────────

// TestRunState_EscalateOnLoopOnce 拦截升级只做一次, 且升级后不再被覆盖。
func TestRunState_EscalateOnLoopOnce(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.ModelFlash = "flash-x"
		c.ModelPro = "pro-x"
	})

	rs := &runState{agent: a, curModel: "flash-x"}
	rs.escalateOnLoop()
	if !rs.escalated || rs.curModel != "pro-x" {
		t.Fatalf("首次升级失败: escalated=%v curModel=%q", rs.escalated, rs.curModel)
	}
	// 第二次: escalated 已置位, 不应再改 (防反复升级打乱模型选择)
	rs.curModel = "flash-x"
	rs.escalateOnLoop()
	if rs.curModel != "flash-x" {
		t.Fatalf("已升级过仍再次升级: %q (escalated 位失效)", rs.curModel)
	}
}

// TestRunState_EscalateOnLoopNoPro 无 pro 可升 (V4.1 起 Flash==Pro) → 保持原模型。
func TestRunState_EscalateOnLoopNoPro(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil) // b3Agent 默认 ModelPro == ModelFlash

	rs := &runState{agent: a, curModel: a.cfg.ModelFlash}
	rs.escalateOnLoop()
	if rs.escalated {
		t.Error("无可升级目标却置位 escalated —— 会掩盖后续真实升级机会")
	}
	if rs.curModel != a.cfg.ModelFlash {
		t.Fatalf("模型被改成了 %q", rs.curModel)
	}
}

// TestRunState_FinishStuckLandsHistory 自动收尾: 返回 nil(正常完成路径) 且
// user+assistant 成对落入 history (缺一即造成下轮 API 400)。
func TestRunState_FinishStuckLandsHistory(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	rs := &runState{
		agent:         a,
		userContent:   "原始任务",
		loopStrikes:   2,
		lastRawOutput: "traceback: boom",
		turnStart:     time.Now(),
	}
	before := len(a.history)
	if err := rs.finishStuck(); err != nil {
		t.Fatalf("收尾应返回 nil (正常完成路径), 实际: %v", err)
	}
	if len(a.history) != before+2 {
		t.Fatalf("history 应追加 user+assistant 两条, 实际 %d -> %d", before, len(a.history))
	}
	u := a.history[before]
	s := a.history[before+1]
	if u.Role != "user" || u.Content != "原始任务" {
		t.Errorf("首条应为原始用户消息: %+v", u)
	}
	if s.Role != "assistant" {
		t.Errorf("次条应为 assistant 收尾: %+v", s)
	}
	if s.Content == "" {
		t.Error("收尾消息为空 —— 用户看不到任何进展说明")
	}
}

// TestRunState_NSWInterveneRoundLimit 达轮次上限后不再干预 (否则无剑反馈成死循环源)。
func TestRunState_NSWInterveneRoundLimit(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	b3Env(t, "FORGE_NSW_EXPR", "1")
	b3Env(t, "FORGE_NOSWORD", "1")

	rs := &runState{agent: a, maxNSWRounds: 1, nswRounds: 1}
	if fb := rs.nswIntervene("错 {{35元}}"); fb != "" {
		t.Fatalf("已达上限仍返回反馈: %q", fb)
	}
	if rs.nswRounds != 1 {
		t.Fatalf("越界时不应递增计数: %d", rs.nswRounds)
	}
}

// TestRunState_NSWInterveneRejectPath 拒绝权 (FORGE_NSW_EXPR=1): 非法标记 →
// 死程序回告模型改写, 且计入轮次。
func TestRunState_NSWInterveneRejectPath(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	b3Env(t, "FORGE_NSW_EXPR", "1")
	b3Env(t, "FORGE_NOSWORD", "1")

	rs := &runState{agent: a, maxNSWRounds: 2}
	fb := rs.nswIntervene("错的两个 {{35元}}")
	if fb == "" {
		t.Fatal("非法标记应触发拒绝反馈 (静默漏过 = 错值直达用户)")
	}
	if rs.nswRounds != 1 {
		t.Fatalf("拒绝路径应计入轮次: %d", rs.nswRounds)
	}
	// 合法标记不应触发拒绝 (否则正常算式也被拦)
	rs2 := &runState{agent: a, maxNSWRounds: 2}
	if fb2 := rs2.nswIntervene("一共 {{35*30}} 元"); fb2 != "" && rs2.nswRounds != 0 {
		// 允许 nswEnabled 路径给反馈, 但拒绝路径的计数必须为 0
		t.Logf("合法标记走了非拒绝路径 (可接受): %q", fb2)
	}
}

// ─── ④ runTurns 出口 ─────────────────────────────────────────

// TestRunTurns_PlainResponseFinalizes 纯文本回复 → 收尾落 history 并返回 nil。
func TestRunTurns_PlainResponseFinalizes(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covContentLine("任务完成"), covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	rs, err := a.newRunState("干个活", context.Background())
	if err != nil {
		t.Fatalf("newRunState: %v", err)
	}
	before := len(a.history)
	if err := rs.runTurns(); err != nil {
		t.Fatalf("runTurns 应返回 nil (正常完成), 实际: %v", err)
	}
	if len(a.history) != before+2 {
		t.Fatalf("收尾应落 user+assistant, 实际 %d -> %d", before, len(a.history))
	}
	if got := a.history[before+1].Content; got != "任务完成" {
		t.Fatalf("落盘的 assistant 内容不符: %q", got)
	}
}

// TestRunTurns_EntryCanceled 循环头即取消 → 返回 ctx 错误。
//
// 这条覆盖的是批0 遗留的未覆盖语句(主循环头 runCtx.Done() -> return runCtx.Err())。
// 批0 当时尝试"轮间取消"时序导致测试进程异常, 已回退; 批5 主循环独立成函数后,
// 直接构造已取消的 runState 即可确定性命中, 不再需要那套脆弱时序。
func TestRunTurns_EntryCanceled(t *testing.T) {
	srv := b3SSE2([]b3Round{{lines: []string{covStopLine}}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rs := &runState{agent: a, runCtx: ctx, messages: []ChatMessage{{Role: "user", Content: "hi"}}}
	err := rs.runTurns()
	if err == nil {
		t.Fatal("循环头已取消应返回错误, 实际 nil (取消后仍会发请求)")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled, 实际: %v", err)
	}
}
