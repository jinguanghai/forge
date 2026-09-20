package main

// quality_alerts_test.go — 质量告警出口的确定性验证 (死程序判定, 不靠肉眼)

import (
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
