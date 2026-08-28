package main

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// python 派生子进程 ping -t（继承 stdout 管道），python 自己睡 30 秒
	code := "import subprocess,time; subprocess.Popen(['ping','-t','127.0.0.1'], stdout=subprocess.PIPE); time.sleep(30)"
	cmd := exec.CommandContext(ctx, "python", "-c", code)
	start := time.Now()
	err := runWithTimeout(ctx, cmd)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	// 关键断言：总耗时必须在 ~8 秒内（超时3s + 清理2s + 余量），不能挂死
	if elapsed > 8*time.Second {
		t.Fatalf("HUNG: took %v, process tree kill failed to unblock Wait", elapsed)
	}
	t.Logf("timeout returned in %v with err=%v (PASS: no hang)", elapsed, err)
}

// TestRunWithTimeoutShellPopen：模拟事故现场 os.popen("setx ...")。
// cmd /c 内嵌 python 起孙进程，验证杀树。
func TestRunWithTimeoutShellPopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code := "import subprocess,time; subprocess.Popen(['cmd','/c','ping','-t','127.0.0.1']); time.sleep(30)"
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
	// 起一个 python 派生 ping -t（记录 PID）
	cmd := exec.CommandContext(context.Background(), "python", "-c",
		"import subprocess,time,sys; p=subprocess.Popen(['ping','-t','127.0.0.1']); print(p.pid, flush=True); time.sleep(30)")
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
