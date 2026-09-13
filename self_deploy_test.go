package main

// self_deploy_test.go — self gate 部署阶段(冒烟→原子就位→失败回滚)的哨兵。
//
// 这些用例守护的是一条具体教训: 「编译成功」曾被当成「部署完成」,
// 于是坏二进制能直接顶替生产 exe, 且 events.jsonl 里 5 天零条部署记录。
// 现在这条链路由死程序判定(公理四): 冒烟不过 → 不替换。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSmokeVerdict(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		stdout   string
		runErr   error
		wantOK   bool
		wantWord string // 判定理由需包含的关键字(便于定位失败原因)
	}{
		{"正常版本输出", 0, "铸剑炉 v3.0.0 — 流式智能体 · 编译器沙箱\n", nil, true, "ok"},
		{"退出码非零", 3, "铸剑炉 v3.0.0\n", nil, false, "退出码"},
		{"无法启动", -1, "", errors.New("fork/exec: 拒绝访问"), false, "无法启动"},
		{"退出码 0 但无版本标识", 0, "panic: nil map\n", nil, false, "版本标识"},
		{"退出码 0 但输出为空", 0, "", nil, false, "版本标识"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, why := smokeVerdict(c.code, c.stdout, c.runErr)
			if ok != c.wantOK {
				t.Fatalf("smokeVerdict = %v, want %v (理由: %s)", ok, c.wantOK, why)
			}
			if !strings.Contains(why, c.wantWord) {
				t.Fatalf("理由 %q 不含关键字 %q", why, c.wantWord)
			}
		})
	}
}

func TestHeadRunes(t *testing.T) {
	if got := headRunes("铸剑炉", 10); got != "铸剑炉" {
		t.Fatalf("短串不应截断: %q", got)
	}
	got := headRunes("铸剑炉编译器沙箱", 3)
	if []rune(got)[0] != '铸' || !strings.HasSuffix(got, "...") {
		t.Fatalf("截断结果异常(需按 rune 而非字节): %q", got)
	}
	if n := len([]rune(strings.TrimSuffix(got, "..."))); n != 3 {
		t.Fatalf("应保留 3 个 rune, 实得 %d (%q)", n, got)
	}
}

// writeFake 造一个内容可辨认的假 exe(无需真能运行, 冒烟由注入的 runner 决定)。
func writeFake(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
}

func readFake(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	return string(b)
}

func okSmoke(string) (int, string, error) {
	return 0, "铸剑炉 v3.0.0 — 流式智能体 · 编译器沙箱\n", nil
}

// 场景A: 冒烟通过 → 备份旧 exe → 新 exe 就位 → 冒烟副本清理干净。
func TestDeploySelfExe_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "forge.exe")
	built := filepath.Join(dir, "forge_new.exe")
	writeFake(t, cur, "OLD-EXE")
	writeFake(t, built, "NEW-EXE")

	var sawPath string
	out, err := deploySelfExe(dir, built, 10, func(p string) (int, string, error) {
		sawPath = p
		return okSmoke("")
	})
	if err != nil {
		t.Fatalf("部署应成功: %v", err)
	}
	if !out.Deployed {
		t.Fatal("Deployed 应为 true")
	}
	if got := readFake(t, cur); got != "NEW-EXE" {
		t.Fatalf("forge.exe 未就位, 内容=%q", got)
	}
	if out.BackupPath == "" {
		t.Fatal("应留下备份路径")
	}
	if got := readFake(t, out.BackupPath); got != "OLD-EXE" {
		t.Fatalf("备份内容错: %q", got)
	}
	if out.SHA256 == "" {
		t.Fatal("应记录 sha256")
	}
	// 冒烟必须跑「中性名副本」而非产物本身 —— 否则会触发 runSelfReplace 绕过验证
	if filepath.Base(sawPath) == "forge_new.exe" || !strings.HasPrefix(filepath.Base(sawPath), "forge_smoke_") {
		t.Fatalf("冒烟应使用中性名副本, 实得 %s", filepath.Base(sawPath))
	}
	if _, err := os.Stat(sawPath); !os.IsNotExist(err) {
		t.Fatalf("冒烟副本未清理: %v", err)
	}
	if _, err := os.Stat(built); !os.IsNotExist(err) {
		t.Fatalf("产物应已被移走(rename), 但仍在: %v", err)
	}
}

