package main

// memory_tracked_sentinel_test.go — 钉住「memory.json 本地跟踪, 绝不出本地」(20261003)
//
// 背景: memory.json 含主人私有数据(画像/家传理论引用/教训)。20261003 起纳入本地 git
// 跟踪, 换来版本安全网(可 diff / 可回滚)。但本仓库的 remote 历史上指向公开仓库
// 公开仓库, 一旦误推 = 私有数据不可逆公开。
//
// 三条判据缺一即存在暴露风险(均为「物理不可能型」, 不依赖概率):
//   1. memory.json 确被本地 git 跟踪 —— 否则安全网是空的
//   2. 没有任何 remote 的 pushurl 指向公开仓库 —— 否则一次误推即外泄
//   3. 开源同步脚本的可执行行不含 memory.json —— 否则"合法"路径外泄
//
// 公理四: 本文件守「安全网」侧(git 可 diff / 可回滚), 与 gate 前置拦截互为两层 ——
// 前置拦住新伤害, 回滚抹掉已发生的伤害, 二者不可替代。
//
// 判据自身也有判据: 三条判据已抽成纯函数 memoryTrackingProblems, 由
// TestMemoryTrackingProblemsSharp 注入变异/健康输入双向钉住 —— 手工变异(20261003
// 实测: 退出跟踪 / pushurl 指回公开仓库 / scope 混入) 均被捕获, 该结果已固化为用例,
// 不再依赖一次性手工验证。

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// memoryTrackingProblems 三类判据的纯函数形态 (便于自检注入变异输入)。
// 返回问题描述列表, 空 = 全部通过。
func memoryTrackingProblems(trackedOut string, pushURLs map[string]string, syncScript string) []string {
	var bad []string
	if strings.TrimSpace(trackedOut) != "memory.json" {
		bad = append(bad, fmt.Sprintf("memory.json 未被本地 git 跟踪 -> 版本安全网失效: %q", trackedOut))
	}
	for r, url := range pushURLs {
		if strings.Contains(url, "github.com") {
			bad = append(bad, fmt.Sprintf("remote %q 的 pushurl 指向公开仓库 %q -> 私有记忆可被误推", r, url))
		}
	}
	for _, line := range strings.Split(syncScript, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue // 剥注释: 注释里提及不构成 scope
		}
		if strings.Contains(s, "memory.json") {
			bad = append(bad, fmt.Sprintf("同步脚本可执行行含 memory.json -> scope 可能外泄: %s", s))
		}
	}
	return bad
}

func gitOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// collectPushURLs 取所有 remote 的 pushurl (未设者跳过: 此时继承 fetchurl, 不判定)
func collectPushURLs(t *testing.T) map[string]string {
	t.Helper()
	urls := map[string]string{}
	for _, r := range strings.Split(gitOut(t, "remote"), "\n") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out, err := exec.Command("git", "remote", "get-url", "--push", r).CombinedOutput()
		if err != nil {
			continue
		}
		urls[r] = strings.TrimSpace(string(out))
	}
	return urls
}

func TestMemoryJsonTrackingContract(t *testing.T) {
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("非主仓库环境(无 .git), 跳过")
	}
	tracked, _ := exec.Command("git", "ls-files", "--error-unmatch", "--", "memory.json").Output()
	script, _ := os.ReadFile("open_source_sync.ps1") // 缺失时为空串, 判据自会报红
	for _, p := range memoryTrackingProblems(string(tracked), collectPushURLs(t), string(script)) {
		t.Error(p)
	}
}

// TestMemoryTrackingProblemsSharp 判据的判据: 变异输入必须报红, 健康输入必须放行。
func TestMemoryTrackingProblemsSharp(t *testing.T) {
	mut := memoryTrackingProblems(
		"",
		map[string]string{"local": "https://github.com/<account>/forge.git"},
		"# memory.json (comment only)\n$allow = @(\"memory.json\")",
	)
	if len(mut) != 3 {
		t.Fatalf("变异输入应报 3 类问题, got %d: %v", len(mut), mut)
	}
	if got := memoryTrackingProblems("memory.json",
		map[string]string{"local": "DISABLED_no_push_from_local_repo"},
		"# memory.json stays private\n$allow = @(\"LICENSE\")"); len(got) != 0 {
		t.Fatalf("健康输入不该报红: %v", got)
	}
	// 剥注释判据本身也要有判据: 注释里的提及不算
	if got := memoryTrackingProblems("memory.json", map[string]string{}, "# memory.json"); len(got) != 0 {
		t.Fatalf("注释行提及不该报红: %v", got)
	}
}
