package main

// forge_self_deploy_test.go — self gate 部署阶段(冒烟→原子就位→回滚)的哨兵。
//
// 这些用例守护一条具体教训: 「编译成功」曾被当成「部署完成」, 于是坏二进制
// 能直接顶替生产 exe。现在这条链路由死程序判定(公理四): 冒烟不过 → 不替换。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForgeSelfDeploy_SmokeVerdict(t *testing.T) {
	// 退出码非 0 -> 不通过
	if ok, why := smokeVerdict(1, "", nil); ok || why == "" {
		t.Errorf("退出码 1 应判定不通过, 得到 (%v,%q)", ok, why)
	}
	// 输出无版本标识 -> 不通过(二进制可能根本没跑起来)
	if ok, _ := smokeVerdict(0, "hello", nil); ok {
		t.Error("无版本标识的输出不应判定通过")
	}
	// 含铸剑炉版本标识 -> 通过
	if ok, why := smokeVerdict(0, "铸剑炉 v3.0.0", nil); !ok {
		t.Errorf("含版本标识应通过, 得到 why=%q", why)
	}
}

func TestForgeSelfDeploy_HeadRunes(t *testing.T) {
	if got := headRunes("abcde", 3); got != "abc..." {
		t.Errorf("headRunes(\"abcde\",3) = %q, 期望 \"abc...\"", got)
	}
	if got := headRunes("abc", 5); got != "abc" {
		t.Errorf("超短串不应加省略号, 得到 %q", got)
	}
}

func TestForgeSelfDeploy_MissingArtifact(t *testing.T) {
	dir := t.TempDir()
	out, err := deploySelfExe(dir, filepath.Join(dir, "nope.exe"), 3, nil)
	if err == nil {
		t.Fatal("产物不存在应返回错误")
	}
	if out.Deployed {
		t.Error("产物不存在不得标记为已部署")
	}
}

func TestForgeSelfDeploy_ArtifactIsDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := deploySelfExe(dir, sub, 3, nil)
	if err == nil {
		t.Fatal("产物是目录应返回错误")
	}
	if out.Deployed {
		t.Error("目录不得标记为已部署")
	}
}

