package main

// ── forge_integration_v2_test.go ──────────────────────────────────
// 集成测试补充场景 A: 多 lang 端到端 / 缓存 / 错误恢复 / 流水线
// 姊妹文件: forge_integration_v3_test.go (并发 / 指标 / 输入 / 边界)
// 拆分动机: F2 单文件 ≤500 行 (fractal_guard_test.go)
//
// 与 forge_integration_test.go 互补: 后者覆盖 python 基础路径与 Build 包装,
// 本组覆盖 go/sh/math/regex/logic/chain 端到端、缓存键空间与淘汰、
// 失败后恢复、跨调用 workDir 共享。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestForgeWithTools: 与 newTestForge 等价,但 toolsDir 指向生产 .forge/forge-tools
// 让 self-hosted gate (math/logic/regex/chain/tcm) 在集成测试里能跑通
// 测试结束后 t.TempDir 清理,生产 toolsDir 不受影响
func newTestForgeWithTools(t *testing.T) *Forge {
	t.Helper()
	cfg := DefaultConfig()
	cfg.WorkDir = t.TempDir()
	f := NewForge(cfg.WorkDir, cfg)
	// 找生产 toolsDir 绝对路径 (当前进程 cwd)
	cwd, _ := os.Getwd()
	prodTools := filepath.Join(cwd, ".forge", "forge-tools")
	if _, err := os.Stat(filepath.Join(prodTools, "math_gate.exe")); err == nil {
		f.toolsDir = prodTools
	}
	return f
}

// ── 1. 多 lang 端到端 ────────────────────────────────────────────

// ── 1. 多 lang 端到端 ────────────────────────────────────────────

