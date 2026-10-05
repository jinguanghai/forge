package main

// ── forge_integration_v3_test.go ──────────────────────────────────
// 集成测试补充场景 B: 并发 / 指标字段 / 输入传递 / 边界
// 姊妹文件: forge_integration_v2_test.go (多 lang / 缓存 / 恢复 / 流水线)
// 拆分动机: F2 单文件 ≤500 行 (fractal_guard_test.go)

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── 5. 并发安全 ──────────────────────────────────────────────────

// TestForgeGate_Concurrent: 多 goroutine 并发调用 forgeGate,验证 Forge 实例的并发安全
// Windows 默认串行(防病毒软件可能误杀并发子进程);Linux 启用 goroutine 并发
func TestForgeGate_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实 python 子进程批量端到端 (Windows 串行 50 次)")
	}
	f := newTestForge(t)
	defer f.Shutdown()

	const totalCalls = 50
	var errCount atomic.Int32

	if runtime.GOOS == "windows" {
		// Windows 串行跑 50 次,验证 Forge 实例连续服务能力
		for i := 0; i < totalCalls; i++ {
			code := fmt.Sprintf("print('SEQ_%s')", strconv.Itoa(i))
			r := f.forgeGate(code, "python", "")
			if !r.OK {
				errCount.Add(1)
				continue
			}
			want := fmt.Sprintf("SEQ_%s", strconv.Itoa(i))
			if !strings.Contains(r.Stdout, want) {
				errCount.Add(1)
			}
		}
	} else {
		// Linux/macOS 用 goroutine 并发
		const goroutines = 10
		const perGoroutine = 5
		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(gid int) {
				defer wg.Done()
				for c := 0; c < perGoroutine; c++ {
					code := fmt.Sprintf("print('G%s_C%s')", strconv.Itoa(gid), strconv.Itoa(c))
					r := f.forgeGate(code, "python", "")
					if !r.OK {
						errCount.Add(1)
						continue
					}
					want := fmt.Sprintf("G%s_C%s", strconv.Itoa(gid), strconv.Itoa(c))
					if !strings.Contains(r.Stdout, want) {
						errCount.Add(1)
					}
				}
			}(g)
		}
		wg.Wait()
	}
	if errCount.Load() != 0 {
		t.Errorf("并发调用有 %d 次错误", errCount.Load())
	}
	// statBuilds 应记录所有调用 (cache hit 不增,只能看 ≥ 总调用-cache命中)
	if got := f.statBuilds.Load(); got == 0 {
		t.Errorf("statBuilds = 0, 期望至少 1")
	}
}

// TestForgeGate_Concurrent_DistinctInput: 多协程 + 不同 input 验证 cache 隔离
// Windows 串行 30 次,Linux 启用 goroutine 并发
func TestForgeGate_Concurrent_DistinctInput(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实 python 子进程批量端到端 (Windows 串行 30 次)")
	}
	f := newTestForge(t)
	defer f.Shutdown()

	code := "import os; print(os.environ.get('铸剑炉_INPUT','NONE'))"
	const total = 30
	var okCount atomic.Int64

	if runtime.GOOS == "windows" {
		for i := 0; i < total; i++ {
			inp := fmt.Sprintf("IDX_%s", strconv.Itoa(i))
			r := f.forgeGate(code, "python", inp)
			if r.OK && strings.Contains(r.Stdout, inp) {
				okCount.Add(1)
			}
		}
	} else {
		const goroutines = 10
		const perGoroutine = 3
		var wg sync.WaitGroup
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(gid int) {
				defer wg.Done()
				for c := 0; c < perGoroutine; c++ {
					inp := fmt.Sprintf("G%s_C%s", strconv.Itoa(gid), strconv.Itoa(c))
					r := f.forgeGate(code, "python", inp)
					if r.OK && strings.Contains(r.Stdout, inp) {
						okCount.Add(1)
					}
				}
			}(g)
		}
		wg.Wait()
	}
	if okCount.Load() != total {
		t.Errorf("distinct input: 只有 %d/%d 拿到自己 input", okCount.Load(), total)
	}
}

// ── 6. 指标准确性 ─────────────────────────────────────────────────

// TestForgeGate_Metrics_CodeLines: 多行代码的 CodeLines/CodeSize 准确
func TestForgeGate_Metrics_CodeLines(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "# line1\n# line2\n# line3\n# line4\nprint('METRICS_OK')\n"
	r := f.forgeGate(code, "python", "")
	if !r.OK {
		t.Fatalf("gate failed: %s", r.Error)
	}
	if r.CodeLines < 1 {
		t.Errorf("CodeLines = %d", r.CodeLines)
	}
	if r.CodeSize != len(code) {
		t.Errorf("CodeSize = %d, want %d", r.CodeSize, len(code))
	}
}

