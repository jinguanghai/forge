package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// ── 测试夹具: 假 DeepSeek API ────────────────────────────────
type fakeSummaryServer struct {
	mu      sync.Mutex
	summary string
	fail    bool // 返回 500
	calls   int
}

func (f *fakeSummaryServer) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		fail := f.fail
		sum := f.summary
		f.mu.Unlock()
		if fail {
			w.WriteHeader(500)
			w.Write([]byte("{\"error\":\"boom\"}"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": sum}},
			},
		})
	}
}

func newCompactRunner(t *testing.T, srv *httptest.Server, cfg *Config) *AgentRunner {
	t.Helper()
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = srv.URL
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "test-key"
	}
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	if cfg.ModelFlash == "" {
		cfg.ModelFlash = "test-flash"
	}
	if cfg.CompactTokenThreshold == 0 {
		cfg.CompactTokenThreshold = 500
	}
	if cfg.CompactMinTurns == 0 {
		cfg.CompactMinTurns = 3
	}
	// CompactEnabled 不在此强制, 由各测试显式决定 (修复: 无条件覆盖吞掉 Disabled 测试的 false)
	llm := NewLLMClient(cfg)
	llm.client = srv.Client()
	ctx, cancel := context.WithCancel(context.Background())
	return &AgentRunner{
		cfg:    cfg,
		llm:    llm,
		ctx:    ctx,
		cancel: cancel,
	}
}

func longMsg(n int) string {
	return strings.Repeat("这是一条用于撑大token估算的很长的对话内容。", n/20+1)[:n]
}

func buildHistory(system string, n int) []ChatMessage {
	h := []ChatMessage{{Role: "system", Content: system}}
	for i := 0; i < n; i++ {
		h = append(h,
			ChatMessage{Role: "user", Content: longMsg(200) + "问题" + string(rune('0'+i%10))},
			ChatMessage{Role: "assistant", Content: longMsg(300) + "回答" + string(rune('0'+i%10))},
		)
	}
	return h
}

// 1. 触发: 超阈值 → 摘要注入头部, 历史变短, system 保持头部, 冷却设置
func TestMaybeCompactTriggers(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "会话摘要: 任务A已完成, 任务B待办。"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYSTEM-RULES", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) >= before {
		t.Fatalf("压缩后历史未变短: before=%d after=%d", before, len(a.history))
	}
	if !strings.HasPrefix(a.history[1].Content, "【会话摘要】") {
		t.Fatalf("history[1] 不是摘要: %q", a.history[1].Content)
	}
	if a.history[0].Role != "system" {
		t.Fatalf("system 应保持头部, got %s", a.history[0].Role)
	}
	if a.compactCooldown != 3 {
		t.Fatalf("冷却未设置: got %d want 3", a.compactCooldown)
	}
}

// 2. 冷却: 压缩后 MIN_TURNS 轮内不再压缩, 冷却结束可再压
func TestMaybeCompactCooldown(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要X"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYS", 15)
	a.maybeCompact()
	after1 := len(a.history)
	for i := 0; i < 3; i++ {
		a.maybeCompact()
		if len(a.history) != after1 {
			t.Fatalf("冷却期第%d次调用历史不应变化", i+1)
		}
	}
	a.maybeCompact() // 冷却结束 (3次递减到0), 应能再次压缩
	if len(a.history) >= after1 {
		t.Fatalf("冷却结束后应能再次压缩")
	}
}

// 3. 开关关闭 → 不压缩
func TestMaybeCompactDisabled(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要X"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: false})
	a.history = buildHistory("SYS", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) != before {
		t.Fatalf("开关关闭时不应压缩")
	}
}

// 4. 低于阈值 → 不压缩
func TestMaybeCompactBelowThreshold(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要X"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true, CompactTokenThreshold: 1 << 20})
	a.history = buildHistory("SYS", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) != before {
		t.Fatalf("低于阈值不应压缩")
	}
}

