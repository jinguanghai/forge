package main

// quality_alert_write_test.go — 告警写入臂的判据 (T29-A)
//
// 隔离: 全部用 t.TempDir() 作 workDir, 告警落 temp/.forge/alerts.jsonl ——
// 绝不碰生产 .forge/alerts.jsonl (会污染真实告警流与横幅)。

import (
	"os"
	"strings"
	"testing"
)

func alertLines(t *testing.T, wd string) []string {
	t.Helper()
	raw, err := os.ReadFile(qualityAlertPath(wd))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("读告警文件: %v", err)
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestEmitQualityAlertWritesOnceThenDedups(t *testing.T) {
	wd := t.TempDir()
	if !EmitQualityAlert(wd, "anchor_exempt", "daily", "warn", 4, 3, "豁免 4 次") {
		t.Fatal("首次应写入")
	}
	if EmitQualityAlert(wd, "anchor_exempt", "daily", "warn", 4, 3, "豁免 4 次") {
		t.Fatal("去重窗口内同键不应重复写")
	}
	if n := len(alertLines(t, wd)); n != 1 {
		t.Fatalf("行数 = %d, want 1 (去重失效)", n)
	}
	// 消费者 (quality_alerts.go) 必须看得见这条告警
	as := readQualityAlerts(wd, qualityAlertWindow)
	if len(as) != 1 || as[0].Metric != "anchor_exempt" {
		t.Fatalf("消费者读数 = %v, want 1 条 anchor_exempt", as)
	}
}

func TestResolveQualityAlertWritesWatermarkIdempotently(t *testing.T) {
	wd := t.TempDir()
	EmitQualityAlert(wd, "anchor_exempt", "daily", "warn", 4, 3, "豁免 4 次")
	if !ResolveQualityAlert(wd, "anchor_exempt", "daily") {
		t.Fatal("在报状态下应写恢复水位")
	}
	if ResolveQualityAlert(wd, "anchor_exempt", "daily") {
		t.Fatal("已恢复后再写 = 重复噪音, 应幂等返回 false")
	}
	ls := alertLines(t, wd)
	if len(ls) != 2 {
		t.Fatalf("行数 = %d, want 2", len(ls))
	}
	if !strings.Contains(ls[1], `"resolved":true`) {
		t.Fatalf("第二行缺 resolved 标记: %s", ls[1])
	}
	// 恢复水位必须压掉旧告警 (消费者看不到活跃告警)
	if as := readQualityAlerts(wd, qualityAlertWindow); len(as) != 0 {
		t.Fatalf("恢复后仍报 %d 条: %v", len(as), as)
	}
}

func TestResolveWithoutPriorAlertIsNoop(t *testing.T) {
	wd := t.TempDir()
	if ResolveQualityAlert(wd, "anchor_exempt", "daily") {
		t.Fatal("无在报告警时写恢复 = 凭空的 info 行, 应拒绝")
	}
	if n := len(alertLines(t, wd)); n != 0 {
		t.Fatalf("行数 = %d, want 0", n)
	}
}

func TestEmitQualityAlertAfterResolveWarnsAgain(t *testing.T) {
	wd := t.TempDir()
	EmitQualityAlert(wd, "anchor_exempt", "daily", "warn", 4, 3, "豁免 4 次")
	ResolveQualityAlert(wd, "anchor_exempt", "daily")
	if !EmitQualityAlert(wd, "anchor_exempt", "daily", "warn", 5, 3, "豁免 5 次") {
		t.Fatal("恢复后再次超线必须能重新报 (否则告警只喊一次, 二次复发静默)")
	}
	if as := readQualityAlerts(wd, qualityAlertWindow); len(as) != 1 {
		t.Fatalf("消费者读数 = %d, want 1", len(as))
	}
}

// 前缀隔离: 本臂只碰自己的 metric|key, 不清除其它脚本的告警。
func TestSyncQualityAlertDoesNotTouchForeignKeys(t *testing.T) {
	wd := t.TempDir()
	EmitQualityAlert(wd, "gate_fail_rate", "python", "high", 0.9, 0.5, "失败率超线")
	SyncQualityAlert(wd, "anchor_exempt", "daily", "warn", 0, 3, "豁免 0 次", false)
	as := readQualityAlerts(wd, qualityAlertWindow)
	if len(as) != 1 || as[0].Metric != "gate_fail_rate" {
		t.Fatalf("外来告警被本臂动过: %v", as)
	}
}
