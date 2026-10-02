package main

// cov_gaps2_more_test.go — 覆盖率缺口补测 第二批 (跨平台, 20261001)
//
// 覆盖会话命令分发 (/sessions /new /use)、会话级 gate 统计解析与 /stats 统计分支。
// 全部落在 t.TempDir; 全局状态 (currentSession/eventSession) 用完即还原。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- main_interactive.go: handleSessionCmd ----

func TestCovGap2_HandleSessionCmd_AllBranches(t *testing.T) {
	agent, cfg, _ := newHandleCmdAgent(t)
	t.Cleanup(func() {
		setCurrentSession("")
		setEventSession("")
	})
	setCurrentSession("")
	var ckpH []ChatMessage

	// 1) /sessions 且无历史会话
	out := captureStdout(t, func() {
		if !handleSessionCmd("/sessions", "/sessions", cfg, agent, &ckpH) {
			t.Error("/sessions 应被判定为已处理")
		}
	})
	if !strings.Contains(out, "暂无历史会话") {
		t.Fatalf("空列表应给出引导: %q", out)
	}

	// 2) /new 创建新会话
	out = captureStdout(t, func() {
		if !handleSessionCmd("/new", "/new", cfg, agent, &ckpH) {
			t.Error("/new 应被判定为已处理")
		}
	})
	if !strings.Contains(out, "已创建新会话") {
		t.Fatalf("/new 应报告创建成功: %q", out)
	}
	id := currentSession()
	if id == "" {
		t.Fatal("/new 后当前会话 id 不得为空")
	}

	// 3) /sessions 有列表 (当前会话应带标记)
	out = captureStdout(t, func() { handleSessionCmd("/sessions", "/sessions", cfg, agent, &ckpH) })
	if !strings.Contains(out, "会话列表") {
		t.Fatalf("应有会话列表: %q", out)
	}
	if !strings.Contains(out, id) {
		t.Fatalf("列表应包含当前会话 %s: %q", id, out)
	}

	// 4) /use 无参数
	out = captureStdout(t, func() {
		if !handleSessionCmd("/use ", "/use ", cfg, agent, &ckpH) {
			t.Error("/use 空参应被判定为已处理")
		}
	})
	if !strings.Contains(out, "用法") {
		t.Fatalf("空参应给用法提示: %q", out)
	}

	// 5) /use 不存在的会话 id
	out = captureStdout(t, func() {
		if !handleSessionCmd("/use no_such_id", "/use no_such_id", cfg, agent, &ckpH) {
			t.Error("/use 不存在 id 应被判定为已处理")
		}
	})
	if !strings.Contains(out, "不存在") {
		t.Fatalf("应提示会话不存在: %q", out)
	}

	// 6) /use 有效 id → 切换成功
	setCurrentSession("") // 先回到 legacy, 确保切换真的发生
	out = captureStdout(t, func() {
		if !handleSessionCmd("/use "+id, "/use "+id, cfg, agent, &ckpH) {
			t.Error("/use 有效 id 应被判定为已处理")
		}
	})
	if !strings.Contains(out, "已切换") && !strings.Contains(out, "已恢复") {
		t.Fatalf("应报告切换结果: %q", out)
	}
	if currentSession() != id {
		t.Fatalf("当前会话未切换: got %q, want %q", currentSession(), id)
	}

	// 7) 非会话命令 → false (不得吞掉别的命令)
	if handleSessionCmd("/help", "/help", cfg, agent, &ckpH) {
		t.Fatal("非会话命令不应被本函数处理")
	}
}

// ---- session_stats.go: collectSessionStats ----

func writeCovGap2EventLines(t *testing.T, wd string, lines []string) {
	t.Helper()
	dir := filepath.Join(wd, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCovGap2_CollectSessionStats_ParsesEvents(t *testing.T) {
	wd := t.TempDir()
	setCurrentSession("") // legacy: 事件路径 = <wd>/.forge/events.jsonl
	setEventSession("")
	t.Cleanup(func() { setCurrentSession(""); setEventSession("") })

	writeCovGap2EventLines(t, wd, []string{
		`{"ts":"2026-10-01T10:00:00+08:00","type":"tool_result","detail":"python","data":{"ok":true}}`,
		`{"ts":"2026-10-01T10:01:00+08:00","type":"tool_result","detail":"python","data":{"ok":false}}`,
		`{"ts":"2026-10-01T10:02:00+08:00","type":"tool_result","detail":"math","data":{"ok":true}}`,
		`{"ts":"2026-10-01T10:03:00+08:00","type":"turn_started","detail":"python"}`,                                 // 非 tool_result → 不计
		`{"ts":"2026-10-01T10:04:00+08:00","type":"tool_result","detail":"","data":{"ok":true}}`,                     // detail 空 → unknown
		`{"ts":"2026-10-01T10:05:00+08:00","type":"tool_result","detail":"go","session":"other","data":{"ok":true}}`, // 别的会话 → 跳过
		`not-json`, // 坏行必须被跳过而非 panic
		``,
	})

	ss := collectSessionStats(wd)
	if ss == nil {
		t.Fatal("collectSessionStats 返回 nil")
	}
	if ss.Total != 4 || ss.OK != 3 || ss.Fail != 1 {
		t.Fatalf("统计不符: total=%d ok=%d fail=%d (want 4/3/1)", ss.Total, ss.OK, ss.Fail)
	}
	if len(ss.Gates) != 3 {
		t.Fatalf("应有 3 个 gate (python/math/unknown), 实际 %+v", ss.Gates)
	}
	// 按调用数降序: python(2) 必须排第一
	if ss.Gates[0].Lang != "python" || ss.Gates[0].Calls != 2 || ss.Gates[0].OK != 1 || ss.Gates[0].Fail != 1 {
		t.Fatalf("排序/计数不符: %+v", ss.Gates[0])
	}
	if ss.Gates[0].LastErr == "" {
		t.Fatal("失败 gate 应记录最后一次错误摘要")
	}

	// 空 workDir 与 无事件文件: 均返回空统计, 绝不 panic
	if got := collectSessionStats(""); got == nil || got.Total != 0 {
		t.Fatalf("空 workDir 应返回空统计, 实际 %+v", got)
	}
	if got := collectSessionStats(t.TempDir()); got == nil || got.Total != 0 {
		t.Fatalf("无事件文件应返回空统计, 实际 %+v", got)
	}
}

// ---- commands_session.go: cmdStats 的 gates 分支 ----

func TestCovGap2_CmdStats_WithGateRecords(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	setCurrentSession("")
	setEventSession("")
	t.Cleanup(func() { setCurrentSession(""); setEventSession("") })

	writeCovGap2EventLines(t, cfg.WorkDir, []string{
		`{"ts":"2026-10-01T10:00:00+08:00","type":"tool_result","detail":"python","data":{"ok":true}}`,
		`{"ts":"2026-10-01T10:01:00+08:00","type":"tool_result","detail":"math","data":{"ok":false}}`,
	})

	sr := false
	out := captureStdout(t, func() { cmdStats(agent, cfg, nil, hist, &sr, "/stats") })
	if !strings.Contains(out, "gates[legacy]") {
		t.Fatalf("应输出 legacy 会话的 gate 统计: %q", out)
	}
	if !strings.Contains(out, "python") || !strings.Contains(out, "math") {
		t.Fatalf("应逐 gate 输出: %q", out)
	}
}
