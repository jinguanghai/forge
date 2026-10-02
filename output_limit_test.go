package main

// output_limit_test.go — A1 输出捕获上限 + A2 缓存命中可见化
//
// 动机 (DSec, arXiv 2609.22978, 2026-09-19): 采集量无上限 → agent 跑 `yes`
// 累积几十 GB; 幂等/非幂等操作需显式区分。
// 本机探针实测(64 MiB 子进程输出):
//   无限制 strings.Builder → 堆 +64.1 MiB;  bytes.Buffer → +128.0 MiB;
//   limitedWriter(4 MiB)   → +4.4 MiB, exit=0;  errWriter(返 error) → exit=1。

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// ─── A1: limitedWriter 单元 ───────────────────────────────────

func TestLimitedWriter_TruncatesAndCounts(t *testing.T) {
	w := newLimitedWriter(1024)
	chunk := strings.Repeat("x", 300)
	total := 0
	for i := 0; i < 10; i++ {
		n, err := w.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write 返回错误 %v — 会中断 os/exec 拷贝协程", err)
		}
		if n != len(chunk) {
			t.Fatalf("Write 返回 n=%d, 期望 %d", n, len(chunk))
		}
		total += len(chunk)
	}
	if w.Len() != 1024 {
		t.Errorf("捕获长度 = %d, 期望封顶 1024", w.Len())
	}
	if w.Dropped() != total-1024 {
		t.Errorf("丢弃计数 = %d, 期望 %d", w.Dropped(), total-1024)
	}
}

// 契约守: Write 恒返回 (len(p), nil)。反例实测: 超限返 error 的子进程 exit=1。
func TestLimitedWriter_NeverReturnsError(t *testing.T) {
	w := newLimitedWriter(16)
	for i := 0; i < 100; i++ {
		n, err := w.Write([]byte("0123456789"))
		if err != nil || n != 10 {
			t.Fatalf("第 %d 次 Write 违反契约: n=%d err=%v", i, n, err)
		}
	}
}

func TestLimitedWriter_ZeroLimitFallsBack(t *testing.T) {
	w := newLimitedWriter(0)
	if w.limit != maxGateCaptureBytes {
		t.Errorf("limit = %d, 期望回落 %d", w.limit, maxGateCaptureBytes)
	}
}

// 真实子进程: 限流不得杀死子进程(反例 errWriter 会让它 exit=1)。
func TestLimitedWriter_RealSubprocessNotKilled(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python 不在 PATH")
	}
	w := newLimitedWriter(64 << 10)
	cmd := exec.Command("python", "-c", "import sys; sys.stdout.write('y'*(2*1024*1024))")
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		t.Fatalf("限流后子进程失败: %v (捕获 %d, 丢弃 %d)", err, w.Len(), w.Dropped())
	}
	if w.Len() > 64<<10 {
		t.Errorf("捕获超上限: %d", w.Len())
	}
	if w.Dropped() == 0 {
		t.Error("未记录丢弃字节")
	}
}

func TestGateCaptureLimit_EnvOverride(t *testing.T) {
	old, had := os.LookupEnv("FORGE_MAX_CAPTURE")
	defer func() {
		if had {
			os.Setenv("FORGE_MAX_CAPTURE", old)
		} else {
			os.Unsetenv("FORGE_MAX_CAPTURE")
		}
	}()
	os.Setenv("FORGE_MAX_CAPTURE", "8192")
	if got := gateCaptureLimit(); got != 8192 {
		t.Errorf("env 覆盖失效: %d", got)
	}
	os.Setenv("FORGE_MAX_CAPTURE", "not-a-number")
	if got := gateCaptureLimit(); got != maxGateCaptureBytes {
		t.Errorf("非法值应回落默认: %d", got)
	}
}

func TestMarkCaptureLimit(t *testing.T) {
	var r ForgeGateResult
	markCaptureLimit(&r, newLimitedWriter(4), newLimitedWriter(4))
	if r.OutputTruncated || r.DroppedBytes != 0 {
		t.Errorf("无丢弃不应标记: %+v", r)
	}
	a, b := newLimitedWriter(4), newLimitedWriter(4)
	a.Write([]byte("12345678"))
	b.Write([]byte("1234"))
	markCaptureLimit(&r, a, b)
	if !r.OutputTruncated || r.DroppedBytes != 4 {
		t.Errorf("丢弃合计 = %d, 期望 4 (truncated=%v)", r.DroppedBytes, r.OutputTruncated)
	}
}

// ─── A1: 真实 gate 端到端 ─────────────────────────────────────

