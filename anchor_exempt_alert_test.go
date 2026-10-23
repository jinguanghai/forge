package main

// anchor_exempt_alert_test.go — 豁免可见化与拒绝留痕的判据 (T29-A / T29-B)
//
// 隔离: 全部在 t.TempDir() 里造审计与告警文件 —— 不碰生产 anchor_audit.jsonl
// 与 .forge/alerts.jsonl (生产文件带 event 记录数, 污染即失真)。

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// writeAudit 直接造审计条目 (绕过 SaveMemory, 只测统计与告警臂)。
func writeAudit(t *testing.T, wd string, e AnchorAuditEntry) {
	t.Helper()
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}
	line, _ := json.Marshal(e)
	f, err := os.OpenFile(anchorAuditPath(wd), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("造审计: %v", err)
	}
	defer f.Close()
	_, _ = f.WriteString(string(line) + "\n")
}

// 拒绝留痕不得占配额, 也不得算进豁免数 (否则拒绝会把护栏自己顶穿)。
func TestAnchorGuardRejectAuditNotCounted(t *testing.T) {
	wd := t.TempDir()
	writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}, Reason: "第一次改动"})
	anchorGuardRejectAudit(wd, 1, 1)
	anchorGuardRejectAudit(wd, 1, 1)

	total, counted, err := anchorAuditTodayStats(wd)
	if err != nil {
		t.Fatalf("统计: %v", err)
	}
	if total != 1 || counted != 1 {
		t.Fatalf("total=%d counted=%d, want 1/1 (拒绝留痕被算进配额或豁免)", total, counted)
	}
	n, err := anchorAuditTodayCount(wd)
	if err != nil || n != 1 {
		t.Fatalf("anchorAuditTodayCount = %d (err=%v), want 1", n, err)
	}
	// 拒绝必须留痕且可辨认
	raw, _ := os.ReadFile(anchorAuditPath(wd))
	if c := strings.Count(string(raw), `"rejected":true`); c != 2 {
		t.Fatalf("拒绝留痕 %d 条, want 2: %s", c, string(raw))
	}
	if !strings.Contains(string(raw), "quota_rejected") {
		t.Fatalf("拒绝留痕缺原因: %s", string(raw))
	}
}

// 拒绝后再写入: 拒绝不占配额 → 护栏仍按「今日已写 1 次」拦截第 2 次。
func TestAnchorGuardRejectDoesNotConsumeQuota(t *testing.T) {
	wd := t.TempDir()
	writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}})
	anchorGuardRejectAudit(wd, 1, 1)
	if err := anchorGuardCheck(wd); err == nil {
		t.Fatal("今日已写 1 次, 第 2 次应被拦 (拒绝留痕不得放行写入)")
	}
}

// 豁免未超阈值: 不写告警。
func TestSyncAnchorExemptAlertBelowThresholdSilent(t *testing.T) {
	wd := t.TempDir()
	writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}})
	writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}, GuardOff: true})
	syncAnchorExemptAlert(wd)
	if as := readQualityAlerts(wd, qualityAlertWindow); len(as) != 0 {
		t.Fatalf("豁免 1 次 (<阈值 3) 不应告警: %v", as)
	}
}

// 豁免达阈值: 必出 1 条 warn, 且被启动横幅消费者看得见。
func TestSyncAnchorExemptAlertAtThresholdWarns(t *testing.T) {
	wd := t.TempDir()
	writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}})
	for i := 0; i < anchorExemptWarnThreshold; i++ {
		writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}, GuardOff: true})
	}
	syncAnchorExemptAlert(wd)
	as := readQualityAlerts(wd, qualityAlertWindow)
	if len(as) != 1 || as[0].Metric != anchorExemptMetric || as[0].Sev != "warn" {
		t.Fatalf("阈值告警缺失或形态错: %v", as)
	}
	if !strings.Contains(as[0].Msg, "豁免") {
		t.Fatalf("告警文本须含「豁免」: %s", as[0].Msg)
	}
	if line := qualityAlertBannerLine(wd); line == "" {
		t.Fatal("横幅应显示该告警 (接告警出口的意义即在此)")
	}
	// 重复调用不刷屏
	syncAnchorExemptAlert(wd)
	if as := readQualityAlerts(wd, qualityAlertWindow); len(as) != 1 {
		t.Fatalf("重复调用应去重, 实得 %d 条", len(as))
	}
}

// 豁免回落到阈值下: 写恢复水位, 消费者不再报 (修好了还喊 = 训练使用者忽略告警)。
func TestSyncAnchorExemptAlertResolvesWhenBackBelowThreshold(t *testing.T) {
	wd := t.TempDir()
	for i := 0; i < anchorExemptWarnThreshold; i++ {
		writeAudit(t, wd, AnchorAuditEntry{File: "memory.json", Fields: []string{"axioms"}, GuardOff: true})
	}
	syncAnchorExemptAlert(wd)
	if as := readQualityAlerts(wd, qualityAlertWindow); len(as) != 1 {
		t.Fatalf("前置条件失败: 应已告警, 实得 %v", as)
	}
	// 换到空目录模拟"次日回落"? 不行 —— 回落靠同一份审计里豁免数下降。
	// 真实回落形态 = 审计被轮转/条目变少, 此处直接改写文件为「只有 1 条普通改动」。
	if err := os.WriteFile(anchorAuditPath(wd), []byte(
		`{"time":"`+time.Now().Format(time.RFC3339)+`","file":"memory.json","fields":["axioms"]}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	syncAnchorExemptAlert(wd)
	if as := readQualityAlerts(wd, qualityAlertWindow); len(as) != 0 {
		t.Fatalf("回落应写 resolved 压掉告警, 实得 %v", as)
	}
}