// 5. 摘要失败 → 降级不阻塞, 历史不变
func TestMaybeCompactFailFallback(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "", fail: true}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYS", 15)
	before := len(a.history)
	a.maybeCompact()
	if len(a.history) != before {
		t.Fatalf("摘要失败应降级, 历史不变")
	}
}

// 6. Summarize 非流式: 正常返回摘要
func TestSummarizeNonStream(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "这是摘要正文"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, nil)
	got, err := a.llm.Summarize(context.Background(), []ChatMessage{{Role: "user", Content: "你好"}}, 400)
	if err != nil {
		t.Fatalf("Summarize err: %v", err)
	}
	if got != "这是摘要正文" {
		t.Fatalf("got %q want 摘要正文", got)
	}
}

// 7. 摘要调用走 Flash 模型 + Stream=false + thinking disabled
func TestSummarizeUsesFlashNonStream(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"摘要\"}}]}"))
	}))
	defer srv.Close()
	a := newCompactRunner(t, srv, nil)
	if _, err := a.llm.Summarize(context.Background(), []ChatMessage{{Role: "user", Content: "x"}}, 400); err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotBody["model"] != "test-flash" {
		t.Fatalf("摘要应走 Flash 模型, got %v", gotBody["model"])
	}
	if gotBody["stream"] != false {
		t.Fatalf("摘要应为非流式, got %v", gotBody["stream"])
	}
	if th, ok := gotBody["thinking"].(map[string]interface{}); !ok || th["type"] != "disabled" {
		t.Fatalf("摘要应关 thinking, got %v", gotBody["thinking"])
	}
}

// 8. trimHistory 挂钩: 超阈值+可压缩时, trimHistory 后历史含摘要 (端到端)
func TestTrimHistoryInvokesCompact(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "端到端摘要"}).handler())
	defer srv.Close()
	a := newCompactRunner(t, srv, &Config{CompactEnabled: true})
	a.history = buildHistory("SYS", 20)
	a.trimHistory()
	found := false
	for _, m := range a.history {
		if strings.HasPrefix(m.Content, "【会话摘要】") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("trimHistory 后应含摘要消息")
	}
}

// 9. 六西格玛 I1 证据: 摘要请求前缀 = 真实历史前缀 (缓存命中前置), 指令追加为最后一条
func TestSummarizePreservesPrefix(t *testing.T) {
	var gotMsgs []ChatMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []ChatMessage `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotMsgs = body.Messages
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"摘要"}}]}`))
	}))
	defer srv.Close()
	a := newCompactRunner(t, srv, nil)
	// 含 system + tool_calls 配对的真实历史段
	hist := []ChatMessage{
		{Role: "system", Content: "SYSTEM-RULES"},
		{Role: "user", Content: "任务1"},
		{Role: "assistant", Content: "思考", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "forge", Arguments: `{"code":"print(1)"}`}}}},
		{Role: "tool", ToolCallID: "c1", Content: "输出1"},
		{Role: "user", Content: "任务2"},
		{Role: "assistant", Content: "回答2"},
	}
	if _, err := a.llm.Summarize(context.Background(), hist, 400); err != nil {
		t.Fatalf("Summarize err: %v", err)
	}
	if len(gotMsgs) != len(hist)+1 {
		t.Fatalf("摘要请求消息数 = %d, want %d (原样 + 1条指令)", len(gotMsgs), len(hist)+1)
	}
	// 前缀逐字节一致: 摘要请求前 len(hist) 条与传入历史 JSON 序列化完全一致 → KV 前缀缓存命中
	wantJSON, _ := json.Marshal(hist)
	gotJSON, _ := json.Marshal(gotMsgs[:len(hist)])
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("前缀不一致 (缓存不会命中)!\nwant=%s\ngot =%s", wantJSON, gotJSON)
	}
	// 最后一条是指令
	last := gotMsgs[len(gotMsgs)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "会话摘要器") {
		t.Fatalf("最后一条应为摘要指令: %+v", last)
	}
	// system 保持头部
	if gotMsgs[0].Role != "system" {
		t.Fatalf("摘要请求首条应为 system: %s", gotMsgs[0].Role)
	}
}