// TestGateOutputLimit_E2E 8 MiB 输出 > 4 MiB 上限: 截获但不影响成功判定。
func TestGateOutputLimit_E2E(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("import sys\nsys.stdout.write('A'*(8*1024*1024))", "python", "")
	if !r.OK {
		t.Fatalf("gate 失败: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !r.OutputTruncated || r.DroppedBytes <= 0 {
		t.Fatalf("未标记超限: truncated=%v dropped=%d", r.OutputTruncated, r.DroppedBytes)
	}
	if r.ExitCode != 0 {
		t.Errorf("ExitCode = %d — 限流不应改变子进程退出码", r.ExitCode)
	}
	if len(r.Stdout) > 4<<20 {
		t.Errorf("捕获仍超上限: %d 字节", len(r.Stdout))
	}
}

func TestGateOutputUnderLimit_NotMarked(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("print('small-output')", "python", "")
	if !r.OK {
		t.Fatalf("gate 失败: %s", r.Error)
	}
	if r.OutputTruncated || r.DroppedBytes != 0 {
		t.Errorf("小输出被误标超限: truncated=%v dropped=%d", r.OutputTruncated, r.DroppedBytes)
	}
}

func TestFormatResult_CaptureNote(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	over := f.formatResult(ForgeGateResult{Lang: "python", OK: true, Stdout: "hi",
		DroppedBytes: 12345, OutputTruncated: true})
	if !strings.Contains(over, "输出超限") || !strings.Contains(over, "12345") {
		t.Errorf("超限标注缺失: %q", over)
	}
	clean := f.formatResult(ForgeGateResult{Lang: "python", OK: true, Stdout: "hi"})
	if strings.Contains(clean, "输出超限") {
		t.Errorf("正常输出被误标: %q", clean)
	}
}

// ─── A2: 缓存命中可见化 ───────────────────────────────────────

func TestCacheHitNote(t *testing.T) {
	if got := cacheHitNote(ForgeGateResult{}); got != "" {
		t.Errorf("未命中不应有提示: %q", got)
	}
	got := cacheHitNote(ForgeGateResult{CachedAt: time.Now().Unix() - 42})
	if !strings.Contains(got, "缓存命中") || !strings.Contains(got, "42s") {
		t.Errorf("命中提示 = %q", got)
	}
	// 未来时间戳(时钟回拨)不得输出负数
	future := cacheHitNote(ForgeGateResult{CachedAt: time.Now().Unix() + 999})
	if strings.Contains(future, "-") {
		t.Errorf("时钟回拨应归零: %q", future)
	}
}

// TestGateCacheHit_LabeledE2E 同代码连调两次: 第二次必须标注且带 CachedAt。
func TestGateCacheHit_LabeledE2E(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "print('CACHE_PROBE_A2')"
	r1 := f.forgeGate(code, "python", "")
	if !r1.OK {
		t.Fatalf("首次执行失败: %s", r1.Error)
	}
	if r1.CachedAt != 0 {
		t.Errorf("首次执行不应带 CachedAt: %d", r1.CachedAt)
	}
	r2 := f.forgeGate(code, "python", "")
	if r2.CachedAt == 0 {
		t.Fatal("第二次未命中缓存 — 判据失效")
	}
	if !strings.Contains(f.formatResult(r2), "缓存命中") {
		t.Errorf("命中结果未标注: %q", f.formatResult(r2))
	}
	if strings.Contains(f.formatResult(r1), "缓存命中") {
		t.Errorf("真跑结果被误标: %q", f.formatResult(r1))
	}
}

// ─── 静态哨兵: 防止捕获点回退 ─────────────────────────────────

// TestNoUnboundedCapture_Sentinel 捕获点不得回退为无上限容器。
// 教训(接线≠声称接线): 功能时有时无优先怀疑构建源≠运行源, 故用死程序守。
func TestNoUnboundedCapture_Sentinel(t *testing.T) {
	// 扫全包生产源码: 捕获点分散在 gate 分发/自托管/文件式等多个文件,
	// 只扫 forge.go 会在拆分后静默失效(实测踩过 7 次, 同一病灶)。
	s := prodGoSources(t)
	for _, bad := range []string{
		"cmd.Stdout = &stdout", "cmd.Stderr = &stderr",
		"cmd.Stdout = &buf", "cmd.Stderr = &buf",
		"var stdout, stderr strings.Builder",
	} {
		if strings.Contains(s, bad) {
			t.Errorf("发现无上限捕获 %q — 应改用 newLimitedWriter(gateCaptureLimit())", bad)
		}
	}
	// 阈值 4: sh gate 退役(20261001)删掉了 runShViaBash / selfHostedSh 两条路径的捕获点,
	// 原 6 → 4。下限仍钉住"不得再退化": 删任一捕获点即报红。
	if n := strings.Count(s, "newLimitedWriter(gateCaptureLimit())"); n < 4 {
		t.Errorf("捕获点 = %d, 期望 >= 4 (漏改则部分 gate 无上限; sh 退役后基线为 4)", n)
	}
	if n := strings.Count(s, "defer markCaptureLimit(&gateRes, stdout, stderr)"); n < 4 {
		t.Errorf("标记点 = %d, 期望 >= 4 (漏标记则超限对用户不可见; sh 退役后基线为 4)", n)
	}
}