// TestForgeSelfDeploy_SmokeFailKeepsOldExe 是本文件最重要的一条:
// 冒烟不过时必须"旧 exe 原样 + 坏产物改名", 绝不能出现"坏二进制顶替生产 exe"。
func TestForgeSelfDeploy_SmokeFailKeepsOldExe(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "forge.exe")
	if err := os.WriteFile(old, []byte("OLD"), 0644); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(dir, "forge_new.exe")
	if err := os.WriteFile(built, []byte("NEW"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := deploySelfExe(dir, built, 3, func(string) (int, string, error) { return 1, "", nil })
	if err == nil {
		t.Fatal("冒烟失败应返回错误")
	}
	if out.Deployed {
		t.Error("冒烟失败不得部署")
	}
	if b, _ := os.ReadFile(old); string(b) != "OLD" {
		t.Errorf("旧 exe 被改动: %q", b)
	}
	if _, serr := os.Stat(built); serr == nil {
		t.Error("失败产物必须改名移走(不得留在原路径冒充新版本)")
	}
	if out.FailedPath == "" {
		t.Error("失败产物路径应记录在 FailedPath 中(供排查)")
	}
}

func TestForgeSelfDeploy_SmokePassDeploysWithBackup(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "forge.exe")
	if err := os.WriteFile(old, []byte("OLD"), 0644); err != nil {
		t.Fatal(err)
	}
	built := filepath.Join(dir, "forge_new.exe")
	if err := os.WriteFile(built, []byte("NEW"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := deploySelfExe(dir, built, 3, func(string) (int, string, error) { return 0, "铸剑炉 v3", nil })
	if err != nil {
		t.Fatalf("冒烟通过却失败: %v", err)
	}
	if !out.Deployed {
		t.Error("冒烟通过应标记已部署")
	}
	if b, _ := os.ReadFile(old); string(b) != "NEW" {
		t.Errorf("生产 exe 未被替换: %q", b)
	}
	if out.BackupPath == "" {
		t.Error("替换旧 exe 前必须留备份")
	} else if b, _ := os.ReadFile(out.BackupPath); string(b) != "OLD" {
		t.Errorf("备份内容 = %q, 期望 \"OLD\"", b)
	}
	if out.SHA256 == "" {
		t.Error("部署结果应带产物 SHA256(供比对)")
	}
}

func TestForgeSelfDeploy_PruneBackups(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// 备份目录 = selfBackupDir(workDir), 与产生点共用同一函数。
	// 2S 归位(20260928)后不再是 src 所在目录 —— 轮转扫的目录必须跟着产生点走,
	// 否则「备份落 A、轮转扫 B」: 备份无界增长而轮转静默空转。
	bdir := selfBackupDir(dir)
	if err := os.MkdirAll(bdir, 0755); err != nil {
		t.Fatal(err)
	}
	names := []string{
		"forge.go.bak_self_20260101_000000.000000000",
		"forge.go.bak_self_20260102_000000.000000000",
		"forge.go.bak_self_20260103_000000.000000000",
		"forge.go.bak_self_20260104_000000.000000000",
		"forge.go.bak_self_20260105_000000.000000000",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(bdir, n), []byte("b"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// 另一源文件的备份: 归位后所有源文件的备份共处同一目录, prune 必须只清
	// src 对应的那一组 —— 用通配符("*.bak_self_*")会误删其他源文件的可回滚备份,
	// 而这一格在「备份各在各的目录」时代不存在(归位新引入的风险)。
	other := "agent.go.bak_self_20260101_000000.000000000"
	if err := os.WriteFile(filepath.Join(bdir, other), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	pruneSelfBackups(bdir, src, 2)
	left, _ := filepath.Glob(filepath.Join(bdir, "forge.go.bak_self_*"))
	if len(left) != 2 {
		t.Fatalf("清理后剩 %d 个备份, 期望 2", len(left))
	}
	if _, err := os.Stat(filepath.Join(bdir, other)); err != nil {
		t.Errorf("其他源文件的备份被误删: %v", err)
	}
	// 必须保留【最新】两个(按时间戳字典序), 否则会删掉可用于回滚的新备份
	for _, want := range names[3:] {
		if _, err := os.Stat(filepath.Join(bdir, want)); err != nil {
			t.Errorf("最新备份 %s 被误删", want)
		}
	}
	// 未超阈值时不得删除
	pruneSelfBackups(bdir, src, 5)
	left2, _ := filepath.Glob(filepath.Join(bdir, "forge.go.bak_self_*"))
	if len(left2) != 2 {
		t.Errorf("keep 大于现有数量时不应再删, 剩 %d", len(left2))
	}
}

// TestSelfBackupSource_LandsInForgeBackups 哨兵(2S 归位, 20260928):
// self gate 源码备份必须落 .forge/backups/, 不得再写源文件旁(根目录)。
//
// 背景: 备份路径原为 srcPath+".bak_self_*", 而 srcPath 在根目录 —— self gate 每改一次
// 源码就在根目录丢一个 .bak, 是根目录垃圾的固定产地。归位后本断言钉住它不复发;
// 少了它, 路径被改回根目录也测不出来(改完照样绿)。
func TestSelfBackupSource_LandsInForgeBackups(t *testing.T) {
	wd := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = wd
	fg := NewForge(wd, cfg)
	defer fg.Shutdown()

	// 前置: 备份目录不预置, 由 selfBackupSource 自己建出(MkdirAll)
	if _, err := os.Stat(selfBackupDir(wd)); err == nil {
		t.Fatal("前置条件不成立: 备份目录本不该预先存在")
	}
	// 两个源文件名 —— self gate 的源文件已按职责拆成多个 .go, locateSelfSource 会
	// 定位到 agent.go / forge.go 等不同文件。备份名写死 "forge.go" 的实现在此暴露:
	// pruneSelfBackups 按 base(srcPath) 扫目录, 写死的名字扫不到 -> 轮转静默失效,
	// 备份无界增长(与「判据必须扫全包, 禁写死单文件」同一类缺陷)。
	for _, name := range []string{"forge.go", "agent.go"} {
		src := filepath.Join(wd, name)
		if err := os.WriteFile(src, []byte("package main\n\n// 归位探针\n"), 0644); err != nil {
			t.Fatal(err)
		}
		bp, _, failed := fg.selfBackupSource(src, time.Now())
		if failed {
			t.Fatalf("%s: 备份应成功", name)
		}
		if want := selfBackupDir(wd); filepath.Dir(bp) != want {
			t.Errorf("%s: 备份应落 %s, 实得 %s", name, want, filepath.Dir(bp))
		}
		if !strings.HasPrefix(filepath.Base(bp), name+".bak_self_") {
			t.Errorf("%s: 备份文件名应含源文件名, 实得 %s", name, filepath.Base(bp))
		}
	}
	// 正向: 目录被自动建出
	if _, err := os.Stat(selfBackupDir(wd)); err != nil {
		t.Errorf("备份目录应被自动创建: %v", err)
	}
	// 反向: 根目录不得残留 .bak_self_*
	if m, _ := filepath.Glob(filepath.Join(wd, "*.bak_self_*")); len(m) != 0 {
		t.Errorf("根目录残留 self gate 备份: %v", m)
	}
}
