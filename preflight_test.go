package main

// preflight_test.go — 前置拒绝权统一入口的哨兵 (20261003)
//
// 为什么要有这个文件: 抽出 preflight.go 后, 若某条拒绝臂在统一入口里被漏调,
// 单条臂的测试仍会全绿(它们直接测各自的 Deny 函数) —— 必须有一条判据钉住"入口收全了"。
//
// 变异自检: 把 preflightDeny 里的 netRedlineDeny 调用删掉, 本文件报红。

import (
	"strings"
	"testing"
)

// TestPreflight_WiresBothArms 入口必须同时收拢两条拒绝臂。
func TestPreflight_WiresBothArms(t *testing.T) {
	f := memGuardForge(t.TempDir())
	// 臂一: GitHub 红线 (凭据进镜像 URL)
	const m = "gh-proxy.com"
	bad := `u = "https://user:pass@` + m + `/https://github.com/o/r"`
	out, res, denied := f.preflightDeny(bad, "python")
	if !denied || res == nil || res.Stage != "rejected" {
		t.Fatalf("红线臂未在入口生效: denied=%v res=%+v\n%s", denied, res, out)
	}
	if !strings.Contains(out, "红线") {
		t.Errorf("红线拒绝回执异常:\n%s", out)
	}
}

// TestPreflight_MemoryArmReachable 记忆臂必须能从入口到达(否则判据只在 /memhealth 显示)。
func TestPreflight_MemoryArmReachable(t *testing.T) {
	d := memGuardFixture(t, false)
	f := memGuardForge(d)
	out, res, denied := f.preflightDeny(`print("x")`, "python")
	if !denied || res == nil || res.Stage != "rejected" {
		t.Fatalf("记忆臂未在入口生效: denied=%v res=%+v\n%s", denied, res, out)
	}
	if !strings.Contains(out, "记忆写入护栏") {
		t.Errorf("记忆拒绝回执异常:\n%s", out)
	}
}

// TestPreflight_NormalCodePasses 正常代码必须放行(防误伤: 误伤会让人关掉整个入口)。
func TestPreflight_NormalCodePasses(t *testing.T) {
	d := memGuardFixture(t, true)
	f := memGuardForge(d)
	out, res, denied := f.preflightDeny(`print("ok-preflight")`, "python")
	if denied || res != nil {
		t.Fatalf("正常代码被入口拦下(误伤): denied=%v res=%+v\n%s", denied, res, out)
	}
}

// TestPreflight_BuildStillDenies 端到端: Build 仍必须拒绝(入口抽出不能断链)。
func TestPreflight_BuildStillDenies(t *testing.T) {
	d := memGuardFixture(t, false)
	f := memGuardForge(d)
	out, res, err := f.Build(`print("SHOULD-NOT-RUN")`, "python", "")
	if err != nil {
		t.Fatalf("拒绝不应产生 error: %v", err)
	}
	if res == nil || res.OK {
		t.Fatalf("Build 未拒绝旁路版本: %+v", res)
	}
	if strings.Contains(out, "SHOULD-NOT-RUN") {
		t.Errorf("被拒代码竟然执行了:\n%s", out)
	}
}
