//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testPingAddr 是本文件测试专用的 ping 目标地址。
// 用 127.0.0.99（而非 127.0.0.1）是为了让测试产生的孤儿进程具有唯一命令行
// 特征，cleanupStrayTestPings 才能精确匹配清理，绝不误杀用户的其它 ping。
const testPingAddr = "127.0.0.99"

// cleanupStrayTestPings 兜底清理本文件测试可能遗留的孤儿 ping 进程。
//
// 背景（2026-09-10）：runWithTimeout 的杀树依赖父进程仍存活（taskkill /T 按
// 父 PID 枚举子树），而 exec.CommandContext 会在 ctx 到期时抢先 Kill 直接子
// 进程，导致父进程消失、孙进程漏网。实测一次全量回归泄漏 34 个 PING.EXE +
// 17 个 cmd.exe，约 190MB 常驻。测试侧必须自带兜底清理。
// psFilter 是匹配本文件测试专用 ping 的 PowerShell 条件（按命令行特征）。
func psFilter() string {
	return `Get-CimInstance Win32_Process -Filter "Name='PING.EXE'" | Where-Object { $_.CommandLine -like '*` +
		testPingAddr + `*' }`
}

// runPSFile 以 -File 方式执行一段 PowerShell 脚本并返回 stdout。
//
// 为什么必须走 -File 而不是 -Command：实测把含引号的过滤器
// （Name='PING.EXE'）内联给 -Command 时，Windows CreateProcess 的参数拼接会
// 破坏引号，PowerShell 静默返回空串且 rc=0；Go 侧 Sscanf 得到 0，探针于是
// 永远报「无残留」—— 假阴性比缺陷本身更危险。2026-09-23 实测：
// 内联 -Command 返回 ” ，-File 返回正确计数。
func runPSFile(t *testing.T, body string) []byte {
	t.Helper()
	ps1 := filepath.Join(t.TempDir(), "q.ps1")
	if err := os.WriteFile(ps1, []byte(body+"\n"), 0644); err != nil {
		t.Fatalf("write ps1: %v", err)
	}
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", ps1)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("powershell -File failed: %v", err)
	}
	return out
}

// pingCountByPSFile 统计带 testPingAddr 特征的存活 ping 数量。
// 探针自身失效时直接 Fatal —— 绝不允许「探针坏了」被读成「没有残留」。
func pingCountByPSFile(t *testing.T) int {
	t.Helper()
	out := strings.TrimSpace(string(runPSFile(t, psFilter()+` | Measure-Object | Select-Object -ExpandProperty Count`)))
	n := -1
	fmt.Sscanf(out, "%d", &n)
	if n < 0 {
		t.Fatalf("探针失效：返回非数字 %q，本次判定不可信", out)
	}
	return n
}

// cleanupStrayTestPings 兜底清理本文件测试可能遗留的孤儿 ping 进程。
func cleanupStrayTestPings(t *testing.T) {
	t.Helper()
	_ = runPSFile(t, psFilter()+` | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`)
}

// TestRunWithTimeoutNormal：正常命令应快速返回。
func TestRunWithTimeoutNormal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd", "/c", "echo ok")
	start := time.Now()
	err := runWithTimeout(ctx, cmd)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("normal command should succeed, got: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("normal command took too long: %v", elapsed)
	}
	t.Logf("normal OK in %v", elapsed)
}

