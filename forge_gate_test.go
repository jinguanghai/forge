package main

// forge_gate_test.go — gate 分发与缓存的哨兵。
//
// 缓存键判错 = 用 A 代码的结果回答 B 代码(错误结论被当成"已缓存的事实");
// 缓存命中提示丢失 = 用户以为代码真跑了(实际未执行)。

import (
	"strings"
	"testing"
	"time"
)

func TestForgeGate_CacheKeyStableAndDistinct(t *testing.T) {
	f := &Forge{}
	k1 := f.cacheKey("code", "go", "")
	k2 := f.cacheKey("code", "go", "")
	if k1 != k2 {
		t.Errorf("同输入 cacheKey 不稳定: %q != %q", k1, k2)
	}
	if k1 == f.cacheKey("code2", "go", "") {
		t.Error("不同 code 必须产生不同 cacheKey")
	}
	if k1 == f.cacheKey("code", "python", "") {
		t.Error("不同 lang 必须产生不同 cacheKey")
	}
	if k1 == f.cacheKey("code", "go", "input") {
		t.Error("不同 input 必须产生不同 cacheKey")
	}
	if len(k1) != 64 {
		t.Errorf("cacheKey 长度 = %d, 期望 64(sha256 hex)", len(k1))
	}
}

func TestForgeGate_CacheResultAndRemove(t *testing.T) {
	f := &Forge{cache: map[string]ForgeGateResult{}}
	k := f.cacheKey("code", "go", "")
	f.cacheResult(k, ForgeGateResult{OK: true, Lang: "go", Stdout: "hi"})
	if len(f.cache) != 1 {
		t.Fatalf("缓存条数 = %d, 期望 1", len(f.cache))
	}
	f.removeCacheKeyLocked(k)
	if len(f.cache) != 0 {
		t.Errorf("移除后缓存条数 = %d, 期望 0", len(f.cache))
	}
}

func TestForgeGate_CacheHitNote(t *testing.T) {
	// CachedAt<=0 表示旧格式条目(已过期), 不得声称"缓存命中"
	if got := cacheHitNote(ForgeGateResult{OK: true}); got != "" {
		t.Errorf("无 CachedAt 不应产生命中提示, 得到 %q", got)
	}
	got := cacheHitNote(ForgeGateResult{OK: true, CachedAt: time.Now().Unix() - 5})
	if !strings.Contains(got, "缓存命中") {
		t.Errorf("命中提示 = %q, 应含 \"缓存命中\"", got)
	}
	// 必须明确告知"本次未实际执行", 否则用户会把缓存结果当成真实运行
	if !strings.Contains(got, "未实际执行") {
		t.Errorf("命中提示 = %q, 应含 \"未实际执行\"", got)
	}
}

func TestForgeGate_SupportedLangs(t *testing.T) {
	got := (&Forge{}).supportedLangs()
	if got == "" {
		t.Fatal("supportedLangs 为空")
	}
	// 已知 gate 必须出现在清单里(漏一个会让模型以为该语言不可用)
	for _, lang := range []string{"go", "python", "node", "math", "logic", "regex"} {
		if !strings.Contains(got, lang) {
			t.Errorf("supportedLangs 缺 %q: %s", lang, got)
		}
	}
}

func TestForgeGate_FormatResult(t *testing.T) {
	f := &Forge{}
	ok := f.formatResult(ForgeGateResult{OK: true, Lang: "go", Stage: "done", Stdout: "hi"})
	if ok == "" {
		t.Fatal("成功结果的格式化输出为空")
	}
	bad := f.formatResult(ForgeGateResult{OK: false, Lang: "go", Error: "boom"})
	if bad == "" {
		t.Fatal("失败结果的格式化输出为空")
	}
	if !strings.Contains(bad, "boom") {
		t.Errorf("失败输出应含错误信息, 得到 %q", bad)
	}
}

// ─── 渲染歧义: OK=true 但退出码非零 ─────────────────────────────

