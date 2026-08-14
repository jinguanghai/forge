package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildRestartScript(t *testing.T) {
	s := buildRestartScript(`C:orge`, 12345, "20260805_213000")
	for _, want := range []string{
		"Set-Location 'C:\forge'",
		"AddSeconds(10)",                // 排空等待上限
		"Get-Process -Id 12345",         // 轮询主进程存活
		"Stop-Process -Id 12345 -Force", // 超时才强杀(兜底)
		"forge.exe.bak_20260805_213000",
		"Move-Item forge_new.exe forge.exe -Force",
		"forge_guard.py init",
		"Start-Process -FilePath 'forge.exe' -WorkingDirectory 'C:\forge'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("脚本缺少: %s", want)
		}
	}
	// 排空式: 强杀必须出现在等待逻辑之后
	if strings.Index(s, "AddSeconds(10)") > strings.Index(s, "Stop-Process -Id 12345 -Force") {
		t.Errorf("脚本顺序错误: 强杀应在等待之后")
	}
}

func TestUpgradeTailLines(t *testing.T) {
	got := upgradeTailLines("a\nb\n\n\nc\nd\ne\nf\ng", 3)
	if len(got) != 3 || got[0] != "e" || got[2] != "g" {
		t.Errorf("upgradeTailLines = %v, 期望最后3行 [e f g]", got)
	}
	got = upgradeTailLines("", 3)
	if len(got) != 0 {
		t.Errorf("空输入应返回空, got %v", got)
	}
}

func TestPruneExeBackups(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"forge.exe.bak_20260805_100000",
		"forge.exe.bak_20260805_110000",
		"forge.exe.bak_20260805_120000",
		"forge.exe.bak_20260805_130000",
	} {
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644)
	}
	pruneExeBackups(dir, 2)
	left, _ := filepath.Glob(filepath.Join(dir, "forge.exe.bak_*"))
	if len(left) != 2 {
		t.Fatalf("应剩2个备份, got %d", len(left))
	}
	// 应保留最新的两个 (字典序最后)
	for _, want := range []string{"forge.exe.bak_20260805_120000", "forge.exe.bak_20260805_130000"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("应保留 %s", want)
		}
	}
}


// ─── I-3 快照测试 ───
func TestCreateCheckpoint(t *testing.T) {
	work := t.TempDir()
	os.MkdirAll(filepath.Join(work, ".forge", "forge-tools"), 0755)
	os.WriteFile(filepath.Join(work, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(work, "memory.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(work, ".forge", "forge-tools", "tcm_gate.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(work, "forge.exe"), []byte("MZ..."), 0644)

	if err := createCheckpoint(work, "20260812_120000", "test checkpoint"); err != nil {
		t.Fatalf("createCheckpoint: %v", err)
	}
	ckDir := filepath.Join(work, ".forge", "checkpoints")
	entries, _ := os.ReadDir(ckDir)
	if len(entries) != 1 {
		t.Fatalf("应创建1个快照目录, got %d", len(entries))
	}
	dst := filepath.Join(ckDir, entries[0].Name())
	for _, want := range []string{"main.go", "memory.json", "gate_tcm_gate.go", "forge.exe"} {
		if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
			t.Errorf("快照缺少: %s", want)
		}
	}
}

func TestPruneCheckpoints(t *testing.T) {
	work := t.TempDir()
	ck := filepath.Join(work, ".forge", "checkpoints")
	for i := 1; i <= 12; i++ {
		d := filepath.Join(ck, fmt.Sprintf("20260812_%06d_upgrade", i))
		os.MkdirAll(d, 0755)
		os.Chtimes(d, time.Now(), time.Now().Add(time.Duration(i)*time.Minute))
	}
	pruneCheckpoints(work, 10)
	entries, _ := os.ReadDir(ck)
	if len(entries) != 10 {
		t.Fatalf("应留10个快照, got %d", len(entries))
	}
}

// ─── I-1 审计测试 ───
func TestUpgradeAudit(t *testing.T) {
	work := t.TempDir()
	if err := writeUpgradeAudit(work, "20260812_130000", "test audit"); err != nil {
		t.Fatalf("writeUpgradeAudit: %v", err)
	}
	a, path := latestAudit(work)
	if a == nil || a.Status != "ready" || a.Reason != "test audit" {
		t.Fatalf("latestAudit 异常: %+v", a)
	}
	markAuditDone(path)
	a2, _ := latestAudit(work)
	if a2 == nil || a2.Status != "done" {
		t.Fatalf("markAuditDone 未生效: %+v", a2)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Errorf("原子替换应无 tmp 残留")
	}
}

func TestSanitizeReason(t *testing.T) {
	got := sanitizeReason(`abc 123!@#$ %^&*() 中文`)
	if !strings.HasPrefix(got, "abc_123_") {
		t.Errorf("sanitizeReason = %q, 应保留 abc_123 前缀", got)
	}
	if strings.ContainsAny(got, "!@#$%^&*() ") {
		t.Errorf("sanitizeReason 未清理非法字符: %q", got)
	}
	if got := sanitizeReason(""); got == "" {
		t.Errorf("空 reason 应有兜底值")
	}
	if got := sanitizeReason("这是一段很长的中文原因名称用于测试长度截断行为是否正确显示"); len(got) > 40 {
		t.Errorf("sanitizeReason 应截断到40: %d", len(got))
	}
}
