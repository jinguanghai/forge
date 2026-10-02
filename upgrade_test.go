package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildRestartScript(t *testing.T) {
	// 用 raw string 传路径: 旧版写 "C:\forge" 会让 \f 变成换页符, 源码里塞进裸控制字符。
	s := buildRestartScript(`C:\forge`, 12345, "20260805_213000")
	for _, want := range []string{
		`$wd = 'C:\forge'`,
		"Set-Location $wd",
		"AddSeconds(30)",                // 排空等待上限
		"Get-Process -Id 12345",         // 轮询主进程存活
		"Stop-Process -Id 12345 -Force", // 超时才强杀(兜底)
		"forge.exe.bak_20260805_213000",
		"Move-Item forge_new.exe forge.exe -Force",
		"forge_guard.py init",
		"Start-Process -FilePath 'forge.exe' -WorkingDirectory $wd",
		"upgrade_restart.log", // 失败留痕: 升级失败必须可见
		// 冒烟: 编译过 != 能跑, 冒烟失败必须回滚而不是把坏 exe 留在生产位
		`& .\forge.exe --version`,
		"$LASTEXITCODE",
		"rolling back",
		"Move-Item forge.exe.bak_20260805_213000 forge.exe -Force",
		// 备份失败 = 无回滚能力 → 拒绝替换(旧版是「备份失败(继续)」)
		"refuse to replace",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("脚本缺少: %s", want)
		}
	}
	// 排空式: 强杀必须出现在等待逻辑之后
	if strings.Index(s, "AddSeconds(30)") > strings.Index(s, "Stop-Process -Id 12345 -Force") {
		t.Errorf("脚本顺序错误: 强杀应在等待之后")
	}
	// 静默失败禁令: 全局 SilentlyContinue 会让「升级失败且程序没回来」无迹可查
	if strings.Contains(s, "$ErrorActionPreference = 'SilentlyContinue'") {
		t.Errorf("全局静默失败未移除: 升级失败将不可见")
	}
	if !strings.Contains(s, "$ErrorActionPreference = 'Continue'") {
		t.Errorf("缺少 $ErrorActionPreference = 'Continue'")
	}
	// 致命步骤必须 exit 1(而非静默继续)
	if !strings.Contains(s, "exit 1") {
		t.Errorf("致命失败路径缺少 exit 1")
	}
}

// TestRestartScriptPath 钉住重启脚本落点归属(2S 定置: 自动生成物必须有专属容器)。
// 根目录再出现 restart_self.ps1 = 归位失效; .forge-temp/ = 与启动清空机制(⑤)冲突。
func TestRestartScriptPath(t *testing.T) {
	work := `D:\forge`
	got := restartScriptPath(work)
	want := filepath.Join(work, ".forge", "restart_self.ps1")
	if got != want {
		t.Fatalf("restartScriptPath = %q, 期望 %q", got, want)
	}
	// 反向断言 1: 不得落在根目录(工作区)
	if filepath.Dir(got) == work {
		t.Errorf("重启脚本落在根目录(工作区): %q", got)
	}
	// 反向断言 2: 不得落在 .forge-temp(启动即清空)
	if strings.Contains(filepath.ToSlash(got), ".forge-temp") {
		t.Errorf("重启脚本落在 .forge-temp(启动即清空): %q", got)
	}
	// 单一来源: 产生点必须调本函数, 且不得残留写死根目录的路径拼接
	src := readRepoFile(t, "upgrade.go")
	if !strings.Contains(src, "scriptPath := restartScriptPath(workDir)") {
		t.Errorf("产生点未使用 restartScriptPath 单一来源")
	}
	if strings.Contains(src, `filepath.Join(workDir, "restart_self.ps1")`) {
		t.Errorf("upgrade.go 仍有写死根目录的路径拼接")
	}
	// 父目录必须能被 MkdirAll 建出(atomicWrite 不建父目录, 见 memory_store.go)
	if filepath.Base(filepath.Dir(got)) != ".forge" {
		t.Errorf("脚本父目录应为 .forge, 实得 %q", filepath.Base(filepath.Dir(got)))
	}
	// 接线钉住: MkdirAll 保护不得被删。此刻主进程已置 exitRequested, 脚本写不进去 =
	// 程序退出且不回来(用户看不到原因)。注: 这是静态断言 —— 无法在单测里真实触发升级流程,
	// 强度弱于运行验证, 故另配 TestRestartScriptPathWritable 动态验证该链路可行。
	if !strings.Contains(src, "os.MkdirAll(filepath.Dir(scriptPath)") {
		t.Errorf("upgrade.go 缺少 MkdirAll 保护(atomicWrite 不建父目录 → 脚本写入失败)")
	}
	// 重启链的日志产物必须在 .gitignore 内, 否则每次升级后 git status 变脏
	if !strings.Contains(readRepoFile(t, ".gitignore"), ".forge/upgrade_restart.log") {
		t.Errorf(".gitignore 缺少 .forge/upgrade_restart.log")
	}
}

