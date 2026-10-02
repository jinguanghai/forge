package main

import (
	"fmt"
	"testing"
)

// ══════════════════════════════════════════════════════════════════
// B3 批4: 无进展检测 (Detector B) + trim 阈值 —— 单元测试
//
// 这里直接构造 progressTracker 而非走 RunStream 全链路: 检测逻辑是纯状态机
// (输入=输出哈希, 输出=干预文本/收尾判定), 单元测试能把每条分支单独钉住;
// 全链路的集成覆盖另见 cov_b3_branches_test.go 的 TestB3_NoProgressInterventionInjected。
//
// 重点钉住的是**指针契约**: loopStrikes 经指针回写, 若有人把它改成值字段,
// 拦截计数会静默丢失(任务永不收尾) —— 下面断言外层变量的实际值, 正是为此。
// ══════════════════════════════════════════════════════════════════

// progFixture 构造检测器与其外部共享状态, 返回三者的可见句柄。
type progFixture struct {
	prog      *progressTracker
	lastOut   string
	strikes   int
	escalated int
}

func newProgFixture(maxStrikes int) *progFixture {
	f := &progFixture{}
	f.prog = &progressTracker{
		maxLoopStrikes: maxStrikes,
		escalateOnLoop: func() { f.escalated++ },
		loopStrikes:    &f.strikes,
		lastRawOutput:  &f.lastOut,
	}
	return f
}

// feed 模拟 toolLoop 写入本轮原始输出后触发一次检测。
func (f *progFixture) feed(out string) (string, bool) {
	f.lastOut = out
	return f.prog.check()
}

// TestProgress_EmptyOutputNoOp: 本回合没有工具执行(lastRawOutput 仍为空)时
// 不得参与连续计数 —— 否则"模型纯文本回复"也会被算成一轮无进展。
func TestProgress_EmptyOutputNoOp(t *testing.T) {
	f := newProgFixture(2)
	for i := 0; i < 10; i++ {
		intervene, stuck := f.feed("")
		if intervene != "" || stuck {
			t.Fatalf("空输出第 %d 次不应触发检测: intervene=%q stuck=%v", i, intervene, stuck)
		}
	}
	if f.strikes != 0 {
		t.Errorf("空输出不应累计拦截, 实际 loopStrikes=%d", f.strikes)
	}
	if f.prog.sameOutRun != 0 {
		t.Errorf("空输出不应累计 sameOutRun, 实际 %d", f.prog.sameOutRun)
	}
}

// TestProgress_ChangeResetsRun: 输出变化 → 连续计数重置为 1, 不得触发拦截。
func TestProgress_ChangeResetsRun(t *testing.T) {
	f := newProgFixture(2)
	for i, out := range []string{"A", "B", "C", "D", "E"} {
		intervene, stuck := f.feed(out)
		if intervene != "" || stuck {
			t.Fatalf("输出变化第 %d 轮不应触发: intervene=%q stuck=%v", i, intervene, stuck)
		}
		if f.prog.sameOutRun != 1 {
			t.Fatalf("输出变化后 sameOutRun 应为 1, 实际 %d", f.prog.sameOutRun)
		}
	}
	if f.strikes != 0 {
		t.Errorf("未达阈值不应拦截, 实际 loopStrikes=%d", f.strikes)
	}
}

// TestProgress_TriggersAtThreshold: 连续 sameOutputThreshold 次相同输出 →
// 第 sameOutputThreshold 次拦截: 注入干预 + 升级模型 + 计数经指针回写。
func TestProgress_TriggersAtThreshold(t *testing.T) {
	f := newProgFixture(5)
	for i := 1; i < sameOutputThreshold; i++ {
		intervene, stuck := f.feed("SAME")
		if intervene != "" || stuck {
			t.Fatalf("第 %d 次(未达阈值 %d)不应触发: intervene=%q stuck=%v",
				i, sameOutputThreshold, intervene, stuck)
		}
	}
	intervene, stuck := f.feed("SAME")
	if stuck {
		t.Fatal("未达拦截上限不应收尾")
	}
	if intervene == "" {
		t.Fatal("达阈值应返回干预文本")
	}
	if f.strikes != 1 {
		t.Errorf("loopStrikes 应经指针回写为 1, 实际 %d", f.strikes)
	}
	if f.escalated != 1 {
		t.Errorf("拦截应调用 escalateOnLoop 一次, 实际 %d", f.escalated)
	}
	if f.prog.sameOutRun != 0 {
		t.Errorf("拦截后 sameOutRun 应归零重新累计, 实际 %d", f.prog.sameOutRun)
	}
}

