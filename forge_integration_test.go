package main

import (
	"strings"
	"testing"
)

// ─── forgeGate 集成测试: 真实编译器 + 缓存 ────────────────────
// 使用临时工作目录, 避免污染生产 forge_cache.gob。

func newTestForge(t *testing.T) *Forge {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorkDir = t.TempDir()
	return NewForge(cfg.WorkDir, cfg)
}

func TestForgeGate_PythonRun(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("print('HELLO_FORGE_TEST')", "python", "")
	if !r.OK {
		t.Fatalf("python gate failed: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "HELLO_FORGE_TEST") {
		t.Errorf("stdout = %q", r.Stdout)
	}
	if r.CodeLines < 1 || r.CodeSize == 0 {
		t.Errorf("code metrics missing: lines=%d size=%d", r.CodeLines, r.CodeSize)
	}
}

func TestForgeGate_PythonCompileError_Diagnostics(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	// 语法错误在 py_compile 检查阶段拦截 → OK=false + structured diagnostics
	r := f.forgeGate("print(", "python", "")
	if r.OK {
		t.Fatal("compile error should fail")
	}
	if r.Stage != "compile" {
		t.Errorf("Stage = %q, want compile", r.Stage)
	}
	if r.Diagnostics == "" {
		t.Error("compile failure should carry structured diagnostics")
	}
	if !strings.Contains(r.Diagnostics, "SyntaxError") {
		t.Errorf("diagnostics = %q", r.Diagnostics)
	}
}

// 运行时错误有输出 → 视为结果而非 gate 失败 (L1913 设计: "results, not gate failures")
func TestForgeGate_PythonRuntimeError_IsResult(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("print(1/0)", "python", "")
	if !r.OK {
		t.Fatalf("runtime error with output should be a result, got OK=false: %s", r.Error)
	}
	if r.ExitCode == 0 {
		t.Error("ExitCode should be non-zero")
	}
	if !strings.Contains(r.Error, "execution failed") {
		t.Errorf("Error = %q", r.Error)
	}
	if !strings.Contains(r.Stderr, "ZeroDivisionError") {
		t.Errorf("Stderr = %q", r.Stderr)
	}
}

func TestForgeGate_EmptyCode(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("   \n  ", "python", "")
	if r.OK {
		t.Fatal("empty code should fail")
	}
	if !strings.Contains(r.Error, "代码为空") {
		t.Errorf("Error = %q", r.Error)
	}
}

func TestForgeGate_LangFallback(t *testing.T) {
	// 请求未知语言, 代码像 python → 自动回退 python 执行成功
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("print('FB_OK')", "fortran", "")
	if !r.OK {
		t.Fatalf("fallback failed: %s", r.Error)
	}
	if !strings.Contains(r.Stdout, "FB_OK") {
		t.Errorf("stdout = %q", r.Stdout)
	}
}

func TestForgeGate_CacheHit(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "print('CACHE_MARK')"
	r1 := f.forgeGate(code, "python", "")
	if !r1.OK {
		t.Fatalf("first run failed: %s", r1.Error)
	}
	hitsBefore := f.statCacheHits.Load()
	r2 := f.forgeGate(code, "python", "")
	if !r2.OK {
		t.Fatalf("second run failed: %s", r2.Error)
	}
	if f.statCacheHits.Load() != hitsBefore+1 {
		t.Errorf("cache hit not counted: %d -> %d", hitsBefore, f.statCacheHits.Load())
	}
	// 缓存结果直接返回 (不重新执行), 内容一致
	if !strings.Contains(r2.Stdout, "CACHE_MARK") {
		t.Errorf("cached stdout = %q", r2.Stdout)
	}
}

func TestForgeGate_CacheTTLExpired(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "print('TTL_FRESH')"
	r1 := f.forgeGate(code, "python", "")
	if !r1.OK {
		t.Fatalf("first run failed: %s", r1.Error)
	}
	key := f.cacheKey(code, "python", "")
	// 模拟旧格式条目 (CachedAt=0 → 过期) 被写入缓存
	f.cacheMu.Lock()
	f.cache[key] = ForgeGateResult{OK: true, Lang: "python", CachedAt: 0, Stdout: "STALE"}
	f.cacheMu.Unlock()

	r2 := f.forgeGate(code, "python", "")
	if !r2.OK || !strings.Contains(r2.Stdout, "TTL_FRESH") {
		t.Errorf("stale entry not re-executed: %+v", r2)
	}
	if strings.Contains(r2.Stdout, "STALE") {
		t.Error("served stale result")
	}
}

func TestForgeGate_InputPassed(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	// python 通过环境变量铸剑炉_INPUT 接收输入
	code := "import os\nprint(os.environ.get('铸剑炉_INPUT', ''))"
	r := f.forgeGate(code, "python", "INPUT_VALUE_XYZ")
	if !r.OK {
		t.Fatalf("gate failed: %s", r.Error)
	}
	if !strings.Contains(r.Stdout, "INPUT_VALUE_XYZ") {
		t.Errorf("input not passed through: stdout=%q", r.Stdout)
	}
}

func TestForge_Build_OK(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	out, res, err := f.Build("print(6*7)", "python", "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res == nil || !res.OK {
		t.Fatal("result not OK")
	}
	if !strings.Contains(out, "42") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "成功") {
		t.Errorf("output missing success header: %q", out[:min(60, len(out))])
	}
}

func TestForge_Build_CompileError(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	out, res, err := f.Build("print(", "python", "")
	if err == nil {
		t.Fatal("compile error should fail the build")
	}
	if res == nil || res.OK {
		t.Fatal("result should not be OK")
	}
	if !strings.Contains(err.Error(), "门失败") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out, "失败") {
		t.Errorf("output should carry failure header: %q", out[:min(80, len(out))])
	}
}

func TestForge_Build_RuntimeError_IsResult(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	// 运行时错误有 stderr 输出 → OK=true (设计), 但内容含 traceback
	out, res, err := f.Build("print(1/0)", "python", "")
	if err != nil {
		t.Fatalf("runtime error should be a result, got: %v", err)
	}
	if res == nil || !res.OK {
		t.Fatal("result should be OK (output present)")
	}
	if !strings.Contains(out, "ZeroDivisionError") {
		t.Errorf("output should expose traceback: %q", out)
	}
}

func TestForge_Build_Alias(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	// js → node 别名归一化
	out, res, err := f.Build("console.log('ALIAS_JS_OK')", "js", "")
	if err != nil || !res.OK {
		t.Fatalf("js alias build failed: %v / %+v", err, res)
	}
	if !strings.Contains(out, "ALIAS_JS_OK") {
		t.Errorf("output = %q", out)
	}
}

func TestForge_Shutdown_ThenBuild(t *testing.T) {
	f := newTestForge(t)
	f.Shutdown()
	// Shutdown 取消 f.ctx → 子进程 context canceled → Build 返回门失败 (ErrShuttingDown 仅 sem 排队时触发)
	_, _, err := f.Build("print(1)", "python", "")
	if err == nil {
		t.Fatal("expected error after shutdown")
	}
}

func TestForge_Stats_CountsBuilds(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	f.forgeGate("print('S')", "python", "")
	if f.statBuilds.Load() == 0 {
		t.Error("statBuilds not incremented")
	}
}

func TestForge_NodeRun(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("console.log('NODE_OK_42')", "node", "")
	if !r.OK {
		t.Skipf("node unavailable: %s", r.Error)
	}
	if !strings.Contains(r.Stdout, "NODE_OK_42") {
		t.Errorf("stdout = %q", r.Stdout)
	}
}