// TestForgeGate_Go_EndToEnd: 真实 go build + run
func TestForgeGate_Go_EndToEnd(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"GO_ENDTOEND_OK\") }\n"
	r := f.forgeGate(code, "go", "")
	if !r.OK {
		t.Skipf("go unavailable: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "GO_ENDTOEND_OK") {
		t.Errorf("stdout = %q", r.Stdout)
	}
	if r.Lang != "go" {
		t.Errorf("Lang = %q, want go", r.Lang)
	}
	if r.CodeLines < 3 {
		t.Errorf("CodeLines = %d, want >= 3", r.CodeLines)
	}
}

// TestForgeGate_Sh_EndToEnd: sh 执行
func TestForgeGate_Sh_EndToEnd(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	r := f.forgeGate("echo SH_ENDTOEND_OK", "sh", "")
	if !r.OK {
		t.Skipf("sh unavailable: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "SH_ENDTOEND_OK") {
		t.Errorf("stdout = %q", r.Stdout)
	}
}

// TestForgeGate_Math_EndToEnd: self-hosted math gate
func TestForgeGate_Math_EndToEnd(t *testing.T) {
	f := newTestForgeWithTools(t)
	defer f.Shutdown()
	r := f.forgeGate("2+3*4", "math", "")
	if !r.OK {
		// self-hosted gate 二进制不在测试 workDir 下 → 跳过(有依赖才跑)
		t.Skipf("math gate unavailable: %s", r.Error)
	}
	if !strings.Contains(r.Stdout, "14") {
		t.Errorf("stdout = %q (expected 14)", r.Stdout)
	}
}

// TestForgeGate_Regex_EndToEnd: self-hosted regex gate (整串匹配)
func TestForgeGate_Regex_EndToEnd(t *testing.T) {
	f := newTestForgeWithTools(t)
	defer f.Shutdown()
	input := `{"type":"match","pattern":"[0-9]{3}","positive":["123","456"],"negative":["12","abcd"]}`
	r := f.forgeGate(input, "regex", "")
	if !r.OK {
		t.Skipf("regex gate unavailable: %s", r.Error)
	}
	if !strings.Contains(r.Stdout, "ok") && !strings.Contains(r.Stdout, "true") && !strings.Contains(r.Stdout, "OK") {
		t.Errorf("stdout should signal match success: %q", r.Stdout)
	}
}

// TestForgeGate_Logic_EndToEnd: z3 SAT (简单 x>0)
func TestForgeGate_Logic_EndToEnd(t *testing.T) {
	f := newTestForgeWithTools(t)
	defer f.Shutdown()
	// 简单整数约束:存在 x 使 x > 0 && x < 10
	input := "x = Int('x')\ns = Solver()\nclaim = And(x > 0, x < 10)\nout = s.check()\nr = s.model()"
	r := f.forgeGate(input, "logic", "")
	if !r.OK {
		t.Skipf("logic(z3) unavailable: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "sat") {
		t.Errorf("stdout should contain sat: %q", r.Stdout)
	}
}

// TestForgeGate_Chain_EndToEnd: chain gate 编排 math → math
func TestForgeGate_Chain_EndToEnd(t *testing.T) {
	f := newTestForgeWithTools(t)
	defer f.Shutdown()
	input := `{"stages":[{"gate":"math","input":{"expr":"2+2"}},{"gate":"math","input":{"expr":"3*3"}}]}`
	r := f.forgeGate(input, "chain", "")
	if !r.OK {
		t.Skipf("chain gate unavailable: %s", r.Error)
	}
	// chain 应串行跑两阶段,最终输出含 4 和 9
	if !strings.Contains(r.Stdout, "4") || !strings.Contains(r.Stdout, "9") {
		t.Errorf("chain output missing intermediate results: %q", r.Stdout)
	}
}

// TestForgeGate_AutoDetectLang: 空 lang hint + 代码自身能识别
func TestForgeGate_AutoDetectLang(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	// 显式传 lang="" + 不在编译器表中 → forgeDetectLang 接管
	r := f.forgeGate("print('AUTODETECT_OK')", "", "")
	if !r.OK {
		t.Fatalf("auto-detect failed: %s / stderr=%s", r.Error, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "AUTODETECT_OK") {
		t.Errorf("stdout = %q", r.Stdout)
	}
	if r.Lang != "python" {
		t.Errorf("Lang = %q, want python (auto-detected)", r.Lang)
	}
}

// ── 2. 缓存 ─────────────────────────────────────────────────────

// TestForgeGate_Cache_DistinctInput: 同一代码 + 不同 input 走不同 key
func TestForgeGate_Cache_DistinctInput(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "import os; print(os.environ.get('铸剑炉_INPUT','NONE'))"
	r1 := f.forgeGate(code, "python", "ALPHA")
	if !r1.OK || !strings.Contains(r1.Stdout, "ALPHA") {
		t.Fatalf("first call: %+v", r1)
	}
	hitsBefore := f.statCacheHits.Load()
	r2 := f.forgeGate(code, "python", "BETA")
	if !r2.OK || !strings.Contains(r2.Stdout, "BETA") {
		t.Fatalf("second call: %+v", r2)
	}
	// 不同 input 必须 cache miss,否则缓存键错位
	if f.statCacheHits.Load() != hitsBefore {
		t.Errorf("input 不同的两次调用竟然命中了缓存: hits before=%d after=%d", hitsBefore, f.statCacheHits.Load())
	}
}

// TestForgeGate_Cache_FIFO_Eviction: 强制 CacheMaxSize=2,写 5 个 key,验证前 2 个被淘汰
func TestForgeGate_Cache_FIFO_Eviction(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WorkDir = t.TempDir()
	cfg.CacheMaxSize = 2
	f := NewForge(cfg.WorkDir, cfg)
	defer f.Shutdown()

	for i := 0; i < 5; i++ {
		r := f.forgeGate(fmt.Sprintf("print('EVICT_%d')", i), "python", "")
		if !r.OK {
			t.Fatalf("call %d: %+v", i, r)
		}
	}
	f.cacheMu.RLock()
	keys := []string{}
	for k := range f.cache {
		keys = append(keys, k)
	}
	f.cacheMu.RUnlock()
	if len(keys) != 2 {
		t.Errorf("cache size = %d, want 2 (FIFO 淘汰)", len(keys))
	}
	// FIFO: 最早的两个 (0,1) 应被淘汰,留下 (3,4)
	for _, k := range keys {
		// 检查每条缓存项的 Stdout,验证留下的都是 3 或 4
		f.cacheMu.RLock()
		v := f.cache[k]
		f.cacheMu.RUnlock()
		if !strings.Contains(v.Stdout, "EVICT_3") && !strings.Contains(v.Stdout, "EVICT_4") {
			t.Errorf("FIFO 淘汰错误: 留下的 %q 含 Stdout=%q", k[:8], v.Stdout)
		}
	}
}

// ── 3. 错误恢复 ──────────────────────────────────────────────────

// TestForgeGate_RecoverAfterError: 失败调用后,同一实例仍可正常服务
func TestForgeGate_RecoverAfterError(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()

	// 阶段 1: syntax error
	r1 := f.forgeGate("print(", "python", "")
	if r1.OK {
		t.Fatal("syntax error should not be OK")
	}
	if r1.Stage != "compile" {
		t.Errorf("Stage = %q, want compile", r1.Stage)
	}

	// 阶段 2: 同一 f → 正确代码 → 应正常跑
	r2 := f.forgeGate("print('RECOVER_OK')", "python", "")
	if !r2.OK {
		t.Fatalf("recovery failed: %s", r2.Error)
	}
	if !strings.Contains(r2.Stdout, "RECOVER_OK") {
		t.Errorf("stdout = %q", r2.Stdout)
	}

	// 阶段 3: runtime error (ZeroDivisionError 有输出 → OK=true)
	r3 := f.forgeGate("print(1/0)", "python", "")
	if !r3.OK || r3.ExitCode == 0 {
		t.Errorf("runtime error should be result with non-zero exit: %+v", r3)
	}

	// 阶段 4: 再成功
	r4 := f.forgeGate("print('FINAL_OK')", "python", "")
	if !r4.OK || !strings.Contains(r4.Stdout, "FINAL_OK") {
		t.Errorf("final recovery: %+v", r4)
	}
}

// TestForgeGate_EmptyCode_AllLangs: 各 lang 收到空码都给一致报错
func TestForgeGate_EmptyCode_AllLangs(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	for _, lang := range []string{"python", "go", "sh", "node"} {
		t.Run(lang, func(t *testing.T) {
			r := f.forgeGate("   \n  ", lang, "")
			if r.OK {
				t.Errorf("lang=%s should reject empty code", lang)
			}
			if !strings.Contains(r.Error, "代码为空") {
				t.Errorf("lang=%s Error = %q, want 代码为空", lang, r.Error)
			}
		})
	}
}

// ── 4. 流水线 ────────────────────────────────────────────────────

// TestForgeGate_Pipeline_WorkdirShared: 跨调用共享 workDir,文件可被后续调用读到
func TestForgeGate_Pipeline_WorkdirShared(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()

	// 步骤 1: 写入文件
	step1 := `
import os, json
data = {"x": 10, "y": 20}
path = os.path.join(os.getcwd(), "shared_data.json")
with open(path, "w", encoding="utf-8") as fh:
    json.dump(data, fh)
print("WRITTEN")
`
	r1 := f.forgeGate(step1, "python", "")
	if !r1.OK || !strings.Contains(r1.Stdout, "WRITTEN") {
		t.Fatalf("step1 failed: %+v", r1)
	}

	// 步骤 2: 读取文件
	step2 := `
import json
with open("shared_data.json", encoding="utf-8") as fh:
    data = json.load(fh)
print(f"SUM={data['x'] + data['y']}")
`
	r2 := f.forgeGate(step2, "python", "")
	if !r2.OK {
		t.Fatalf("step2 failed: %+v", r2)
	}
	if !strings.Contains(r2.Stdout, "SUM=30") {
		t.Errorf("step2 stdout = %q (want SUM=30)", r2.Stdout)
	}

	// 验证文件确实在 f.workDir 下
	cfgFile := filepath.Join(f.workDir, "shared_data.json")
	if _, err := os.Stat(cfgFile); err != nil {
		t.Errorf("shared_data.json not in workDir: %v", err)
	}
}
