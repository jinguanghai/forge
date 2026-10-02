// session_stats_test.go: 按会话的 gate 调用统计(/stats 增强)的行为契约。
//
// 防的是"跨会话串味": 会话过滤一旦失效, 别人的统计会混进当前会话。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withSessionID(t *testing.T, id string) {
	t.Helper()
	old := currentSession()
	setCurrentSession(id)
	t.Cleanup(func() { setCurrentSession(old) })
}

func writeEventLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func evLine(t *testing.T, ev Event) string {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSessionGateStats_EventsFilePath(t *testing.T) {
	dir := t.TempDir()
	withSessionID(t, "")
	legacy := eventsFilePath(dir)
	if legacy != filepath.Join(dir, ".forge", "events.jsonl") {
		t.Errorf("legacy 路径 = %q", legacy)
	}
	withSessionID(t, "s-2026")
	got := eventsFilePath(dir)
	want := filepath.Join(dir, ".forge", "sessions", "s-2026", "events.jsonl")
	if got != want {
		t.Errorf("会话路径 = %q 期望 %q", got, want)
	}
}

func TestSessionGateStats_EmptyAndMissing(t *testing.T) {
	withSessionID(t, "")
	// workDir 为空 → 空统计, 不 panic
	out := collectSessionStats("")
	if out == nil || out.Total != 0 || len(out.Gates) != 0 {
		t.Errorf("空 workDir 应返回空统计: %+v", out)
	}
	// 文件不存在 → 空统计
	out = collectSessionStats(t.TempDir())
	if out == nil || out.Total != 0 || out.Fail != 0 || out.OK != 0 {
		t.Errorf("缺失文件应返回空统计: %+v", out)
	}
}

func TestSessionGateStats_CollectLegacy(t *testing.T) {
	dir := t.TempDir()
	withSessionID(t, "")
	path := eventsFilePath(dir)
	lines := []string{
		evLine(t, Event{Type: EvToolResult, Detail: "python", Data: map[string]interface{}{"ok": true}}),
		evLine(t, Event{Type: EvToolResult, Detail: "python", Data: map[string]interface{}{"ok": false}}),
		evLine(t, Event{Type: EvToolResult, Detail: "go", Data: map[string]interface{}{"ok": true}}),
		evLine(t, Event{Type: "other_event", Detail: "python", Data: map[string]interface{}{"ok": false}}),
		`{"ts":"x","type":"tool_result"`, // 非法 JSON 行 → 跳过
		"",                               // 空行 → 跳过
	}
	writeEventLines(t, path, lines)

	out := collectSessionStats(dir)
	if out.Total != 3 || out.OK != 2 || out.Fail != 1 {
		t.Fatalf("统计 total/ok/fail = %d/%d/%d 期望 3/2/1", out.Total, out.OK, out.Fail)
	}
	if len(out.Gates) != 2 {
		t.Fatalf("gate 数 = %d 期望 2: %+v", len(out.Gates), out.Gates)
	}
	if out.Gates[0].Lang != "python" || out.Gates[0].Calls != 2 {
		t.Errorf("首位应为 python(2 次): %+v", out.Gates[0])
	}
	if out.Gates[0].OK != 1 || out.Gates[0].Fail != 1 {
		t.Errorf("python 成败 = %d/%d 期望 1/1", out.Gates[0].OK, out.Gates[0].Fail)
	}
	if out.Gates[1].Lang != "go" || out.Gates[1].Calls != 1 {
		t.Errorf("次位应为 go(1 次): %+v", out.Gates[1])
	}
	// 失败项的 LastErr 不得为空(便于定位), 且已按 rune 截断
	if out.Gates[0].LastErr == "" {
		t.Error("失败项应有 LastErr")
	}
	if r := []rune(out.Gates[0].LastErr); len(r) > 41 {
		t.Errorf("LastErr 未截断: %d 字", len(r))
	}
}

func TestSessionGateStats_UnknownLang(t *testing.T) {
	dir := t.TempDir()
	withSessionID(t, "")
	writeEventLines(t, eventsFilePath(dir), []string{
		evLine(t, Event{Type: EvToolResult, Detail: "", Data: map[string]interface{}{"ok": true}}),
	})
	out := collectSessionStats(dir)
	if len(out.Gates) != 1 || out.Gates[0].Lang != "unknown" {
		t.Errorf("空 detail 应归入 unknown: %+v", out.Gates)
	}
}

func TestSessionGateStats_SessionFilter(t *testing.T) {
	dir := t.TempDir()
	withSessionID(t, "s1")
	writeEventLines(t, eventsFilePath(dir), []string{
		evLine(t, Event{Type: EvToolResult, Session: "s1", Detail: "python", Data: map[string]interface{}{"ok": true}}),
		evLine(t, Event{Type: EvToolResult, Session: "s2", Detail: "python", Data: map[string]interface{}{"ok": false}}),
		evLine(t, Event{Type: EvToolResult, Session: "", Detail: "python", Data: map[string]interface{}{"ok": true}}),
	})
	out := collectSessionStats(dir)
	if out.SessionID != "s1" {
		t.Errorf("SessionID = %q 期望 s1", out.SessionID)
	}
	// 只有 s1 与非空匹配项计入; s2 必须被过滤
	if out.Total != 2 || out.OK != 2 || out.Fail != 0 {
		t.Errorf("会话过滤失效: total/ok/fail = %d/%d/%d 期望 2/2/0", out.Total, out.OK, out.Fail)
	}
}

func TestSessionGateStats_SortByCallsDesc(t *testing.T) {
	dir := t.TempDir()
	withSessionID(t, "")
	var lines []string
	add := func(lang string, n int) {
		for i := 0; i < n; i++ {
			lines = append(lines, evLine(t, Event{Type: EvToolResult, Detail: lang,
				Data: map[string]interface{}{"ok": true}}))
		}
	}
	add("zeta", 1)
	add("alpha", 3)
	add("beta", 2)
	writeEventLines(t, eventsFilePath(dir), lines)

	out := collectSessionStats(dir)
	var got []string
	for _, g := range out.Gates {
		got = append(got, g.Lang)
	}
	want := []string{"alpha", "beta", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("排序 = %v 期望 %v (按调用数降序)", got, want)
	}
}

func TestSessionGateStats_SameCallsSortedByLang(t *testing.T) {
	dir := t.TempDir()
	withSessionID(t, "")
	writeEventLines(t, eventsFilePath(dir), []string{
		evLine(t, Event{Type: EvToolResult, Detail: "go", Data: map[string]interface{}{"ok": true}}),
		evLine(t, Event{Type: EvToolResult, Detail: "math", Data: map[string]interface{}{"ok": true}}),
		evLine(t, Event{Type: EvToolResult, Detail: "go", Data: map[string]interface{}{"ok": true}}),
		evLine(t, Event{Type: EvToolResult, Detail: "math", Data: map[string]interface{}{"ok": true}}),
	})
	out := collectSessionStats(dir)
	if len(out.Gates) != 2 {
		t.Fatalf("gate 数 = %d 期望 2", len(out.Gates))
	}
	// 调用数相同时按 Lang 升序 —— 保证输出确定性(否则同一份日志两次展示顺序可能不同)
	if out.Gates[0].Lang != "go" || out.Gates[1].Lang != "math" {
		t.Errorf("同调用数应按 Lang 升序: %+v", out.Gates)
	}
}