// TestRunWithTimeoutGrandchildHoldsPipe：孙进程持有 stdout 管道（旧缺陷场景）。
// python 起孙进程 ping -t（永不退出），python 自己 sleep。超时后 runWithTimeout
// 必须杀进程树并强制返回，绝不能挂死。
func TestRunWithTimeoutGrandchildHoldsPipe(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实子进程树测试(需taskkill杀树)")
	}
	t.Cleanup(func() { cleanupStrayTestPings(t) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// python 派生子进程 ping -t（继承 stdout 管道），python 自己睡 30 秒
	code := "import subprocess,time; subprocess.Popen(['ping','-t','" + testPingAddr + "'], stdout=subprocess.PIPE); time.sleep(30)"
	cmd := exec.Command("python", "-c", code)
	start := time.Now()
	err := runWithTimeout(ctx, cmd)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	// 超时错误必须是可判定的 context.DeadlineExceeded（retryGate 靠它决定不重试）
	if !isTimeoutErr(err) {
		t.Fatalf("timeout error should satisfy errors.Is(err, context.DeadlineExceeded), got: %v", err)
	}
	// 关键断言：总耗时必须在 ~8 秒内（超时3s + 清理2s + 余量），不能挂死
	if elapsed > 8*time.Second {
		t.Fatalf("HUNG: took %v, process tree kill failed to unblock Wait", elapsed)
	}
	t.Logf("timeout returned in %v with err=%v (PASS: no hang, timeout detectable)", elapsed, err)
}

// TestRunWithTimeoutShellPopen：模拟事故现场 os.popen("setx ...")。
// cmd /c 内嵌 python 起孙进程，验证杀树。
func TestRunWithTimeoutShellPopen(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实子进程树测试(需taskkill杀树)")
	}
	t.Cleanup(func() { cleanupStrayTestPings(t) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code := "import subprocess,time; subprocess.Popen(['cmd','/c','ping','-t','" + testPingAddr + "']); time.sleep(30)"
	cmd := exec.Command("python", "-c", code)
	start := time.Now()
	err := runWithTimeout(ctx, cmd)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if elapsed > 8*time.Second {
		t.Fatalf("HUNG: took %v", elapsed)
	}
	t.Logf("shell popen scenario returned in %v (PASS)", elapsed)
}

// TestKillProcessTreeCleansGrandchildren：断言 killProcessTree 真的清掉了孙进程。
//
// 这是「缺陷回归哨兵」：2026-09-23 发现原实现先 Kill 父进程再 taskkill /T，
// 而 /T 依赖父进程存活才能枚举子树 → 孙进程 100% 漏网（实测 3/3）。
// 原版测试只 t.Logf 不做断言，等于没测 —— 缺陷因此存活数周。
// 顺序写反、或探针失效，本测试都必须失败。
func TestKillProcessTreeCleansGrandchildren(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实子进程树测试(需taskkill杀树)")
	}
	t.Cleanup(func() { cleanupStrayTestPings(t) })
	cleanupStrayTestPings(t)
	time.Sleep(300 * time.Millisecond)

	// 用 exec.Command（不带 ctx）对齐修复后的产品路径（newCmd 已不用 CommandContext）
	cmd := exec.Command("python", "-c",
		"import subprocess,time; subprocess.Popen(['ping','-n','60','"+testPingAddr+"']); time.sleep(30)")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	time.Sleep(900 * time.Millisecond) // 等 python 派生出 ping 孙进程

	before := pingCountByPSFile(t)
	if before == 0 {
		t.Fatalf("前置探针无效：未观测到孙进程 ping，本次测试无判定力")
	}

	killProcessTree(cmd)
	time.Sleep(1200 * time.Millisecond)
	_, _ = cmd.Process.Wait()

	after := pingCountByPSFile(t)
	if after != 0 {
		t.Fatalf("孙进程漏网 %d 个：killProcessTree 必须先 taskkill /T 再 Kill 父进程（顺序反了 /T 枚举不到子树）", after)
	}
	t.Logf("孙进程清理验证 before=%d after=%d (PASS)", before, after)
}

// pingAliveByPID 用 tasklist 按 PID 精确判断某个 ping 是否存活。
func pingAliveByPID(pid string) bool {
	cmd := exec.Command("tasklist", "/FI", "PID eq "+pid, "/NH")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "ping.exe")
}

// TestNewCmdPathCleansGrandchildren 端到端哨兵：走产品真实入口 f.newCmd()
// 与 runWithTimeout 的组合，断言孙进程被清除。
//
// 为什么单测 killProcessTree 不够：若有人把 newCmd 改回 exec.CommandContext，
// Go 内部 watcher 会在 ctx 到期时抢先 Kill 父进程，使 killProcessTree 的
// taskkill /T 落空 —— 而 killProcessTree 自身实现完全正确，单测照样通过。
// 只有走 newCmd 的端到端断言能抓住这个回归点（2026-09-23 修复的另一半）。
func TestNewCmdPathCleansGrandchildren(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实子进程树测试(需taskkill杀树)")
	}
	t.Cleanup(func() { cleanupStrayTestPings(t) })
	cleanupStrayTestPings(t)
	time.Sleep(300 * time.Millisecond)

	pidFile := filepath.Join(t.TempDir(), "pingpid.txt")
	var f Forge
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code := "import subprocess,time; p=subprocess.Popen(['ping','-n','60','" + testPingAddr + "']); " +
		"open(r'" + pidFile + "','w').write(str(p.pid)); time.sleep(30)"
	cmd := f.newCmd(ctx, "python", "-c", code)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = runWithTimeout(ctx, cmd)
	time.Sleep(1200 * time.Millisecond)

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("读不到孙进程 pid（python 未派生出 ping），测试无效: %v", err)
	}
	pid := strings.TrimSpace(string(raw))
	if pid == "" {
		t.Fatalf("孙进程 pid 为空，测试无效")
	}
	if pingAliveByPID(pid) {
		t.Fatalf("产品路径漏网：孙进程 ping(pid=%s) 仍存活 —— newCmd 不得使用 exec.CommandContext", pid)
	}
	if n := pingCountByPSFile(t); n != 0 {
		t.Fatalf("产品路径漏网 %d 个特征 ping", n)
	}
	t.Logf("产品路径(newCmd+runWithTimeout)清理孙进程 pid=%s PASS", pid)
}

// 简单 capture 实现
type stdoutCapture struct{ b []byte }

func (s *stdoutCapture) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }

type stderrCapture struct{ b []byte }

func (s *stderrCapture) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