// 场景B: 冒烟失败 → 绝不替换, 旧 exe 原样, 坏产物改名 .failed_*。
func TestDeploySelfExe_SmokeFailKeepsOldExe(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "forge.exe")
	built := filepath.Join(dir, "forge_new.exe")
	writeFake(t, cur, "OLD-EXE")
	writeFake(t, built, "BROKEN-EXE")

	out, err := deploySelfExe(dir, built, 10, func(string) (int, string, error) {
		return 3, "panic: boom", nil
	})
	if err == nil {
		t.Fatal("冒烟失败必须返回错误")
	}
	if out.Deployed {
		t.Fatal("冒烟失败不得标记为已部署")
	}
	if got := readFake(t, cur); got != "OLD-EXE" {
		t.Fatalf("旧 exe 被破坏: %q", got)
	}
	if !strings.Contains(out.FailedPath, ".failed_") {
		t.Fatalf("坏产物应改名 .failed_*, 实得 %q", out.FailedPath)
	}
	if got := readFake(t, out.FailedPath); got != "BROKEN-EXE" {
		t.Fatalf("坏产物内容错: %q", got)
	}
	// 不得留下任何备份(因为根本没进入替换阶段)
	baks, _ := filepath.Glob(filepath.Join(dir, "forge.exe.bak_*"))
	if len(baks) != 0 {
		t.Fatalf("冒烟失败不应产生备份: %v", baks)
	}
}

// 场景C: 就位阶段失败 → 自动回滚, forge.exe 仍是旧版本(可回滚性)。
// 构造手法: 冒烟回调里删掉产物 —— 于是备份已发生, 而 rename(产物→forge.exe) 必失败。
func TestDeploySelfExe_RollbackWhenDeployFails(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "forge.exe")
	built := filepath.Join(dir, "forge_new.exe")
	writeFake(t, cur, "OLD-EXE")
	writeFake(t, built, "NEW-EXE")

	out, err := deploySelfExe(dir, built, 10, func(string) (int, string, error) {
		os.Remove(built) // 就位前产物消失 → rename 失败
		return okSmoke("")
	})
	if err == nil {
		t.Fatal("就位失败必须返回错误")
	}
	if out.Deployed {
		t.Fatal("失败不得标记为已部署")
	}
	if got := readFake(t, cur); got != "OLD-EXE" {
		t.Fatalf("回滚未生效, forge.exe=%q", got)
	}
	if out.BackupPath != "" {
		t.Fatalf("回滚后不应残留备份指针: %q", out.BackupPath)
	}
	if !strings.Contains(err.Error(), "回滚") {
		t.Fatalf("错误信息应说明回滚结果: %v", err)
	}
}

