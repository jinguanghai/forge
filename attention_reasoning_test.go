package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// attention_reasoning_test.go —— 「注意力剩余」分子口径哨兵 (2026-10-08)。
//
// 缺陷: contextBudgetUsed 直接估算 history[headLen:] 全量, 而真正发出去的请求在
// buildStreamMessages (agent_stream_loop.go) 里已经过 stripCrossTurnReasoning 剥离
// 跨轮 assistant 的 reasoning_content (deepseek-harness §1.3 规则3: 服务端跨轮不要求
// 回传, 保留还破坏前缀缓存) —— 分子把「不发出去的思考段」算进了已用量。后果:
//  1. 状态栏「注意力剩余」系统性偏低;
//  2. maybeCompact 共用同一函数 → 历史压缩被提前触发 (白付摘要费 + 周期性丢细节)。
//
// 判据分层:
//  1. 功能·剔除: 无 tool_calls 的 assistant reasoning 不计入 (附旧口径对照, 证明有区分力)
//  2. 功能·保留: 有 tool_calls 的 assistant reasoning 仍计入 (§1.3 规则2 仍回传)
//  3. 同口径: contextBudgetUsed == estimateTokens(stripCrossTurnReasoning(history[head:]))
//  4. 端到端: 显示值随剥离联动 (剩% 不得被 reasoning 拉低)
//  5. 接线(AST): contextBudgetUsed 函数体必须调用 stripCrossTurnReasoning
//
// 变异验证: 把 contextBudgetUsed 的 return 改回 estimateTokens(a.history[head:])
// → 第 1/3/5 组转红 (已实测)。

const attnRTestLimit = 20000

// attnReasoningAgent 构造 headLen=1 (固定头占 history[0]) 的注意力测试 agent。
func attnReasoningAgent() *AgentRunner {
	a := newStatusBarTestAgent()
	a.cfg = &Config{CompactTokenThreshold: attnRTestLimit}
	a.headLen = 1
	return a
}

// ── 1. 功能·剔除: 跨轮 reasoning 不得计入分子 ────────────────
func TestContextBudgetUsed_ExcludesCrossTurnReasoning(t *testing.T) {
	a := attnReasoningAgent()
	a.history = []ChatMessage{
		{Role: "system", Content: "SYS"}, // 固定头 (headLen=1, 不参与分子)
		// 终答: 无 tool_calls → 跨轮必须剥离 reasoning (1000 rune)
		{Role: "assistant", Content: strings.Repeat("c", 10), ReasoningContent: strings.Repeat("r", 1000)},
		{Role: "user", Content: strings.Repeat("u", 20)},
	}
	// 对照: 证明本用例有区分力 —— 旧口径 (不剥离) 确实是 10+1000+20。
	// 这条断言同时是「判据自身的判据」: 若有人把 estimateTokens 改成不数 reasoning,
	// 对照值即失配 → 本组转红, 不会因为两边同时改变而假绿。
	if raw := estimateTokens(a.history[1:]); raw != 1030 {
		t.Fatalf("对照失配: 旧口径应为 1030 (10 content + 1000 reasoning + 20 user), 实际 %d", raw)
	}
	if got := contextBudgetUsed(a); got != 30 {
		t.Errorf("contextBudgetUsed=%d, want 30 (跨轮 reasoning 不发出去, 不得计入分子)", got)
	}
}

// ── 2. 功能·保留: 工具轮的 reasoning 仍回传, 必须计入 ────────
func TestContextBudgetUsed_KeepsToolCallReasoning(t *testing.T) {
	a := attnReasoningAgent()
	tc := ToolCall{ID: "t1", Type: "function", Function: FunctionCall{Name: "forge", Arguments: "{}"}}
	a.history = []ChatMessage{
		{Role: "system", Content: "SYS"},
		// 带 tool_calls → stripCrossTurnReasoning 不动它 (§1.3 规则2)
		{Role: "assistant", ReasoningContent: strings.Repeat("r", 500), ToolCalls: []ToolCall{tc}},
		{Role: "tool", ToolCallID: "t1", Content: strings.Repeat("o", 40)},
	}
	// reasoning 500 + arguments "{}" 2 + tool 输出 40
	if got := contextBudgetUsed(a); got != 542 {
		t.Errorf("contextBudgetUsed=%d, want 542 (工具轮 reasoning 仍上线, 必须计入)", got)
	}
}

