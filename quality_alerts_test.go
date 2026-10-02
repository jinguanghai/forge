package main

// quality_alerts_test.go — 质量告警出口的确定性验证 (死程序判定, 不靠肉眼)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeAlerts(t *testing.T, dir string, lines ...string) {
	t.Helper()
	ff := filepath.Join(dir, ".forge")
	if err := os.MkdirAll(ff, 0755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(ff, "alerts.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// 横幅摘要: 只数非 info 告警, 高危单独计数。
func TestQualityAlertBannerLine(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+now+`","sev":"high","metric":"gate_fail_rate","key":"sh","value":0.5,"threshold":0.3,"msg":"gate sh 失败率 50.8%"}`,
		`{"ts":"`+now+`","sev":"info","metric":"log_rotated","key":"events.jsonl","value":0,"threshold":0,"msg":"已轮转"}`,
	)
	line := qualityAlertBannerLine(dir)
	if !strings.Contains(line, "1 条") || !strings.Contains(line, "高危 1") {
		t.Fatalf("横幅摘要不符: %q", line)
	}
	if strings.Contains(line, "已轮转") {
		t.Fatalf("info 记录不该进告警: %q", line)
	}
}

// 无告警文件 / 无告警 → 空串 (横幅不出现该行)。
func TestQualityAlertEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := qualityAlertBannerLine(dir); got != "" {
		t.Fatalf("无告警时应为空串, 得到 %q", got)
	}
	if got := qualityAlertBannerLine(""); got != "" {
		t.Fatalf("空 workDir 应安全返回空串, 得到 %q", got)
	}
}

// 窗口过滤 + 同键去重保留最新。
func TestQualityAlertDedupAndWindow(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-48 * time.Hour).Format(time.RFC3339)
	recent := now.Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+old+`","sev":"high","metric":"gate_fail_rate","key":"sh","msg":"旧"}`,
		`{"ts":"`+recent+`","sev":"high","metric":"gate_fail_rate","key":"sh","msg":"新"}`,
	)
	as := readQualityAlerts(dir, 24*time.Hour)
	if len(as) != 1 {
		t.Fatalf("窗口外应被过滤且同键去重, 得 %d 条", len(as))
	}
	if as[0].Msg != "新" {
		t.Fatalf("应保留最新记录, 得 %q", as[0].Msg)
	}
}

// 坏行不崩 (写一半/非法 JSON) —— 零崩溃契约。
func TestQualityAlertBadLines(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+now+`","sev":"warn","metric":"m","key":"k","msg":"好"}`,
		`{坏行`,
		``,
		`不是JSON`,
	)
	as := readQualityAlerts(dir, 24*time.Hour)
	if len(as) != 1 {
		t.Fatalf("坏行应被跳过, 好行保留, 得 %d 条", len(as))
	}
}

// 真实工作目录冒烟: 读得到就解析, 读不到不 panic。
func TestQualityAlertsRealWorkdirSmoke(t *testing.T) {
	as := readQualityAlerts(`D:\forge`, 24*time.Hour)
	t.Logf("真实工作目录告警 %d 条", len(as))
	for _, a := range as {
		t.Logf("  [%s] %s", a.Sev, a.Msg)
	}
}

// 横幅集成: buildBanner 必须把质量告警渲染进去 (此前只有文件, 无人知)。
func TestBannerShowsQualityAlert(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+now+`","sev":"high","metric":"gate_fail_rate","key":"sh","value":0.5,"threshold":0.3,"msg":"gate sh 失败率 50.8%"}`,
	)
	cfg := &Config{WorkDir: dir, Model: "m", BaseURL: "u"}
	joined := strings.Join(buildBanner(cfg), "\n")
	if !strings.Contains(joined, "告 警") {
		t.Fatalf("横幅缺少告警行:\n%s", joined)
	}
	if !strings.Contains(joined, "gate sh 失败率") {
		t.Fatalf("横幅未带告警摘要:\n%s", joined)
	}
	// 无告警时横幅不该出现该行
	empty := t.TempDir()
	cfg2 := &Config{WorkDir: empty, Model: "m", BaseURL: "u"}
	if strings.Contains(strings.Join(buildBanner(cfg2), "\n"), "告 警") {
		t.Fatal("无告警时横幅不应出现告警行")
	}
}

// 无时区时间戳(历史数据/其它脚本产出)也必须受时间窗口约束 ——
// 缺陷回归: 旧实现 `perr == nil && ts.Before(cutoff)` 把"解析失败"当成"未过期",
// 一条缺 +08:00 的过期告警绕过窗口永久常驻启动横幅 (2026-09-21 僵尸告警)。
func TestQualityAlertNaiveTsWindow(t *testing.T) {
	dir := t.TempDir()
	naiveOld := time.Now().Add(-48 * time.Hour).Format("2006-01-02T15:04:05")
	naiveNew := time.Now().Format("2006-01-02T15:04:05")
	writeAlerts(t, dir,
		`{"ts":"`+naiveOld+`","sev":"warn","metric":"fractal_guard","key":"structure","msg":"过期"}`,
		`{"ts":"`+naiveNew+`","sev":"warn","metric":"m","key":"k","msg":"当下"}`,
	)
	as := readQualityAlerts(dir, 24*time.Hour)
	if len(as) != 1 || as[0].Msg != "当下" {
		t.Fatalf("无时区时间戳须按本地时区参与窗口过滤, 得 %d 条: %+v", len(as), as)
	}
	if _, ok := parseAlertTs(naiveOld); !ok {
		t.Fatal("无时区时间戳应能回退解析")
	}
	if _, ok := parseAlertTs(naiveOld + "+08:00"); !ok {
		t.Fatal("RFC3339 时间戳应走首选解析")
	}
	if _, ok := parseAlertTs("不是时间"); ok {
		t.Fatal("非法时间戳不该解析成功 (须保留该条, 宁可多报)")
	}
}