// 场景D: 前置检查 —— 产物不存在 / 产物是目录, 都必须拒绝且不碰旧 exe。
func TestDeploySelfExe_Preflight(t *testing.T) {
	dir := t.TempDir()
	cur := filepath.Join(dir, "forge.exe")
	writeFake(t, cur, "OLD-EXE")

	if _, err := deploySelfExe(dir, filepath.Join(dir, "nope.exe"), 10, okSmoke); err == nil {
		t.Fatal("产物不存在应报错")
	}
	dirProd := filepath.Join(dir, "dirprod.exe")
	if err := os.Mkdir(dirProd, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := deploySelfExe(dir, dirProd, 10, okSmoke); err == nil {
		t.Fatal("产物是目录应报错")
	}
	if got := readFake(t, cur); got != "OLD-EXE" {
		t.Fatalf("前置检查失败时旧 exe 不得被改动: %q", got)
	}
}

// 场景E: 首次部署(目标不存在) → 无备份但就位成功。
func TestDeploySelfExe_FirstInstall(t *testing.T) {
	dir := t.TempDir()
	built := filepath.Join(dir, "forge_new.exe")
	writeFake(t, built, "FIRST-EXE")

	out, err := deploySelfExe(dir, built, 10, okSmoke)
	if err != nil {
		t.Fatalf("首次部署应成功: %v", err)
	}
	if out.BackupPath != "" {
		t.Fatalf("无旧 exe 时不应有备份: %q", out.BackupPath)
	}
	if got := readFake(t, filepath.Join(dir, "forge.exe")); got != "FIRST-EXE" {
		t.Fatalf("就位内容错: %q", got)
	}
}

// ─── 端到端: self gate 全链路(真 go build → 真冒烟 → 真就位) ──────────
//
// 默认跳过(要编译一整个包, 约 10-20s, 不适合每次 go test 都跑)。
// 跑法: set FORGE_SELF_E2E=1 && go test -run TestSelfGateE2E -v .
//
// 关键点: 全程在 t.TempDir() 沙箱里进行 —— selfHostedSelf 的 workDir 指向沙箱,
// 所以 go build / 备份 / 就位 / 冒烟全部落在沙箱, 绝不碰生产 forge.exe。
func TestSelfGateE2E_SandboxDeploy(t *testing.T) {
	if os.Getenv("FORGE_SELF_E2E") != "1" {
		t.Skip("set FORGE_SELF_E2E=1 to run the self-gate end-to-end deploy test")
	}
	prodDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sandbox := t.TempDir()
	for _, pat := range []string{"*.go", "go.mod", "go.sum"} {
		ms, _ := filepath.Glob(filepath.Join(prodDir, pat))
		for _, m := range ms {
			if cerr := copyFile(m, filepath.Join(sandbox, filepath.Base(m))); cerr != nil {
				t.Fatalf("复制 %s: %v", m, cerr)
			}
		}
	}
	// 沙箱里的「旧 forge.exe」用文本假文件: 就位/备份是纯 rename, 不需要真 exe;
	// 冒烟跑的是 go build 出来的真产物, 所以冒烟是真实判定。
	oldExe := filepath.Join(sandbox, "forge.exe")
	if werr := os.WriteFile(oldExe, []byte("OLD-EXE"), 0644); werr != nil {
		t.Fatal(werr)
	}

	f := &Forge{workDir: sandbox, ctx: context.Background(), sem: make(chan struct{}, 1)}
	// 无害改动: replace 成等价文本(只为触发「改源码 → 编译 → 部署」全链路)
	res := f.selfHostedSelf("", "replace:const selfSmokeTimeout = 15 * time.Second:const selfSmokeTimeout = 15 * time.Second // e2e", time.Now())
	if !res.OK {
		t.Fatalf("self gate 应成功, 实际失败: stage=%s err=%s", res.Stage, res.Error)
	}
	if !strings.Contains(res.Stdout, "已就位") {
		t.Fatalf("输出应报告已就位, 实得: %s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "冒烟通过") {
		t.Fatalf("输出应报告冒烟通过, 实得: %s", res.Stdout)
	}
	// ① 新 exe 就位(不再是假的 OLD-EXE)
	got, rerr := os.ReadFile(oldExe)
	if rerr != nil {
		t.Fatalf("就位后 forge.exe 不可读: %v", rerr)
	}
	if string(got) == "OLD-EXE" {
		t.Fatal("forge.exe 未被替换(仍是旧的假文件)")
	}
	if len(got) < 1<<20 {
		t.Fatalf("就位的 exe 太小(%d 字节), 不像真二进制", len(got))
	}
	// ② 备份保留了旧文件
	baks, _ := filepath.Glob(filepath.Join(sandbox, "forge.exe.bak_*"))
	if len(baks) != 1 {
		t.Fatalf("应恰好留 1 个备份, 实得 %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); string(b) != "OLD-EXE" {
		t.Fatalf("备份内容错: %q", string(b))
	}
	// ③ 中间产物清理干净: 无 forge_new.exe / forge_smoke_*.exe / *.stale_*
	for _, pat := range []string{"forge_new.exe", "forge_smoke_*.exe", "*.stale_*", "*.failed_*"} {
		left, _ := filepath.Glob(filepath.Join(sandbox, pat))
		if len(left) > 0 {
			t.Fatalf("中间产物未清理 (%s): %v", pat, left)
		}
	}
	// ④ 就位的二进制真能跑(--version 自证)
	code, out, rerr := realSmokeRunner(oldExe)
	if ok, why := smokeVerdict(code, out, rerr); !ok {
		t.Fatalf("就位的 exe 冒烟失败: %s (输出 %q)", why, headRunes(out, 80))
	}
	t.Logf("端到端通过: %s", strings.Split(res.Stdout, "\n")[1])
}