// ── 3. 同口径: 分子 == 发送路径消息的估算 ────────────────────
func TestContextBudgetUsed_MatchesSendPath(t *testing.T) {
	a := attnReasoningAgent()
	a.history = []ChatMessage{
		{Role: "system", Content: "SYS"},
		{Role: "assistant", Content: "a", ReasoningContent: strings.Repeat("r", 300)},
		{Role: "user", Content: "u"},
		{Role: "assistant", ReasoningContent: strings.Repeat("r", 700),
			ToolCalls: []ToolCall{{ID: "t1", Function: FunctionCall{Name: "f", Arguments: "{}"}}}},
		{Role: "tool", ToolCallID: "t1", Content: "ok"},
	}
	want := estimateTokens(stripCrossTurnReasoning(a.history[a.headLen:]))
	if got := contextBudgetUsed(a); got != want {
		t.Errorf("contextBudgetUsed=%d, want %d (必须与 buildStreamMessages 发送路径同口径)", got, want)
	}
	// 两种形态并存: 300 被剥, 700 保留 → 分子应严格介于两者之间
	if want >= estimateTokens(a.history[a.headLen:]) {
		t.Errorf("同口径值 %d 未剔除任何 reasoning (对照 %d)", want, estimateTokens(a.history[a.headLen:]))
	}
}

// ── 4. 端到端: 显示值随剥离联动 ──────────────────────────────
func TestContextUsageText_IgnoresCrossTurnReasoning(t *testing.T) {
	a := attnReasoningAgent()
	a.history = []ChatMessage{
		{Role: "system", Content: "SYS"},
		{Role: "user", Content: strings.Repeat("u", 1000)},
		// 终答正文 1000 + 思考 8000: 旧口径 10000 → 剩50% (黄); 新口径 2000 → 剩90% (绿)
		{Role: "assistant", Content: strings.Repeat("c", 1000), ReasoningContent: strings.Repeat("r", 8000)},
	}
	if raw := estimateTokens(a.history[1:]); raw != 10000 {
		t.Fatalf("对照失配: 旧口径应为 10000, 实际 %d", raw)
	}
	got := stripANSI(contextUsageText(a))
	if !strings.Contains(got, "剩90%") {
		t.Errorf("显示 %q, want 含「剩90%%」(reasoning 不发出去, 不得拉低剩余)", got)
	}
	if strings.Contains(got, "剩50%") {
		t.Errorf("显示 %q 仍是旧口径 (reasoning 被计入)", got)
	}
}

// ── 5. 接线(AST): 剥离动作必须真的在 contextBudgetUsed 里 ────
//
// 接线只能由死程序判定 (公理三): 注释里写"同口径"不算证据。
// AST 扫描天然剥离注释与字符串, 注释提及函数名不会假阳性。
func TestContextBudgetUsed_StripsReasoningOnSendPath(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "agent_trim.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 agent_trim.go 失败: %v", err)
	}
	foundFn, foundCall := false, false
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		if fd.Name.Name != "contextBudgetUsed" {
			return true
		}
		foundFn = true
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			if ce, ok := m.(*ast.CallExpr); ok {
				if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "stripCrossTurnReasoning" {
					foundCall = true
				}
			}
			return true
		})
		return true
	})
	if !foundFn {
		t.Fatalf("agent_trim.go 未找到 contextBudgetUsed 定义")
	}
	if !foundCall {
		t.Errorf("contextBudgetUsed 未调用 stripCrossTurnReasoning —— 分子与发送路径口径已分叉")
	}
}
