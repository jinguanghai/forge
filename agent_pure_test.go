package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAbortUnknownTools(t *testing.T) {
	if abort, _ := abortUnknownTools(2, 5); abort {
		t.Error("2/5 不应中止")
	}
	if abort, msg := abortUnknownTools(5, 5); !abort || !strings.Contains(msg, "5") {
		t.Errorf("5/5 应中止, got abort=%v msg=%q", abort, msg)
	}
	if abort, _ := abortUnknownTools(8, 5); !abort {
		t.Error("8/5 应中止")
	}
	if abort, _ := abortUnknownTools(0, 0); !abort {
		t.Error("0/0 应中止")
	}
}

func TestParseErrMessage(t *testing.T) {
	msg := parseErrMessage(errors.New("bad json"))
	if !strings.Contains(msg, "bad json") || !strings.Contains(msg, "action") {
		t.Errorf("消息应含错误与参数说明: %q", msg)
	}
}

// repeatReminder 三级梯度 + 摘要截断 (dsH repeat-tool-reminder 偷师, 20260816)
func TestRepeatReminderLevels(t *testing.T) {
	// 温和档 (count 4~5): 含次数, 不含摘要/禁止
	mild := repeatReminder(4, "print(1)")
	if !strings.Contains(mild, "4") || strings.Contains(mild, "禁止") || strings.Contains(mild, "摘要") {
		t.Errorf("温和档应含次数且不含摘要/禁止: %q", mild)
	}
	mild5 := repeatReminder(5, "print(1)")
	if !strings.Contains(mild5, "5") {
		t.Errorf("count=5 仍应温和档: %q", mild5)
	}
	// 详细档 (count 6~8): 含次数+摘要
	detail := repeatReminder(6, "print(42) # comment")
	if !strings.Contains(detail, "6") || !strings.Contains(detail, "print(42)") {
		t.Errorf("详细档应含次数+归一化摘要: %q", detail)
	}
	if !strings.Contains(detail, "换") {
		t.Errorf("详细档应要求换方案: %q", detail)
	}
	detail8 := repeatReminder(8, "x = 1")
	if !strings.Contains(detail8, "8") || !strings.Contains(detail8, "x=1") {
		t.Errorf("count=8 仍应详细档: %q", detail8)
	}
	// 强提醒档 (count>8): 含禁止指令
	harsh := repeatReminder(9, "print(1)")
	if !strings.Contains(harsh, "禁止") || !strings.Contains(harsh, "9") {
		t.Errorf("强提醒应含禁止指令+次数: %q", harsh)
	}
	// 边界: 8->9 档位切换
	if !strings.Contains(repeatReminder(8, "a"), "⚠️") || !strings.Contains(repeatReminder(9, "a"), "🚫") {
		t.Error("8→9 应切换档位符号")
	}
}

func TestRepeatReminderSummaryTruncation(t *testing.T) {
	// 长代码摘要截 80 rune + 省略号 (走详细/强提醒档才带摘要)
	long := strings.Repeat("f(", 100) + strings.Repeat(")", 100)
	msg := repeatReminder(6, long)
	if !strings.Contains(msg, "...") {
		t.Errorf("超长摘要应截断补省略号")
	}
	// 短代码摘要原样
	short := repeatReminder(6, "ok()")
	if !strings.Contains(short, "ok()") {
		t.Errorf("短摘要应原样包含: %q", short)
	}
	// 语义等价归一化: 换注释/空白后摘要一致, 揭示同一性
	a := repeatReminder(7, "print(1) # one")
	b := repeatReminder(7, "print(1) // two")
	ia := strings.Index(a, "相同代码")
	ib := strings.Index(b, "相同代码")
	if a[ia:] != b[ib:] {
		t.Errorf("语义等价代码摘要应一致:\n%s\nvs\n%s", a[ia:], b[ib:])
	}
}

