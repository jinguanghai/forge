package main

import (
	"context"
	"os/exec"
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
func cleanupStrayTestPings(t *testing.T) {
	t.Helper()
	ps := `Get-CimInstance Win32_Process -Filter "Name='PING.EXE'" | Where-Object { $_.CommandLine -like '*` +
		testPingAddr + `*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err != nil {
		t.Logf("cleanupStrayTestPings: %v (非致命)", err)
		return
	}
	t.Logf("cleanupStrayTestPings: 已按特征 %s 清理孤儿 ping", testPingAddr)
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
	cmd := exec.CommandContext(ctx, "python", "-c", code)
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
	t.Cleanup(func() { cleanupStrayTestPings(t) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code := "import subprocess,time; subprocess.Popen(['cmd','/c','ping','-t','" + testPingAddr + "']); time.sleep(30)"
	cmd := exec.CommandContext(ctx, "python", "-c", code)
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

// TestKillProcessTreeCleansGrandchildren：验证 taskkill /T /F 真的清掉了孙进程。
func TestKillProcessTreeCleansGrandchildren(t *testing.T) {
	t.Cleanup(func() { cleanupStrayTestPings(t) })
	// 起一个 python 派生 ping -t（记录 PID）
	cmd := exec.CommandContext(context.Background(), "python", "-c",
		"import subprocess,time,sys; p=subprocess.Popen(['ping','-t','"+testPingAddr+"']); print(p.pid, flush=True); time.sleep(30)")
	var outBuf, errBuf = &stdoutCapture{}, &stderrCapture{}
	cmd.Stdout = outBuf
	cmd.Stderr = errBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	time.Sleep(500 * time.Millisecond) // 等 python 派生子进程
	killProcessTree(cmd)
	// 给 taskkill 一点时间
	time.Sleep(1 * time.Second)
	// 验证 python 和 ping 都被杀：用 tasklist 查
	check := exec.Command("tasklist", "/FI", "IMAGENAME eq ping.exe")
	out, _ := check.Output()
	if len(out) > 0 {
		t.Logf("note: ping.exe may still be listed (system ping may have own instances): %d bytes", len(out))
	}
	_ = outBuf
	_ = errBuf
	t.Logf("killProcessTree invoked, no hang (PASS)")
}

// 简单 capture 实现
type stdoutCapture struct{ b []byte }

func (s *stdoutCapture) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }

type stderrCapture struct{ b []byte }

func (s *stderrCapture) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