// TestProgress_StuckAtLimit: 拦截次数达上限 → 返回 stuck=true 且不再注入干预
// (stuck 与 intervene 互斥, 否则会先注入再收尾, 多耗一轮 API)。
func TestProgress_StuckAtLimit(t *testing.T) {
	f := newProgFixture(1)
	var intervene string
	var stuck bool
	for i := 0; i < sameOutputThreshold; i++ {
		intervene, stuck = f.feed("LOOP")
	}
	if !stuck {
		t.Fatal("达拦截上限应返回 stuck=true")
	}
	if intervene != "" {
		t.Errorf("收尾时不应同时注入干预文本, 实际 %q", intervene)
	}
	if f.strikes != 1 {
		t.Errorf("loopStrikes 应为 1, 实际 %d", f.strikes)
	}
}

// TestProgress_InterventionTextCarriesStrikes: 干预文本必须带拦截序号,
// 模型据此知道"这是第几次被拦"(stuckInterventionMsg 的既有契约)。
func TestProgress_InterventionTextCarriesStrikes(t *testing.T) {
	f := newProgFixture(3)
	var intervene string
	for i := 0; i < sameOutputThreshold; i++ {
		intervene, _ = f.feed("SAME")
	}
	if intervene == "" {
		t.Fatal("达阈值应返回干预文本")
	}
	if want := stuckInterventionMsg(1); intervene != want {
		t.Errorf("干预文本应等于 stuckInterventionMsg(1)\n got: %q\nwant: %q", intervene, want)
	}
}

// TestTrimTurnMessagesIfNeeded_UnderLimit: 未超阈值时原样返回(同一底层数组),
// 不得因"顺手复制"而白白丢失前缀缓存。
func TestTrimTurnMessagesIfNeeded_UnderLimit(t *testing.T) {
	msgs := make([]ChatMessage, maxTurnMessages)
	for i := range msgs {
		msgs[i] = ChatMessage{Role: "user", Content: "x"}
	}
	got := trimTurnMessagesIfNeeded(msgs)
	if len(got) != maxTurnMessages {
		t.Fatalf("恰好等于阈值不应裁剪, 实际 %d", len(got))
	}
	if &got[0] != &msgs[0] {
		t.Error("未超阈值应原样返回(同一底层数组), 而非复制")
	}
}

// TestTrimTurnMessagesIfNeeded_OverLimit: 超阈值时委托 trimTurnMessages 裁剪。
//
// 构造真实形态: 一条 tool 轮 = assistant(tool_calls) + tool 响应 两条。
// trimTurnMessages 只删这种完整轮(保 assistant↔tool 配对), 故纯 user 消息
// 即便超限也不会被删 —— 初版测试正是踩了这个坑, 现按真实形态构造。
func TestTrimTurnMessagesIfNeeded_OverLimit(t *testing.T) {
	msgs := []ChatMessage{{Role: "system", Content: "固定头"}}
	for i := 0; len(msgs) <= maxTurnMessages+10; i++ {
		id := fmt.Sprintf("call_%d", i)
		msgs = append(msgs,
			ChatMessage{Role: "assistant", ToolCalls: []ToolCall{{ID: id}}},
			ChatMessage{Role: "tool", ToolCallID: id, Content: "结果"},
		)
	}
	over := len(msgs)
	got := trimTurnMessagesIfNeeded(msgs)
	if len(got) >= over {
		t.Fatalf("超阈值应裁剪, 原 %d 条 → 实际 %d 条", over, len(got))
	}
	if len(got) > maxTurnMessages {
		t.Errorf("裁剪后不应超过阈值 %d, 实际 %d", maxTurnMessages, len(got))
	}
	if got[0].Content != "固定头" {
		t.Errorf("messages[0](固定头)必须保留, 实际 %q", got[0].Content)
	}
}
