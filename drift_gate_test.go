package main

// ============================================================================
// drift_gate_test.go —— 漂移维阈值元判据的接线哨兵 (J6/J7, 20261005)
//
// 病根 (实测): 漂移维阈值(对手=时间, 无意志)此前零元判据 —— 改阈值无人报红。
//   「抬闸门是最便宜的规避」: 判据都在, 缺的是「阈值本身被动过」这件事的判据。
//
// 本文件钉住三件事:
//   1. 真实基线一致 (未变松 / 未超限幅 / 无未登记阈值)
//   2. 鉴别力: 沙箱把某阈值改松 -> 必须报红; 改严(限幅内) -> 必须绿
//   3. 语义: 收紧自动 / 放松需审批 (由 --selftest 的 13 向量判定)
// ============================================================================

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const driftGatePy = "defense_system/drift_gate.py"
const driftBaselineFile = "defense_system/drift_baseline.json"
const driftLedgerFile = "defense_system/judgement_ledger.json"

func driftGateRun(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(guardGatePython(), append([]string{driftGatePy}, args...)...)
	cmd.Env = append(pythonUTF8Env(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestDriftGateRealWorkspace 真实基线必须与台账一致 (无变松/超限幅/未登记)。
func TestDriftGateRealWorkspace(t *testing.T) {
	out, err := driftGateRun(t, nil, "--check")
	if err != nil {
		t.Fatalf("漂移阈值与基线不一致: %v\n%s", err, out)
	}
	if !strings.Contains(out, "与基线一致") {
		t.Fatalf("--check 输出异常: %s", out)
	}
}

// TestDriftGateSelftest 鉴别力自检 (13 向量, 死程序判定)。
func TestDriftGateSelftest(t *testing.T) {
	out, err := driftGateRun(t, nil, "--selftest")
	if err != nil {
		t.Fatalf("--selftest 失败: %v\n%s", err, out)
	}
	if !strings.Contains(out, "13/13") || strings.Contains(out, "FAIL") {
		t.Fatalf("--selftest 输出异常: %s", out)
	}
}

// TestDriftGateCoverage 基线必须覆盖台账里每一条漂移阈值 (零盲区)。
func TestDriftGateCoverage(t *testing.T) {
	raw, err := os.ReadFile(driftBaselineFile)
	if err != nil {
		t.Fatalf("读基线失败: %v", err)
	}
	var b struct {
		MaxStepPct float64 `json:"max_step_pct"`
		Thresholds map[string]struct {
			Value float64 `json:"value"`
			Dir   string  `json:"dir"`
		} `json:"thresholds"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("解析基线失败: %v", err)
	}
	if b.MaxStepPct <= 0 || b.MaxStepPct > 1 {
		t.Fatalf("max_step_pct 非法: %v", b.MaxStepPct)
	}
	raw2, err := os.ReadFile(driftLedgerFile)
	if err != nil {
		t.Fatalf("读台账失败: %v", err)
	}
	var led struct {
		Entries []struct {
			ID        string `json:"id"`
			Dimension string `json:"dimension"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw2, &led); err != nil {
		t.Fatalf("解析台账失败: %v", err)
	}
	driftIDs := map[string]bool{}
	for _, e := range led.Entries {
		if e.Dimension == "drift" {
			driftIDs[e.ID] = true
		}
	}
	if len(driftIDs) == 0 {
		t.Fatal("台账里没有漂移维条目 —— 扫描失效")
	}
	for id := range driftIDs {
		rec, ok := b.Thresholds[id]
		if !ok {
			t.Errorf("漂移阈值未登记基线: %s", id)
			continue
		}
		if rec.Dir != "down" && rec.Dir != "up" {
			t.Errorf("方向语义非法: %s dir=%q", id, rec.Dir)
		}
	}
	for id := range b.Thresholds {
		if !driftIDs[id] {
			t.Errorf("基线僵尸(台账无此漂移条目): %s", id)
		}
	}
	t.Logf("漂移阈值基线覆盖 %d 条", len(driftIDs))
}

// TestDriftGateLoosenCaught 鉴别力: 沙箱改松 -> 报红; 改严(限幅内) -> 绿。
func TestDriftGateLoosenCaught(t *testing.T) {
	raw, err := os.ReadFile(driftBaselineFile)
	if err != nil {
		t.Fatalf("读基线失败: %v", err)
	}
	var b map[string]interface{}
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	th, ok := b["thresholds"].(map[string]interface{})
	if !ok {
		t.Fatal("thresholds 结构异常")
	}
	// 找一个 dir=down 的条目 (值变大 = 变松)
	var key string
	for k, v := range th {
		m := v.(map[string]interface{})
		if m["dir"] == "down" && m["value"].(float64) > 0 {
			key = k
			break
		}
	}
	if key == "" {
		t.Fatal("找不到 dir=down 的基线条目")
	}
	rec := th[key].(map[string]interface{})
	oldVal := rec["value"].(float64)

	dir := t.TempDir()
	writeCase := func(mult float64) string {
		t.Helper()
		rec["value"] = oldVal * mult
		b["thresholds"].(map[string]interface{})[key] = rec
		bs, _ := json.Marshal(b)
		fp := filepath.Join(dir, "baseline.json")
		if err := os.WriteFile(fp, bs, 0o644); err != nil {
			t.Fatalf("写沙箱基线失败: %v", err)
		}
		return fp
	}
	// 变松 20% -> 必须报红
	fp := writeCase(0.8) // 基线值调小 = 当前值相对更大 = 变松
	out, err := driftGateRun(t, []string{"FORGE_DRIFT_BASELINE=" + fp}, "--check")
	if err == nil {
		t.Fatalf("变松未被捕获 (判据无鉴别力): %s", out)
	}
	if !strings.Contains(out, "变松") {
		t.Fatalf("报红原因不是「变松」: %s", out)
	}
	// 变严 20% -> 必须绿 (限幅 50% 内)
	fp = writeCase(1.2)
	out, err = driftGateRun(t, []string{"FORGE_DRIFT_BASELINE=" + fp}, "--check")
	if err != nil {
		t.Fatalf("变严(限幅内)被误报: %v\n%s", err, out)
	}
}