// ══════════════════════════════════════════════════════════════
// DG-2 (2026-10-11 主人拍板): 触发线按百分比 / 压缩量按 token 目标反推 / 达标判据
//
// 实测根因 (源 = gate_audit.jsonl, 188 次真实长会话压缩): 旧实现「压缩量 = rest/3 夹
// 4~12」与「超出多少 token」无关 —— compressed_msgs 分布 10/12/7/4 全落在夹取区间,
// 于是 23.9% 的压缩压完仍剩 <30% (冷却一过又撞线 = 抖动), 而判据侧对此完全空白。
// 本组判据: ① 70% 占用即触发 (旧实现要满 100%); ② 压后必须到达目标线;
// ③ 压不到目标线必须落 compact_undershoot 审计 (可度量, 不靠事后统计);
// ④ 配置矛盾 (目标 >= 触发) 自动纠正。
// ══════════════════════════════════════════════════════════════

// buildEvenHistory 构造 pairs 对等长消息 (user n rune / assistant n rune)。
// estimateTokens 对每个 rune 计 1, 故 token 量可精确预判 (判据不依赖经验值)。
func buildEvenHistory(system string, pairs, n int) []ChatMessage {
	h := []ChatMessage{{Role: "system", Content: system}}
	for i := 0; i < pairs; i++ {
		h = append(h, ChatMessage{Role: "user", Content: strings.Repeat("u", n)})
		h = append(h, ChatMessage{Role: "assistant", Content: strings.Repeat("a", n)})
	}
	return h
}

// auditEventRows 读隔离审计文件 (TestMain 设 FORGE_AUDIT_PATH) 里某 event 的行。
func auditEventRows(t *testing.T, event string) []map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("FORGE_AUDIT_PATH"))
	if err != nil {
		t.Fatalf("读隔离审计文件失败: %v", err)
	}
	var out []map[string]interface{}
	for _, ln := range strings.Split(string(b), "\n") {
		if !strings.Contains(ln, `"`+event+`"`) {
			continue
		}
		var o map[string]interface{}
		if json.Unmarshal([]byte(ln), &o) != nil {
			continue
		}
		if o["event"] == event {
			out = append(out, o)
		}
	}
	return out
}

// 8. 触发线: 占用达 TriggerPercent% 即动手 (旧实现要满 100% 才压)。
// 区分力对照: 同一份历史在 TriggerPercent=100 下必须不触发 —— 否则用例恒真。
func TestCompactTriggersAtTriggerPercent(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要T"}).handler())
	defer srv.Close()

	// 历史 = 5 对 x 160 rune = 800 token; 阈值 1000 → 70% 线 = 700 (800 >= 700 → 触发)
	build := func() []ChatMessage { return buildEvenHistory("SYS", 5, 80) }

	low := newCompactRunner(t, srv, &Config{CompactEnabled: true, CompactTokenThreshold: 1000,
		CompactTriggerPercent: 70, CompactTargetPercent: 50})
	low.history = build()
	low.maybeCompact()
	if len(low.history) >= len(build()) {
		t.Fatalf("占用 80%% (>=70%% 触发线) 应压缩, 历史未变短: %d", len(low.history))
	}

	high := newCompactRunner(t, srv, &Config{CompactEnabled: true, CompactTokenThreshold: 1000,
		CompactTriggerPercent: 100, CompactTargetPercent: 50})
	high.history = build()
	before := len(high.history)
	high.maybeCompact()
	if len(high.history) != before {
		t.Fatalf("占用 80%% < 100%% 触发线不应压缩, 历史变短: %d → %d", before, len(high.history))
	}
}

