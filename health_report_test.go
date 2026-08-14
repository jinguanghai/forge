package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 构造 events.jsonl: 按给定规格写入 (type, detail, ok)
func writeTestEvents(t *testing.T, dir string, spec [][2]string, oks []bool) string {
	t.Helper()
	fdir := filepath.Join(dir, ".forge")
	if err := os.MkdirAll(fdir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fdir, "events.jsonl")
	now := time.Now().Format(time.RFC3339)
	var sb string
	for i, s := range spec {
		typ, detail := s[0], s[1]
		line := `{"ts":"` + now + `","type":"` + typ + `","detail":"` + detail + `"`
		if typ == EvToolResult {
			ok := true
			if i < len(oks) {
				ok = oks[i]
			}
			line += `,"data":{"ok":` + boolStr(ok) + `}}`
		} else {
			line += `}`
		}
		sb += line + "\n"
	}
	if err := os.WriteFile(path, []byte(sb), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// 1. 基础统计: python 失败3次(超阈值) + go 成功 + LLM error 1次(不超)
func TestHealthScanBasic(t *testing.T) {
	dir := t.TempDir()
	spec := [][2]string{
		{EvToolResult, "python"}, {EvToolResult, "python"}, {EvToolResult, "python"},
		{EvToolResult, "go"},
		{EvError, "LLM error"},
	}
	oks := []bool{false, false, false, true}
	path := writeTestEvents(t, dir, spec, oks)
	issues, total := scanHealthEvents(path, 0, time.Time{})
	if total != 5 {
		t.Errorf("应5行, got %d", total)
	}
	if issues["python"] == nil || issues["python"].Count != 3 || issues["python"].Kind != "tool" {
		t.Errorf("python 应3次tool失败, got %+v", issues["python"])
	}
	if issues["LLM error"] == nil || issues["LLM error"].Count != 1 || issues["LLM error"].Kind != "error" {
		t.Errorf("LLM error 应1次, got %+v", issues["LLM error"])
	}
	if issues["go"] != nil {
		t.Errorf("go 成功不应计数: %+v", issues["go"])
	}
	report := buildHealthReport(issues, healthThreshold)
	if report != "python失败×3" {
		t.Errorf("报告应为 python失败×3, got %q", report)
	}
}

// 2. 阈值: 2次失败 → 静默 (不构成问题)
func TestHealthThresholdSilent(t *testing.T) {
	dir := t.TempDir()
	spec := [][2]string{{EvToolResult, "browser"}, {EvToolResult, "browser"}}
	oks := []bool{false, false}
	path := writeTestEvents(t, dir, spec, oks)
	issues, _ := scanHealthEvents(path, 0, time.Time{})
	if r := buildHealthReport(issues, healthThreshold); r != "" {
		t.Errorf("2次失败应静默, got %q", r)
	}
}

// 3. 水位线: 先扫全量, 再增量扫 → 旧问题不重复; 新错误才报
func TestHealthWatermark(t *testing.T) {
	dir := t.TempDir()
	spec := [][2]string{{EvToolResult, "python"}, {EvToolResult, "python"}, {EvToolResult, "python"}}
	oks := []bool{false, false, false}
	path := writeTestEvents(t, dir, spec, oks)
	issues, total := scanHealthEvents(path, 0, time.Time{})
	if r := buildHealthReport(issues, healthThreshold); r == "" {
		t.Fatal("前3次应报告")
	}
	// 增量: 从 total 之后 → 无新问题
	issues2, total2 := scanHealthEvents(path, total, time.Time{})
	if total2 != total {
		t.Errorf("行数应不变 %d, got %d", total, total2)
	}
	if r := buildHealthReport(issues2, healthThreshold); r != "" {
		t.Errorf("增量应静默, got %q", r)
	}
	// 新增 3 条 browser 失败 → 增量应报 browser
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	for i := 0; i < 3; i++ {
		f.WriteString(`{"ts":"2026-08-12T21:00:00+08:00","type":"tool_result","detail":"browser","data":{"ok":false}}` + "\n")
	}
	f.Close()
	issues3, total3 := scanHealthEvents(path, total2, time.Time{})
	if total3 != total2+3 {
		t.Errorf("行数应 +3, got %d", total3)
	}
	if r := buildHealthReport(issues3, healthThreshold); r != "browser失败×3" {
		t.Errorf("增量应报 browser失败×3, got %q", r)
	}
}

// 4. 坏行/空文件/不存在 → 不崩溃
func TestHealthBadLines(t *testing.T) {
	dir := t.TempDir()
	fdir := filepath.Join(dir, ".forge")
	os.MkdirAll(fdir, 0755)
	path := filepath.Join(fdir, "events.jsonl")
	os.WriteFile(path, []byte("{{{not json\n{\"ts\":\"x\",\"type\":\"tool_result\"\n\n"), 0644)
	issues, total := scanHealthEvents(path, 0, time.Time{})
	if len(issues) != 0 || total != 2 {
		t.Errorf("坏行不产生问题但应计入行数(已消费): issues=%d total=%d", len(issues), total)
	}
	// 不存在文件
	issues2, total2 := scanHealthEvents(filepath.Join(fdir, "nope.jsonl"), 0, time.Time{})
	if len(issues2) != 0 || total2 != 0 {
		t.Errorf("不存在应空: %d %d", len(issues2), total2)
	}
	// 空文件
	os.WriteFile(path, []byte(""), 0644)
	issues3, _ := scanHealthEvents(path, 0, time.Time{})
	if len(issues3) != 0 {
		t.Errorf("空文件应空")
	}
}

// 5. 修复信号: 失败后有 self_modified → FixedAfter
func TestHealthFixedAfter(t *testing.T) {
	dir := t.TempDir()
	spec := [][2]string{
		{EvToolResult, "browser"}, {EvToolResult, "browser"}, {EvToolResult, "browser"},
		{EvSelfModified, "replace"},
	}
	oks := []bool{false, false, false}
	path := writeTestEvents(t, dir, spec, oks)
	issues, _ := scanHealthEvents(path, 0, time.Time{})
	if issues["browser"] == nil || !issues["browser"].FixedAfter {
		t.Errorf("失败后自改应标 FixedAfter: %+v", issues["browser"])
	}
	// 反过来: 自改在失败之前 → 不标
	spec2 := [][2]string{
		{EvSelfModified, "replace"},
		{EvToolResult, "python"}, {EvToolResult, "python"}, {EvToolResult, "python"},
	}
	path2 := writeTestEvents(t, dir+"2", spec2, []bool{false, false, false})
	issues2, _ := scanHealthEvents(path2, 0, time.Time{})
	if issues2["python"] != nil && issues2["python"].FixedAfter {
		t.Errorf("自改在先不应标 FixedAfter: %+v", issues2["python"])
	}
}

// 6. 轮转: 小阈值触发归档 + 水位线重置
func TestHealthRotate(t *testing.T) {
	dir := t.TempDir()
	fdir := filepath.Join(dir, ".forge")
	os.MkdirAll(fdir, 0755)
	path := filepath.Join(fdir, "events.jsonl")
	// 写入超过 maxBytes(100B) 的内容
	big := ""
	for i := 0; i < 30; i++ {
		big += `{"ts":"2026-08-12T20:00:00+08:00","type":"tool_result","detail":"python","data":{"ok":true}}` + "\n"
	}
	os.WriteFile(path, []byte(big), 0644)
	writeWatermark(dir, 30)
	rotateEventsIfNeeded(dir, 100, 100000)
	if _, err := os.Stat(path); err == nil {
		t.Error("归档后主文件应不存在")
	}
	archives, _ := filepath.Glob(filepath.Join(fdir, "events_archive_*.jsonl"))
	if len(archives) != 1 {
		t.Errorf("应有1个归档, got %d", len(archives))
	}
	if wm := readWatermark(dir); wm != 0 {
		t.Errorf("轮转后水位线应重置为0, got %d", wm)
	}
	// 归档后新事件可继续写
	logEvent(EvTurnStarted, "轮转后", nil)
}

// 7. 行数阈值轮转
func TestHealthRotateByLines(t *testing.T) {
	dir := t.TempDir()
	fdir := filepath.Join(dir, ".forge")
	os.MkdirAll(fdir, 0755)
	path := filepath.Join(fdir, "events.jsonl")
	var sb string
	for i := 0; i < 5; i++ {
		sb += `{"ts":"2026-08-12T20:00:00+08:00","type":"turn_started","detail":"x"}` + "\n"
	}
	os.WriteFile(path, []byte(sb), 0644)
	rotateEventsIfNeeded(dir, 10*1024*1024, 3) // 行数>3 → 轮转
	if _, err := os.Stat(path); err == nil {
		t.Error("超行数应轮转")
	}
}

// 8. 端到端: 完整函数链 printHealthReport (stderr 重定向捕获)
func TestHealthEndToEndStartup(t *testing.T) {
	dir := t.TempDir()
	spec := [][2]string{{EvToolResult, "browser"}, {EvToolResult, "browser"}, {EvToolResult, "browser"}}
	writeTestEvents(t, dir, spec, []bool{false, false, false})

	old := os.Stderr
	rf, err := os.CreateTemp("", "stderr*.txt")
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = rf
	printHealthReport(dir)
	rf.Close()
	os.Stderr = old

	out, _ := os.ReadFile(rf.Name())
	if !strings.Contains(string(out), "躯壳自检") || !strings.Contains(string(out), "browser失败×3") {
		t.Errorf("启动应输出躯壳自检报告, got %q", string(out))
	}
	if wm := readWatermark(dir); wm != 3 {
		t.Errorf("启动后水位线应=3(已扫描), got %d", wm)
	}
	// 再次启动 → 无新错误 → 静默 (水位线防重复)
	old2 := os.Stderr
	rf2, _ := os.CreateTemp("", "stderr2*.txt")
	os.Stderr = rf2
	printHealthReport(dir)
	rf2.Close()
	os.Stderr = old2
	out2, _ := os.ReadFile(rf2.Name())
	if strings.Contains(string(out2), "躯壳自检") {
		t.Errorf("重复启动应静默(水位线生效), got %q", string(out2))
	}
}

// 9. 端到端: 每轮增量自检提示
func TestHealthEndToEndPerTurn(t *testing.T) {
	dir := t.TempDir()
	// 启动扫描(无错误) → 水位线=0
	old := os.Stderr
	rf, _ := os.CreateTemp("", "stderr*.txt")
	os.Stderr = rf
	printHealthReport(dir)
	rf.Close()
	os.Stderr = old

	// 新错误 3 条 → 每轮自检应提示
	spec := [][2]string{{EvToolResult, "python"}, {EvToolResult, "python"}, {EvToolResult, "python"}}
	writeTestEvents(t, dir, spec, []bool{false, false, false})

	old2 := os.Stderr
	rf2, _ := os.CreateTemp("", "stderr*.txt")
	os.Stderr = rf2
	maybePrintHealthHint(dir)
	rf2.Close()
	os.Stderr = old2
	out, _ := os.ReadFile(rf2.Name())
	if !strings.Contains(string(out), "自检") || !strings.Contains(string(out), "python失败×3") {
		t.Errorf("每轮应提示新问题, got %q", string(out))
	}
	// 再跑一轮 → 无新错误 → 静默
	old3 := os.Stderr
	rf3, _ := os.CreateTemp("", "stderr*.txt")
	os.Stderr = rf3
	maybePrintHealthHint(dir)
	rf3.Close()
	os.Stderr = old3
	out2, _ := os.ReadFile(rf3.Name())
	if strings.Contains(string(out2), "自检") {
		t.Errorf("无新错误应静默, got %q", string(out2))
	}
}
