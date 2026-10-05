package main

// memory_guard_test.go — 记忆写入拒绝权判据哨兵 (20261003)
//
// 架构即测试(公理三): "记忆写入纪律"不是约束, 死程序判据才是。本文件钉住六件事:
//   V1 判据向量 —— 有留痕必放 / 无留痕必拒 / 无基线不判定 / 主文件缺失不判定
//   V2 逃生通道 —— 含闭合留痕链动作的代码必放行(防死锁), 且命中名非空(审计可区分)
//   V3 开关     —— FORGE_MEMORY_GUARD=0 必须放行(回滚通道, 与引入前行为一致)
//   V4 接线     —— Build 里拒绝必须发生在 retryGate 之前(只报不拦 = 软约束)
//   V5 出路     —— 拒绝文本必须含出口工具用法与回滚开关(拒绝必须自带出路)
//   V6 端到端   —— 走 Build 入口: 旁路版本必拒且不执行, 合规版本必放行(防误伤)
//
// 变异自检(判据的判据): 删掉 memory_guard.go 的拒绝分支, V1/V6 必报红;
// 删掉 Build 里的接线, V4 必报红; 清空 memoryGuardFixHints, V2 必报红。
//
// 隔离: 全部用例走 t.TempDir() —— 会改真实数据的测试必须隔离工作目录
// (实测教训: 用 wd := "." 的测试直写真实 memory.json)。

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memGuardFixture 构造隔离工作目录: memory.json + memory_writes.jsonl。
// traced=false 时留痕里放的是别的 sha = 旁路写入场景(当前内容无留痕)。
func memGuardFixture(t *testing.T, traced bool) string {
	t.Helper()
	d := t.TempDir()
	mem := []byte(`{"identity":"t","axioms":"a"}`)
	if err := os.WriteFile(filepath.Join(d, "memory.json"), mem, 0644); err != nil {
		t.Fatal(err)
	}
	sha := sysHashPrefix(mem)
	if !traced {
		sha = "deadbeefdeadbeef"
	}
	line, err := json.Marshal(MemoryWriteEntry{
		Time: "2026-10-03T00:00:00+08:00", SHA: sha, Size: len(mem),
		Src: memoryWriteSrcSave, Reason: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, memoryWritesFileName), append(line, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	return d
}

// memGuardForge 构造只含 Build 所需字段的 Forge (与 netroute 端到端用例同构)。
func memGuardForge(d string) *Forge {
	return &Forge{workDir: d, ctx: context.Background(),
		sem: make(chan struct{}, 1), cache: map[string]ForgeGateResult{}}
}

// TestMemoryGuard_Vectors V1: 判据向量 —— 确凿旁路必拒, 判不了必放(宁缺毋滥)。
func TestMemoryGuard_Vectors(t *testing.T) {
	cases := []struct {
		name   string
		traced bool
		want   bool
	}{
		{"有留痕 => 放行", true, true},
		{"无留痕(旁路写入) => 拒绝", false, false},
	}
	for _, c := range cases {
		d := memGuardFixture(t, c.traced)
		ok, hit, why := checkMemoryGuard(d, `print("x")`)
		if ok != c.want {
			t.Errorf("%s: ok=%v; 期望 %v (%s)", c.name, ok, c.want, why)
		}
		if !c.want && hit == "" {
			t.Errorf("%s: 拒绝时命中名不得为空(审计要能区分)", c.name)
		}
		if c.want && hit != "" {
			t.Errorf("%s: 正常放行不应带命中名, 得 %q", c.name, hit)
		}
	}
	// 无留痕基线 (留痕文件不存在) => 放行
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "memory.json"), []byte(`{"a":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := checkMemoryGuard(d, "x"); !ok {
		t.Error("无留痕基线时必须放行 (哨兵尚未生效, 不能拦)")
	}
	// 主文件缺失 => 放行
	if ok, _, _ := checkMemoryGuard(t.TempDir(), "x"); !ok {
		t.Error("memory.json 缺失时必须放行 (首次运行)")
	}
}

// TestMemoryGuard_EscapeHatch V2: 逃生通道 —— 否则一次旁路即把自己永久锁死
// (修留痕本身要跑 gate, 而 gate 正被这条判据拒)。
func TestMemoryGuard_EscapeHatch(t *testing.T) {
	hints := memoryGuardFixHints()
	if len(hints) < 3 {
		t.Fatalf("逃生通道特征表被清空/缩减到 %d 条: %v", len(hints), hints)
	}
	d := memGuardFixture(t, false)
	for _, h := range hints {
		ok, hit, why := checkMemoryGuard(d, "print('"+h+"')")
		if !ok {
			t.Errorf("含 %q 的代码被拒 => 死锁: %s", h, why)
		}
		if hit != h {
			t.Errorf("命中名 = %q; 期望 %q (审计须能看出走了哪条通道)", hit, h)
		}
	}
}

// TestMemoryGuard_TextHasWayOut V5: 拒绝文本必须自带出路(拒绝 > 猜测)。
func TestMemoryGuard_TextHasWayOut(t *testing.T) {
	if !strings.Contains(memoryGuardText, "memory_"+"write.py") {
		t.Error("拒绝文本缺出口工具 (拒绝必须自带出路, 否则只会诱发绕行)")
	}
	if !strings.Contains(memoryGuardText, "FORGE_MEMORY_GUARD") {
		t.Error("拒绝文本缺回滚开关")
	}
	if !strings.Contains(memoryGuardText, "memory_"+"writes.jsonl") {
		t.Error("拒绝文本缺留痕文件说明")
	}
}

// TestMemoryGuard_WiredInBuild V4: 接线必须前置于执行(只报不拦 = 软约束)。
// 用 AST 扫描而非字符串匹配 —— 注释里提及不算接线。
func TestMemoryGuard_WiredInBuild(t *testing.T) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "forge.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 forge.go 失败: %v", err)
	}
	var buildFn *ast.FuncDecl
	for _, d := range af.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "Build" && fd.Recv != nil {
			buildFn = fd
		}
	}
	if buildFn == nil {
		t.Fatal("forge.go 里找不到 (*Forge).Build")
	}
	var guardPos, retryPos token.Pos
	ast.Inspect(buildFn, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
			if se.Sel.Name == "preflightDeny" && guardPos == 0 {
				guardPos = ce.Pos()
			}
			if se.Sel.Name == "retryGate" && retryPos == 0 {
				retryPos = ce.Pos()
			}
		}
		return true
	})
	if guardPos == 0 {
		t.Fatal("Build 未接线 preflightDeny —— 判据只在 /memhealth 显示 = 软约束")
	}
	if retryPos == 0 {
		t.Fatal("找不到 retryGate 调用点 (接线顺序判据失去参照)")
	}
	if guardPos > retryPos {
		t.Error("记忆护栏必须前置于执行: 拒绝要发生在 retryGate 之前")
	}
	// 第二段: 入口必须收拢记忆臂 (preflight.go)
	af2, err := parser.ParseFile(fset, "preflight.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 preflight.go 失败: %v", err)
	}
	entryCallsArm := false
	for _, d := range af2.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "memoryGuardDeny" {
				if fd.Name.Name == "preflightDeny" {
					entryCallsArm = true
				}
			}
			return true
		})
	}
	if !entryCallsArm {
		t.Error("preflightDeny 未收拢 memoryGuardDeny —— 入口漏臂(判据仍在 /memhealth 显示)")
	}
	// 第三段: 执行臂内部必须真的调 checkMemoryGuard (接线不能被换成空壳)
	af3, err := parser.ParseFile(fset, "memory_guard.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 memory_guard.go 失败: %v", err)
	}
	armCallsCheck := false
	for _, d := range af3.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "memoryGuardDeny" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			if ce, ok := n.(*ast.CallExpr); ok {
				if id, ok := ce.Fun.(*ast.Ident); ok && id.Name == "checkMemoryGuard" {
					armCallsCheck = true
				}
			}
			return true
		})
	}
	if !armCallsCheck {
		t.Error("memoryGuardDeny 未调用 checkMemoryGuard —— 执行臂被换成空壳")
	}
}

// TestMemoryGuard_BuildRejectsBypassEndToEnd V6 反例: 旁路版本必拒, 且代码不得执行。
func TestMemoryGuard_BuildRejectsBypassEndToEnd(t *testing.T) {
	d := memGuardFixture(t, false)
	f := memGuardForge(d)
	out, res, err := f.Build(`print("SHOULD-NOT-RUN")`, "python", "")
	if err != nil {
		t.Fatalf("拒绝不应产生 error (避免触发失败重试链): %v", err)
	}
	if res == nil || res.OK {
		t.Fatalf("旁路版本未被拒: %+v", res)
	}
	if res.Stage != "rejected" {
		t.Errorf("stage = %q; 期望 rejected", res.Stage)
	}
	if strings.Contains(out, "SHOULD-NOT-RUN") {
		t.Errorf("被拒代码竟然执行了:\n%s", out)
	}
	if !strings.Contains(out, "memory_"+"write.py") {
		t.Errorf("拒绝回执缺修复路径:\n%s", out)
	}
}

// TestMemoryGuard_BuildAllowsTracedEndToEnd V6 正例: 合规版本必放行(防误伤)。
func TestMemoryGuard_BuildAllowsTracedEndToEnd(t *testing.T) {
	d := memGuardFixture(t, true)
	f := memGuardForge(d)
	out, res, err := f.Build(`print("ok-memguard")`, "python", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("合规版本被拦(误伤): err=%v res=%+v\n%s", err, res, out)
	}
	if !strings.Contains(out, "ok-memguard") {
		t.Errorf("代码未被执行:\n%s", out)
	}
}

// TestMemoryGuard_BuildSwitchOff V3: 开关关闭时行为与引入前逐字节一致(放行)。
func TestMemoryGuard_BuildSwitchOff(t *testing.T) {
	t.Setenv("FORGE_MEMORY_GUARD", "0")
	if memoryGuardEnabled() {
		t.Fatal("FORGE_MEMORY_GUARD=0 未生效")
	}
	d := memGuardFixture(t, false)
	f := memGuardForge(d)
	out, res, err := f.Build(`print("ok-switch-off")`, "python", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("开关关闭后仍被拦: err=%v res=%+v\n%s", err, res, out)
	}
	if !strings.Contains(out, "ok-switch-off") {
		t.Errorf("代码未被执行:\n%s", out)
	}
}