// TestFormatResult_NonZeroExitVisible 钉住渲染层的歧义:
// forgeGateFile 在「程序运行时报错退出但有输出」时返回 OK=true + ExitCode!=0
// (设计契约: runtime errors with output are results, not gate failures)。
// 结构体里 OK/ExitCode/Error 三重并存足以区分, 但渲染层若只说「成功」,
// 模型读到的唯一信号就是「运行正常」—— 证据在呈现层被削弱。
func TestFormatResult_NonZeroExitVisible(t *testing.T) {
	f := &Forge{}
	// 零退出码: 保持原文案(不引入噪音)
	clean := f.formatResult(ForgeGateResult{OK: true, Lang: "python", Stage: "done",
		Stdout: "hi", ExitCode: 0, Duration: 12, CodeLines: 1})
	if !strings.Contains(clean, "成功") {
		t.Errorf("正常成功结果丢了「成功」: %q", clean)
	}
	if strings.Contains(clean, "非零") {
		t.Errorf("零退出码被误标「非零」: %q", clean)
	}
	// 非零退出码: 必须显式呈现, 且不得丢失「成功」语义(OK 确实为真)
	bad := f.formatResult(ForgeGateResult{OK: true, Lang: "python", Stage: "execute",
		Stdout: "hi", ExitCode: 1, Duration: 12, CodeLines: 1})
	if !strings.Contains(bad, "成功") {
		t.Errorf("OK=true 的结果丢了「成功」: %q", bad)
	}
	if !strings.Contains(bad, "非零") {
		t.Errorf("退出码非零未呈现 —— 「成功」会被读成「运行正常」: %q", bad)
	}
	if !strings.Contains(bad, "exit=1") {
		t.Errorf("未给出具体退出码: %q", bad)
	}
	// 具体码值必须原样透出(137=被 SIGKILL/可能 OOM, 与 1 意义不同)
	oom := f.formatResult(ForgeGateResult{OK: true, Lang: "python", Stdout: "x", ExitCode: 137})
	if !strings.Contains(oom, "exit=137") {
		t.Errorf("退出码 137 未透出: %q", oom)
	}
	// 失败分支不受影响(已有 exit 字段)
	failOut := f.formatResult(ForgeGateResult{OK: false, Lang: "python", Stage: "compile",
		ExitCode: 1, Error: "boom"})
	if !strings.Contains(failOut, "失败") || !strings.Contains(failOut, "exit=1") {
		t.Errorf("失败分支被改动: %q", failOut)
	}
}

// TestFormatResult_NonZeroExitE2E 真实跑「打印后非零退出」: 判据必须在真链路上成立。
func TestFormatResult_NonZeroExitE2E(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	code := "import sys\nprint('OUT_BEFORE_EXIT')\nsys.exit(3)"
	r := f.forgeGate(code, "python", "")
	// 契约: 有输出 = 结果而非 gate 故障
	if !r.OK {
		t.Fatalf("有输出的非零退出应判为结果, 实际 OK=false: %s", r.Error)
	}
	if r.ExitCode != 3 {
		t.Fatalf("退出码应为 3, 实际 %d", r.ExitCode)
	}
	out := f.formatResult(r)
	if !strings.Contains(out, "OUT_BEFORE_EXIT") {
		t.Errorf("正文丢失: %q", out)
	}
	if !strings.Contains(out, "exit=3") || !strings.Contains(out, "非零") {
		t.Errorf("渲染层丢了非零退出码 —— 模型只会看到「成功」: %q", out)
	}

	// 最严重场景: stderr 非空 → Error 字段不参与渲染
	// (见 formatResult 的 `r.Error != "" && r.Stderr == ""` 条件),
	// 修复前退出码在渲染输出里 100% 丢失 —— 正文只有 stdout 与 stderr, 无任何退出码痕迹。
	code2 := "import sys\nprint('OUT_LINE')\nprint('ERR_LINE', file=sys.stderr)\nsys.exit(3)"
	r2 := f.forgeGate(code2, "python", "")
	if !r2.OK || r2.ExitCode != 3 || r2.Stderr == "" {
		t.Fatalf("stderr 非空场景前置条件不成立: OK=%v exit=%d stderr=%q", r2.OK, r2.ExitCode, r2.Stderr)
	}
	out2 := f.formatResult(r2)
	if !strings.Contains(out2, "exit=3") {
		t.Errorf("stderr 非空时退出码在渲染层完全丢失: %q", out2)
	}
	if !strings.Contains(out2, "ERR_LINE") {
		t.Errorf("stderr 正文丢失: %q", out2)
	}
}
