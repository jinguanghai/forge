package main

// verify_claim_cwd_test.go — 完成态核验的 cwd 无关性判据 (20261007)
//
// 由来: verifyClaim.go 的 runVerification 曾用裸 exec.Command 且不设 cmd.Dir,
// 而全部 gate 子进程都经 newCmd 绑定 workDir —— 它是"唯一绕过统一入口"的那条命令。
// 从仓库外启动时 git/go 全数失败 (实测 rc=128/128/1), 错误文本被当作"模型虚报"
// 的反证注入上下文, 反过来诱导模型去修正并不存在的错误。
//
// workdir_binding_test.go 的判据只钉住 newCmd 本身, 钉不住"绕过 newCmd 的裸调用";
// 本文件补上这一条, 用行为判据而非结构扫描。
//
// 判据(因): cwd 换成空目录后, runVerification(仓库根) 仍须拿到真实证据。
// 对照(果): 同一 cwd 下 runVerification("") 必须失败 —— 证明判据钉的是
// "绑定 workDir"这个因, 而不是恒真的果 (变异自检)。

import (
	"os"
	"strings"
	"testing"
)

// TestRunVerification_IndependentOfProcessCwd: 核验命令不得依赖进程 cwd。
func TestRunVerification_IndependentOfProcessCwd(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	chdirTemp(t, foreign)

	got := runVerification(repoRoot)
	// 失败形态示例: "[git log --oneline -5] exit status 128; " / "[go vet .] exit status 1; "
	for _, bad := range []string{"[git log", "[git status", "[go vet"} {
		if strings.Contains(got, bad) {
			t.Fatalf("绑定 workDir 后核验仍在仓库外失败 (缺陷复发), 命中 %q: %s", bad, got)
		}
	}
	if !strings.Contains(got, "[git]") {
		t.Fatalf("未采集到 git 证据段 (成功段形如 [git] <hash>): %s", got)
	}
}

// TestRunVerification_EmptyWorkDirFailsFromForeignCwd: 变异自检 ——
// 把 workDir 退回空值 (即修复前的行为), 判据必须能看见失败。
func TestRunVerification_EmptyWorkDirFailsFromForeignCwd(t *testing.T) {
	foreign := t.TempDir()
	chdirTemp(t, foreign)
	got := runVerification("")
	if !strings.Contains(got, "[git log") {
		t.Fatalf("对照失效: 空 workDir 在仓库外竟未失败 (%s) —— 上一条判据可能恒真", got)
	}
}