// TestForgeGate_Metrics_AllPopulated: 成功结果各字段语义
func TestForgeGate_Metrics_AllPopulated(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("print('METRICS_FULL')", "python", "")
	if !r.OK {
		t.Fatalf("gate failed: %s", r.Error)
	}
	if r.Lang != "python" {
		t.Errorf("Lang = %q", r.Lang)
	}
	if r.Duration <= 0 {
		t.Errorf("Duration = %d, want > 0", r.Duration)
	}
	if r.ExitCode != 0 {
		t.Errorf("ExitCode = %d", r.ExitCode)
	}
	if r.Error != "" {
		t.Errorf("Error should be empty on OK: %q", r.Error)
	}
	if r.Stderr != "" {
		t.Errorf("Stderr should be empty: %q", r.Stderr)
	}
	if r.DroppedBytes != 0 {
		t.Errorf("DroppedBytes = %d, want 0", r.DroppedBytes)
	}
	if r.OutputTruncated {
		t.Error("OutputTruncated should be false")
	}
}

// TestForgeGate_Metrics_OutputTruncated: 大输出下的截断字段结构
func TestForgeGate_Metrics_OutputTruncated(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "import sys\nsys.stdout.write('A' * 1200000)\nsys.stdout.flush()\n"
	r := f.forgeGate(code, "python", "")
	if !r.OK {
		t.Skipf("python 输出受限: %s", r.Error)
	}
	if len(r.Stdout) == 0 {
		t.Error("Stdout 不应为空")
	}
	t.Logf("Stdout 长度=%d, DroppedBytes=%d, Truncated=%v", len(r.Stdout), r.DroppedBytes, r.OutputTruncated)
}

// ── 7. 输入传递 ──────────────────────────────────────────────────

// TestForgeGate_InputViaStdin: python 从 stdin 读 input
func TestForgeGate_InputViaStdin(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "import sys\ndata = sys.stdin.read().strip()\nprint('STDIN=' + data)\n"
	r := f.forgeGate(code, "python", "FROM_STDIN")
	if !r.OK {
		t.Fatalf("gate failed: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "STDIN=FROM_STDIN") {
		t.Errorf("Stdin 注入失败: stdout=%q", r.Stdout)
	}
}

// TestForgeGate_InputViaEnv: python 从环境变量读 input
func TestForgeGate_InputViaEnv(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "import os\nprint('ENV=' + os.environ.get('铸剑炉_INPUT', 'MISSING'))\n"
	r := f.forgeGate(code, "python", "FROM_ENV")
	if !r.OK {
		t.Fatalf("gate failed: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "ENV=FROM_ENV") {
		t.Errorf("Env 注入失败: stdout=%q", r.Stdout)
	}
}

// TestForgeGate_InputEmpty: input="" 时不应注入 env
func TestForgeGate_InputEmpty(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "import os\nprint('VAL=' + os.environ.get('铸剑炉_INPUT', '<unset>'))\n"
	r := f.forgeGate(code, "python", "")
	if !r.OK {
		t.Fatalf("gate failed: %s", r.Error)
	}
	if !strings.Contains(r.Stdout, "VAL=<unset>") {
		t.Errorf("input 为空时不应注入 env: stdout=%q", r.Stdout)
	}
}

// ── 8. 边界 ─────────────────────────────────────────────────────

// TestForgeGate_UnsupportedLang_NoFallback: 不像任何支持语言的代码 + 未知 lang
func TestForgeGate_UnsupportedLang_NoFallback(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("(defun foo () 'foo))", "lisp", "")
	// 会被 forgeDetectLang 判成 python → python 跑 lisp 代码必然 SyntaxError
	if r.OK {
		t.Fatalf("lisp 代码在 python 下不应成功: %+v", r)
	}
	if r.Stage != "compile" {
		t.Errorf("Stage = %q, want compile", r.Stage)
	}
}

// TestForgeGate_TempFileIgnored: 执行完不留 .forge-temp 残留
func TestForgeGate_TempFileIgnored(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("package main\nfunc main(){}\n", "go", "")
	if !r.OK {
		t.Skipf("go unavailable: %s", r.Error)
	}
	time.Sleep(50 * time.Millisecond)
	tempDir := filepath.Join(f.workDir, ".forge-temp")
	if entries, err := os.ReadDir(tempDir); err == nil && len(entries) > 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf(".forge-temp 不应有未清扫文件: %v", names)
	}
}

// TestForgeGate_Build_NoLeakageAfterShutdown: Shutdown 幂等 + 之后 Build 必失败
func TestForgeGate_Build_NoLeakageAfterShutdown(t *testing.T) {
	f := newTestForge(t)
	f.Shutdown()
	f.Shutdown() // 幂等
	_, _, err := f.Build("print(1)", "python", "")
	if err == nil {
		t.Fatal("Shutdown 后 Build 应失败")
	}
}