// 9. 压缩量按 token 目标反推: 压完对话部分必须 <= 目标线 (旧实现压不干净)。
func TestCompactReachesTargetLine(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要T"}).handler())
	defer srv.Close()
	cfg := &Config{CompactEnabled: true, CompactTokenThreshold: 20000,
		CompactTriggerPercent: 70, CompactTargetPercent: 50}
	a := newCompactRunner(t, srv, cfg)
	// 50 对 x 200 rune = 20000 token; 目标线 = 10000 → 需压掉约 10420 (53 条)
	a.history = buildEvenHistory("SYS", 50, 200)
	if got := contextBudgetUsed(a); got != 20000 {
		t.Fatalf("夹具前提失配: est=%d want 20000", got)
	}
	a.maybeCompact()
	target := compactTargetTokens(cfg)
	if got := contextBudgetUsed(a); got > target {
		t.Fatalf("压后对话 %d 仍超目标线 %d —— 压缩量未按 token 目标反推", got, target)
	}
}

// 10. 压不到目标线时必须落 compact_undershoot 审计 (判据侧空白补上)。
// 构造: 对话量远超阈值 (80000 vs 20000) → 削减需求超过「单次至多压 3/4」护栏 → 必然未达标。
func TestCompactUndershootAudit(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要T"}).handler())
	defer srv.Close()
	cfg := &Config{CompactEnabled: true, CompactTokenThreshold: 20000,
		CompactTriggerPercent: 70, CompactTargetPercent: 50}
	a := newCompactRunner(t, srv, cfg)
	a.history = buildEvenHistory("SYS", 400, 200) // 80000 token
	// 增量判定: 隔离审计文件被整个测试进程共用, 读全量会把别的用例的行算进来。
	before := len(auditEventRows(t, "compact_undershoot"))
	a.maybeCompact()
	if got := contextBudgetUsed(a); got <= compactTargetTokens(cfg) {
		t.Fatalf("夹具前提失配: 本用例需压不达标, 实际已达标 (%d <= %d)",
			got, compactTargetTokens(cfg))
	}
	rows := auditEventRows(t, "compact_undershoot")
	if len(rows) <= before {
		t.Fatal("压后未达标却无 compact_undershoot 审计行 —— 抖动不可度量")
	}
	last := rows[len(rows)-1]
	for _, f := range []string{"conv_after_tokens", "target_tokens", "overshoot_tokens", "compressed_msgs"} {
		if _, ok := last[f]; !ok {
			t.Errorf("compact_undershoot 缺字段 %q (audit_schema.go 必填契约)", f)
		}
	}
	if ov, _ := last["overshoot_tokens"].(float64); ov <= 0 {
		t.Errorf("overshoot_tokens 应 > 0, 实际 %v", last["overshoot_tokens"])
	}
}

// 11. 达标时不得写 undershoot (防判据恒真: 无条件写审计也算"有落痕")。
func TestCompactNoUndershootWhenReached(t *testing.T) {
	srv := httptest.NewServer((&fakeSummaryServer{summary: "摘要T"}).handler())
	defer srv.Close()
	cfg := &Config{CompactEnabled: true, CompactTokenThreshold: 20000,
		CompactTriggerPercent: 70, CompactTargetPercent: 50}
	a := newCompactRunner(t, srv, cfg)
	a.history = buildEvenHistory("SYS", 50, 200) // 20000 token, 同 TestCompactReachesTargetLine
	// 增量判定 (同用例 10): 审计文件跨用例累积, 绝对值断言会互相污染。
	beforeUS := len(auditEventRows(t, "compact_undershoot"))
	beforeCP := len(auditEventRows(t, "compact"))
	a.maybeCompact()
	if got := contextBudgetUsed(a); got > compactTargetTokens(cfg) {
		t.Fatalf("夹具前提失配: 本用例需达标, 实际 %d > %d", got, compactTargetTokens(cfg))
	}
	if n := len(auditEventRows(t, "compact_undershoot")); n != beforeUS {
		t.Fatalf("已达标却新增 %d 条 compact_undershoot", n-beforeUS)
	}
	if n := len(auditEventRows(t, "compact")); n != beforeCP+1 {
		t.Fatalf("compact 审计行增量 = %d, want 1", n-beforeCP)
	}
}