func TestTruncateDetail(t *testing.T) {
	if got := truncateDetail("short", 10); got != "short" {
		t.Errorf("短串不应截断: %q", got)
	}
	if got := truncateDetail("123456789012", 10); got != "1234567890..." {
		t.Errorf("长串截断错误: %q", got)
	}
	if got := truncateDetail("1234567890", 10); got != "1234567890" {
		t.Errorf("边界不应截断: %q", got)
	}
}

func TestConsecutiveFailMessage(t *testing.T) {
	msg := consecutiveFailMessage(4)
	if !strings.Contains(msg, "4") {
		t.Errorf("应含失败次数: %q", msg)
	}
}

func TestGoalAnchor(t *testing.T) {
	got := goalAnchor("out", "任务X", 5)
	if !strings.Contains(got, "out") || !strings.Contains(got, "任务X") || !strings.Contains(got, "第6轮") {
		t.Errorf("goalAnchor 输出不完整: %q", got)
	}
}

// ─── 循环拦截: 归一化 / 消息 / 瞬态判定 (20260816) ───

func TestNormalizeCode(t *testing.T) {
	cases := []struct{ in, want string }{
		{"print(1)", "print(1)"},
		{"  print(1)  ", "print(1)"},
		{"print(1) # comment", "print(1)"},
		{"// comment\nprint(1)", "print(1)"},
		{"a = 1\nb = 2", "a=1b=2"},
		{"\n\n  \n\t", ""},
		{"def f():\n    return 1  # ok", "deff():return1"},
	}
	for _, c := range cases {
		if got := normalizeCode(c.in); got != c.want {
			t.Errorf("normalizeCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 语义等价: 换注释/换行/缩进的同一代码应归一化到同一串 → 同一哈希
func TestNormalizeCodeSemanticEquivalence(t *testing.T) {
	a := normalizeCode("print(1) # first\nprint(2)")
	b := normalizeCode("print(1)\n\n print(2) // second")
	if a != b {
		t.Errorf("语义等价代码应归一化一致: %q vs %q", a, b)
	}
}

func TestEffectiveMaxStrikes(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 4}, {4, 4}, {-3, 4}, {1, 1}, {10, 10},
	}
	for _, c := range cases {
		if got := effectiveMaxStrikes(c.in); got != c.want {
			t.Errorf("effectiveMaxStrikes(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestStuckMessages(t *testing.T) {
	interv := stuckInterventionMsg(1)
	if !strings.Contains(interv, "1") || !strings.Contains(interv, "换") {
		t.Errorf("干预消息应含次数与换策略要求: %q", interv)
	}
	exit := stuckExitMessage(3, strings.Repeat("x", 1000))
	if !strings.Contains(exit, "3") || !strings.Contains(exit, "收尾") || !strings.Contains(exit, "进展") {
		t.Errorf("收尾消息应含次数/收尾/进展: %q", exit)
	}
	// 超长进展截断
	if len(exit) > 1000 {
		t.Errorf("收尾消息过长: %d", len(exit))
	}
	// 空进展不 panic
	if s := stuckExitMessage(2, ""); !strings.Contains(s, "2") {
		t.Errorf("空进展收尾异常: %q", s)
	}
}

func TestClassifyTransient(t *testing.T) {
	// 非零退出不是瞬态(旧漏洞: exit status 被豁免 → 反复失败不中止)
	r := &ForgeGateResult{OK: false, Stage: "execute", Error: "python execution failed: exit status 1"}
	if classifyTransient(r) {
		t.Error("exit status 不应判为瞬态")
	}
	// 超时/工具缺失是环境瞬态
	if !classifyTransient(&ForgeGateResult{OK: false, Stage: "execute", Error: "execution timeout after 15s"}) {
		t.Error("timeout 应判为瞬态")
	}
	if !classifyTransient(&ForgeGateResult{OK: false, Stage: "execute", Error: "exec: python not found"}) {
		t.Error("tool not found 应判为瞬态")
	}
	// 编译错误是真实代码 bug, 非瞬态
	if classifyTransient(&ForgeGateResult{OK: false, Stage: "compile", Error: "undefined: foo"}) {
		t.Error("编译错误不应判为瞬态")
	}
	// nil 安全
	if classifyTransient(nil) {
		t.Error("nil 不应判为瞬态")
	}
}

// ─── 六西格玛 I2: 工具结果修剪 (20260817) ───
func TestPruneToolOutput(t *testing.T) {
	// 短输出原样
	if got := pruneToolOutput("short", 100, 50, 10); got != "short" {
		t.Errorf("短输出应原样: %q", got)
	}
	// 超阈值修剪: 头+尾保留, 中间省略标记
	long := strings.Repeat("x", 10000)
	got := pruneToolOutput(long, 8192, 4096, 1024)
	if !strings.Contains(got, "⏴ PRUNED ⏵") {
		t.Fatalf("超阈值应含省略标记")
	}
	if !strings.HasPrefix(got, strings.Repeat("x", 4096)) || !strings.HasSuffix(got, strings.Repeat("x", 1024)) {
		t.Fatalf("头尾应保留: len=%d", len(got))
	}
	// 头尾之和覆盖全文 → 原样 (兜底)
	mid := strings.Repeat("y", 5000)
	if got := pruneToolOutput(mid, 8192, 4096, 1024); got != mid {
		t.Fatalf("头尾覆盖时应原样")
	}
	// Unicode: 按 rune 切不劈开字符
	uni := strings.Repeat("中", 9000)
	gotU := pruneToolOutput(uni, 8192, 4096, 1024)
	if !strings.HasPrefix(gotU, strings.Repeat("中", 4096)) || !strings.HasSuffix(gotU, strings.Repeat("中", 1024)) {
		t.Fatalf("rune 边界应正确: len=%d", len(gotU))
	}
	// 阈值边界: 恰好等于阈值 → 不修剪
	exact := strings.Repeat("z", 8192)
	if got := pruneToolOutput(exact, 8192, 4096, 1024); got != exact {
		t.Fatalf("恰好阈值不应修剪")
	}
	// goalAnchor 集成: 超长输出被修剪, 锚点仍在
	big := strings.Repeat("a", 9000)
	ga := goalAnchor(big, "任务", 1)
	if strings.Contains(ga, strings.Repeat("a", 9000)) {
		t.Fatalf("goalAnchor 应修剪超长输出")
	}
	if !strings.Contains(ga, "任务") || !strings.Contains(ga, "第2轮") || !strings.Contains(ga, "⏴ PRUNED ⏵") {
		t.Fatalf("goalAnchor 修剪后锚点应保留")
	}
}

// ─── 六西格玛 I1: 配对安全选段 (20260817) ───
func TestCompletePairs(t *testing.T) {
	u := func() ChatMessage { return ChatMessage{Role: "user", Content: "u"} }
	a := func() ChatMessage { return ChatMessage{Role: "assistant", Content: "a"} }
	at := func(ids ...string) ChatMessage {
		tcs := make([]ToolCall, 0, len(ids))
		for _, id := range ids {
			tcs = append(tcs, ToolCall{ID: id, Function: FunctionCall{Name: "f", Arguments: "{}"}})
		}
		return ChatMessage{Role: "assistant", Content: "a", ToolCalls: tcs}
	}
	tl := func(id string) ChatMessage { return ChatMessage{Role: "tool", ToolCallID: id, Content: "r"} }

	// ① 无工具轮次: 区间不变
	msgs := []ChatMessage{u(), a(), u()}
	if s, e := completePairs(msgs, 1, 2); s != 1 || e != 2 {
		t.Fatalf("无工具轮次不应扩展: %d,%d", s, e)
	}
	// ② end 切在 assistant(tool_calls) 后 → 收齐响应到 user 前
	msgs2 := []ChatMessage{u(), at("c1", "c2"), tl("c1"), tl("c2"), u()}
	if s, e := completePairs(msgs2, 1, 2); s != 1 || e != 4 {
		t.Fatalf("应扩展收齐响应: %d,%d (want 1,4)", s, e)
	}
	// ③ start 处是 tool → 前移纳入其 assistant
	msgs3 := []ChatMessage{u(), at("c1"), tl("c1"), u()}
	if s, e := completePairs(msgs3, 2, 3); s != 1 || e != 3 {
		t.Fatalf("start 前移应纳入 assistant: %d,%d (want 1,3)", s, e)
	}
	// ④ end 停在 tool 序列中间 → 收进 (防御)
	msgs4 := []ChatMessage{u(), at("c1", "c2"), tl("c1"), tl("c2"), u()}
	if s, e := completePairs(msgs4, 1, 3); s != 1 || e != 4 {
		t.Fatalf("end 在 tool 中间应收齐: %d,%d (want 1,4)", s, e)
	}
	// ⑤ 保护 system: start<1 强制为 1
	msgs5 := []ChatMessage{{Role: "system", Content: "S"}, u(), a()}
	if s, e := completePairs(msgs5, 0, 3); s != 1 || e != 3 {
		t.Fatalf("应保护 system: %d,%d (want 1,3)", s, e)
	}
	// ⑥ 响应在段外 (end 切在 assistant 后, tool 响应在段外) → end 扩展到收齐
	msgs6 := []ChatMessage{u(), at("c1"), tl("c1"), u()}
	if s, e := completePairs(msgs6, 1, 2); s != 1 || e != 3 {
		t.Fatalf("段外响应应扩展: %d,%d (want 1,3)", s, e)
	}
	// ⑦ 孤立 tool 在段尾 (无对应 assistant, 防御场景) → ③ 收进, 避免 restMsgs 头部孤立 tool
	msgs7 := []ChatMessage{u(), at("c1"), tl("c1"), tl("x"), u()}
	if s, e := completePairs(msgs7, 1, 3); s != 1 || e != 4 {
		t.Fatalf("孤立 tool 应收进: %d,%d (want 1,4)", s, e)
	}
	// ⑧ 空/边界: start>=end 直接返回
	if s, e := completePairs(msgs, 2, 2); s != 2 || e != 2 {
		t.Fatalf("start>=end 应原样: %d,%d", s, e)
	}
}

// TestGoalAnchorRef 六西格玛 P0① 落盘引用:
// 小输出走原 goalAnchor 内联逻辑 (不改变模型可见性);
// 大输出 (>12000 rune) 触发全文落盘 + 内联头/尾摘要 + 文件指针。
func TestGoalAnchorRef(t *testing.T) {
	// 小输出: 保持 goalAnchor 原逻辑, 内联全文 + 任务锚点 + 轮次
	small := goalAnchorRef("out", "任务X", 5, t.TempDir())
	if !strings.Contains(small, "out") || !strings.Contains(small, "任务X") || !strings.Contains(small, "第6轮") {
		t.Errorf("小输出应走原 goalAnchor 逻辑, 实际: %q", small)
	}

	// 大输出 (>12000 rune): 触发落盘引用
	big := strings.Repeat("x", 15000) // 15000 ascii rune > 12000 阈值
	ref := goalAnchorRef(big, "任务Y", 3, t.TempDir())
	if !strings.Contains(ref, "落盘保存到") || !strings.Contains(ref, "省略") || !strings.Contains(ref, "任务Y") {
		t.Errorf("大输出应包含落盘指针+摘要+任务锚点, 实际: %q", ref[:200])
	}
	if !strings.Contains(ref, "toolcache") {
		t.Errorf("落盘引用应含 toolcache 路径, 实际: %q", ref[:150])
	}
}

// TestStoreToolOutputRef: 落盘文件真实存在且内容等于原文。
func TestStoreToolOutputRef(t *testing.T) {
	wd := t.TempDir()
	out := strings.Repeat("hello", 300)
	p := storeToolOutputRef(out, 1, wd)
	if p == "" {
		t.Fatal("落盘返回空路径")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取落盘文件失败: %v", err)
	}
	if string(b) != out {
		t.Errorf("落盘内容与原文不一致: got %d, want %d", len(b), len(out))
	}
}
