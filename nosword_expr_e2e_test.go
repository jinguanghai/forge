package main

// ── nosword_expr_e2e_test.go — 表达式化强制端到端 (20260920 无剑二期) ──
// 不依赖真机 API: httptest 假冒流式端点, 真实走 AgentRunner.RunStream 主循环。
// 验证三件事:
//   ① 展示层替换: LLM 吐 {{算式}} → 用户看到的是求值结果
//   ② 历史存原文: 请求体里保留标记 (模型下轮看到自己的格式才会自维持)
//   ③ 拒绝权: 非法标记 → 死程序拒绝执行 → 注入反馈让模型改写 (带来源信封)

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nswCaptureStdout 捕获 processStream 的渲染输出 (它直接写 os.Stdout)。
func nswCaptureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

// TestNSWExprE2E_RenderAndHistory: 合法标记 → 展示求值结果, 历史留原文
func TestNSWExprE2E_RenderAndHistory(t *testing.T) {
	saveGlobals(t)
	// t.Setenv 自动恢复; FORGE_NOSWORD 置空以隔离一期嗅探路径 (本用例只测表达式化)
	t.Setenv("FORGE_NSW_EXPR", "1")
	t.Setenv("FORGE_NOSWORD", "")

	reply := "一共 {{3*25.5}} 元。"
	srv, bodies := nswMockServer([]string{reply})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	out := nswCaptureStdout(t, func() {
		if e := a.RunStream("3 剂药每剂 25.5 元多少钱"); e != nil {
			t.Errorf("RunStream: %v", e)
		}
	})
	if !strings.Contains(out, "76.5") {
		t.Errorf("展示层未出现求值结果 76.5; 实际输出=%q", out)
	}
	if strings.Contains(out, "{{3*25.5}}") {
		t.Errorf("展示层仍显示标记原文; 实际输出=%q", out)
	}
	// system 必须带约束段 (否则模型不知道要写标记)
	reqs := bodies()
	if len(reqs) == 0 {
		t.Fatal("无请求记录")
	}
	if !strings.Contains(reqs[0], "numeric_output") {
		t.Error("system 未含表达式化约束段")
	}
	// 历史存原文: 模型下轮看到自己写过的标记格式才会自维持
	last := a.history[len(a.history)-1]
	if last.Content != reply {
		t.Errorf("history 应存原文 %q, 实得 %q", reply, last.Content)
	}
	// 埋点 (20260920): 触发率不可测则无法判断价值 -> 审计必须留痕
	ab, err := os.ReadFile(filepath.Join(a.cfg.WorkDir, "gate_audit.jsonl"))
	if err != nil {
		t.Fatalf("读审计文件失败: %v", err)
	}
	if !strings.Contains(string(ab), "nosword_expr") {
		t.Error("审计未记录 nosword_expr 事件 -> 表达式化触发率不可测")
	}
	if !strings.Contains(string(ab), `"marks":1`) {
		t.Errorf("审计未记录标记数 marks=1; 实际=%s", string(ab))
	}
	t.Logf("✅ 展示替换 76.5 / 历史留原文 / system 带约束 / 埋点已留痕")
}

// TestNSWExprE2E_RejectInjected: 非法标记 → 拒绝权注入 + 续生成
func TestNSWExprE2E_RejectInjected(t *testing.T) {
	saveGlobals(t)
	t.Setenv("FORGE_NSW_EXPR", "1")
	t.Setenv("FORGE_NOSWORD", "")

	srv, bodies := nswMockServer([]string{"这个 {{35元*2}} 算不出来。", "改好了，是 70 元。"})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	nswCaptureStdout(t, func() {
		if e := a.RunStream("算一下"); e != nil {
			t.Errorf("RunStream: %v", e)
		}
	})
	reqs := bodies()
	if len(reqs) < 2 {
		t.Fatalf("拒绝权未触发: 只发了 %d 次请求 (期望 >=2)", len(reqs))
	}
	second := reqs[1]
	if !strings.Contains(second, "35元*2") {
		t.Errorf("第2次请求未回告非法标记; 尾部=%q", tail(second, 400))
	}
	if !strings.Contains(second, "无法求值") {
		t.Errorf("第2次请求缺少拒绝权反馈本体; 尾部=%q", tail(second, 400))
	}
	if !strings.Contains(second, "非用户消息") {
		t.Errorf("拒绝权注入缺来源信封 (缺陷P 复发); 尾部=%q", tail(second, 400))
	}
	if strings.Index(second, "非用户消息") > strings.Index(second, "无法求值") {
		t.Error("信封必须排在反馈本体之前")
	}
	last := a.history[len(a.history)-1]
	if last.Content != "改好了，是 70 元。" {
		t.Errorf("收尾历史异常: %q", last.Content)
	}
	t.Logf("✅ 拒绝权闭环: 非法标记 → 回告 → 第2轮改写收尾")
}

// TestNSWExprE2E_Off: 开关关 → 零替换、零注入、system 无约束段
func TestNSWExprE2E_Off(t *testing.T) {
	saveGlobals(t)
	t.Setenv("FORGE_NSW_EXPR", "")
	t.Setenv("FORGE_NOSWORD", "")

	reply := "一共 {{3*25.5}} 元。"
	srv, bodies := nswMockServer([]string{reply})
	defer srv.Close()
	a := nswNewTestAgent(t, srv.URL)

	out := nswCaptureStdout(t, func() {
		if e := a.RunStream("算一下"); e != nil {
			t.Errorf("RunStream: %v", e)
		}
	})
	reqs := bodies()
	if len(reqs) != 1 {
		t.Fatalf("开关关闭仍有额外请求: %d 次", len(reqs))
	}
	if strings.Contains(reqs[0], "numeric_output") {
		t.Error("开关关闭仍注入约束段 -> 缓存前缀被污染")
	}
	if strings.Contains(reqs[0], "非用户消息") {
		t.Error("开关关闭仍注入信封")
	}
	if !strings.Contains(out, "{{3*25.5}}") {
		t.Errorf("开关关闭时不应替换标记 (行为必须与现状一致); 输出=%q", out)
	}
	t.Logf("✅ 默认关: 1 次请求, 零替换, 零注入")
}