// 12. 配置矛盾 (目标线 >= 触发线) 自动纠正 —— 否则每次压缩都注定不达标。
func TestNormalizeCompactPercents(t *testing.T) {
	cases := []struct {
		trigger, target, wantTarget int
	}{
		{70, 50, 50},  // 正常: 不动
		{70, 70, 35},  // 相等 → 折半 (否则压不到线)
		{70, 90, 35},  // 目标高于触发 → 折半
		{100, 99, 99}, // 未矛盾 (99 < 100): 不纠正 —— 激进但自洽的配置由主人自担
	}
	for _, c := range cases {
		cfg := &Config{CompactTriggerPercent: c.trigger, CompactTargetPercent: c.target}
		normalizeCompactPercents(cfg)
		if cfg.CompactTargetPercent != c.wantTarget {
			t.Errorf("trigger=%d target=%d: got %d want %d",
				c.trigger, c.target, cfg.CompactTargetPercent, c.wantTarget)
		}
	}
	normalizeCompactPercents(nil) // 不得 panic
}

// 13. 预算公式钉住: 触发线/目标线必须真的等于 threshold x 百分比 (含零值兜底)。
// 动机 (变异自检实测): 「压后 <= 目标线」这类端到端判据抓不到「目标线本身被设成 100%」
// 的变异 —— 目标=阈值时压掉一点就"达标"。公式必须由值判据直接钉住, 否则该臂是盲区。
func TestCompactLineFormulas(t *testing.T) {
	cfg := &Config{CompactTokenThreshold: 60000, CompactTriggerPercent: 70, CompactTargetPercent: 50}
	if got := compactTriggerTokens(cfg); got != 42000 {
		t.Errorf("触发线 = %d, want 42000 (60000 x 70%%)", got)
	}
	if got := compactTargetTokens(cfg); got != 30000 {
		t.Errorf("目标线 = %d, want 30000 (60000 x 50%%)", got)
	}
	// 零值兜底: 测试夹具/直接构造 Config 不走 DefaultConfig, 兜底值必须生效
	zero := &Config{CompactTokenThreshold: 1000}
	if got := compactTriggerTokens(zero); got != 700 {
		t.Errorf("零值触发百分比应兜底 70%%: got %d want 700", got)
	}
	if got := compactTargetTokens(zero); got != 500 {
		t.Errorf("零值目标百分比应兜底 50%%: got %d want 500", got)
	}
	if got := compactTriggerTokens(nil); got != 0 {
		t.Errorf("nil cfg 触发线应得 0, 实际 %d", got)
	}
	if got := compactTargetTokens(nil); got != 0 {
		t.Errorf("nil cfg 目标线应得 0, 实际 %d", got)
	}
}

// 14. 默认参数哨兵: 生产默认值必须等于主人拍板值。
// 动机: 本次改的是"默认值", 而默认值此前没有任何判据盯着 —— 被改回旧值 (20000/无百分比)
// 不会有任何用例转红, 等于改动可被静默回滚。主人若再调参数, 本用例须同步更新 (棘轮)。
func TestCompactDefaultParams(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.CompactTokenThreshold != 60000 {
		t.Errorf("默认阈值 = %d, want 60000 (DG-2: 1M 窗口下 20000 太早, 触发全量仅占窗口 4.0%%)",
			cfg.CompactTokenThreshold)
	}
	if cfg.CompactTriggerPercent != 70 {
		t.Errorf("默认触发百分比 = %d, want 70 (主人原话: 注意力少于 30%% 就动作)", cfg.CompactTriggerPercent)
	}
	if cfg.CompactTargetPercent != 50 {
		t.Errorf("默认目标百分比 = %d, want 50 (压一次就到位)", cfg.CompactTargetPercent)
	}
	if cfg.CompactMinTurns != 10 {
		t.Errorf("默认冷却 = %d, want 10 (防前缀频繁变)", cfg.CompactMinTurns)
	}
	if cfg.CompactTargetPercent >= cfg.CompactTriggerPercent {
		t.Errorf("默认配置自相矛盾: 目标 %d >= 触发 %d", cfg.CompactTargetPercent, cfg.CompactTriggerPercent)
	}
}