// TestRestartScriptPathWritable 动态验证落点链路可行: 空 workDir(.forge/ 不存在)
// → MkdirAll → atomicWrite → 读回。防的是「落点改到新目录」与「atomicWrite 不建父目录」
// 组合出的静默失败。
func TestRestartScriptPathWritable(t *testing.T) {
	work := t.TempDir() // 空目录, 无 .forge/
	sp := restartScriptPath(work)
	if _, err := os.Stat(filepath.Dir(sp)); err == nil {
		t.Fatalf("前置条件不成立: %s 已存在", filepath.Dir(sp))
	}
	if err := os.MkdirAll(filepath.Dir(sp), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := atomicWrite(sp, []byte("probe")); err != nil {
		t.Fatalf("atomicWrite: %v", err)
	}
	got, err := os.ReadFile(sp)
	if err != nil || string(got) != "probe" {
		t.Fatalf("读回失败: err=%v got=%q", err, got)
	}
	if _, err := os.Stat(sp + ".tmp"); err == nil {
		t.Errorf("原子替换应无 .tmp 残留")
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
	exeContent := []byte("MZ...fake-exe-payload")
	os.WriteFile(filepath.Join(work, "forge.exe"), exeContent, 0644)

	if err := createCheckpoint(work, "20260812_120000", "test checkpoint"); err != nil {
		t.Fatalf("createCheckpoint: %v", err)
	}
	ckDir := filepath.Join(work, ".forge", "checkpoints")
	entries, _ := os.ReadDir(ckDir)
	if len(entries) != 1 {
		t.Fatalf("应创建1个快照目录, got %d", len(entries))
	}
	dst := filepath.Join(ckDir, entries[0].Name())
	// 源码/记忆: 落盘
	for _, want := range []string{"main.go", "memory.json", "gate_tcm_gate.go"} {
		if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
			t.Errorf("快照缺少: %s", want)
		}
	}
	// 二进制: 不落盘。旧断言把 "forge.exe 落盘" 当契约, 但那正是 33.96MB 冗余的根因
	// (11.32MB/份 x keep=3); 消费点实测 0 处, 回滚走 forge.exe.bak_* 与 git。
	if _, err := os.Stat(filepath.Join(dst, "forge.exe")); err == nil {
		t.Errorf("快照不应落盘 forge.exe (冗余 11MB/份) —— 指纹应记进 checkpoint.json")
	}
	// 但指纹必须留下, 否则"自改前跑的是哪个版本"永久丢失。
	mb, err := os.ReadFile(filepath.Join(dst, "checkpoint.json"))
	if err != nil {
		t.Fatalf("快照缺少 checkpoint.json: %v", err)
	}
	var meta checkpointMeta
	if err := json.Unmarshal(mb, &meta); err != nil {
		t.Fatalf("checkpoint.json 解析失败: %v", err)
	}
	if meta.Binary == nil {
		t.Fatal("checkpoint.json 缺 binary 指纹")
	}
	wantSum := sha256.Sum256(exeContent)
	if meta.Binary.SHA256 != hex.EncodeToString(wantSum[:]) {
		t.Errorf("指纹不符: got %s", meta.Binary.SHA256)
	}
	if meta.Binary.Size != int64(len(exeContent)) {
		t.Errorf("体积不符: got %d want %d", meta.Binary.Size, len(exeContent))
	}
	if meta.Ts != "20260812_120000" || meta.Reason != "test checkpoint" {
		t.Errorf("元数据不符: %+v", meta)
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