// 恢复标记: 指标回落后追加的同键记录必须把旧告警撤下 ——
// 缺陷回归 (2026-10-01): 结构违规 19:46 已修好(复检 0 违规), 横幅仍常驻
// 「3 条(高危 1)」, 因为「已恢复」只写进各脚本自己的 state 文件, 消费端无从知晓。
func TestQualityAlertResolvedClears(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	t1 := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+t0+`","sev":"warn","metric":"fractal_guard","key":"structure","value":1,"threshold":0,"msg":"新增违规 1 条"}`,
		`{"ts":"`+t1+`","sev":"info","resolved":true,"metric":"fractal_guard","key":"structure","value":0,"threshold":0,"msg":"已恢复"}`,
	)
	if as := readQualityAlerts(dir, 24*time.Hour); len(as) != 0 {
		t.Fatalf("已恢复的告警不该再计入, 得 %d 条: %+v", len(as), as)
	}
	if line := qualityAlertBannerLine(dir); line != "" {
		t.Fatalf("已恢复后横幅应为空, 得 %q", line)
	}
}

// 恢复之后又复发 -> 必须重新报警 (恢复标记不是永久免报金牌)。
func TestQualityAlertResolvedThenRecur(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-3 * time.Hour).Format(time.RFC3339)
	t1 := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	t2 := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+t0+`","sev":"warn","metric":"fractal_guard","key":"structure","msg":"违规"}`,
		`{"ts":"`+t1+`","sev":"info","resolved":true,"metric":"fractal_guard","key":"structure","msg":"已恢复"}`,
		`{"ts":"`+t2+`","sev":"warn","metric":"fractal_guard","key":"structure","msg":"复发"}`,
	)
	as := readQualityAlerts(dir, 24*time.Hour)
	if len(as) != 1 || as[0].Msg != "复发" {
		t.Fatalf("恢复后复发必须重新报警, 得 %d 条: %+v", len(as), as)
	}
}

// 普通 info(无 resolved 字段)不参与去重, 不得吞掉同键真告警 (原有语义未变)。
func TestQualityAlertPlainInfoDoesNotClear(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	t1 := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	writeAlerts(t, dir,
		`{"ts":"`+t0+`","sev":"warn","metric":"fractal_guard","key":"structure","msg":"违规"}`,
		`{"ts":"`+t1+`","sev":"info","metric":"fractal_guard","key":"structure","msg":"说明性 info"}`,
	)
	as := readQualityAlerts(dir, 24*time.Hour)
	if len(as) != 1 || as[0].Msg != "违规" {
		t.Fatalf("普通 info 不得清除同键告警, 得 %d 条: %+v", len(as), as)
	}
}

// 跨语言契约哨兵: Python 侧(alert_sink.py)写入的恢复标记必须与 Go 侧 json tag
// 对得上 —— 两套实现各改一半 = 恢复语义静默失效 (本仓库最高频缺陷形态)。
func TestAlertSinkResolvedKeyContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("defense_system", "alert_sink.py"))
	if err != nil {
		t.Fatalf("读 alert_sink.py 失败: %v", err)
	}
	src := string(raw)
	if !strings.Contains(src, "'resolved': True") {
		t.Fatal("alert_sink.py 未写 resolved 标记 —— Go 侧 Resolved 字段将永远为 false")
	}
	if !strings.Contains(src, "'sev': 'info'") {
		t.Fatal("alert_sink.py 恢复记录必须是 sev=info (普通 info 语义不得改变)")
	}
	var a QualityAlert
	if err := json.Unmarshal([]byte(`{"sev":"info","resolved":true,"metric":"m","key":"k"}`), &a); err != nil {
		t.Fatalf("恢复记录 JSON 解析失败: %v", err)
	}
	if !a.Resolved || a.Sev != "info" {
		t.Fatalf("resolved 字段未接上 json tag: %+v", a)
	}
}

// 接线哨兵: 两个巡检脚本必须把恢复语义接到共享实现上 ——
// 判据是「调用点存在」, 不是「文件里有这段文字」(模型声称/提交信息都不算证据)。
// 同时钉住 prefixes 参数: 漏传它, 流驱动(历史缺口自愈)静默失效, 而单测全绿。
func TestAlertSinkWiredBothScripts(t *testing.T) {
	cases := []struct {
		file   string
		prefix string
	}{
		{"forge_watchdog.py", "prefixes=WATCH_PREFIXES"},
		{"fractal_check.py", "prefixes=('fractal_',)"},
	}
	for _, c := range cases {
		raw, err := os.ReadFile(filepath.Join("defense_system", c.file))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", c.file, err)
		}
		src := string(raw)
		if !strings.Contains(src, "import alert_sink") {
			t.Fatalf("%s 未 import 共享告警出口", c.file)
		}
		if !strings.Contains(src, "alert_sink.emit(") {
			t.Fatalf("%s 未调用 alert_sink.emit (恢复语义断线)", c.file)
		}
		if !strings.Contains(src, c.prefix) {
			t.Fatalf("%s 未传 %s —— 流驱动恢复检测会静默失效", c.file, c.prefix)
		}
		// 不得回退成自己写盘: 直接 open(ALERTS, 'a') 是旧实现的指纹
		if strings.Contains(src, "open(ALERTS, 'a'") {
			t.Fatalf("%s 又自己写告警流了 —— 两套实现必然漂移", c.file)
		}
	}
}
