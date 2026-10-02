package main

// cov_verify_smoke_test.go — 自检/冒烟执行器补测 (20260920)
//
// 两个零覆盖函数都是"判定"型死程序:
//   runVerification  — /verify 的证据采集 (git 状态 + go vet), 只读命令
//   realSmokeRunner  — 自升级就位前的冒烟: 必须能把"进程起不来"与"进程起了但退出码非0"
//                      区分开 (前者 err!=nil, 后者 code 带回), 否则冒烟判据失效。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runVerification 采集三类证据且只读 (不修改仓库状态)。
func TestRunVerification_CollectsReadOnlyEvidence(t *testing.T) {
	got := runVerification()
	if strings.TrimSpace(got) == "" {
		t.Fatalf("验证输出不得为空")
	}
	if got != "(无输出)" && !strings.Contains(got, "[") {
		t.Fatalf("输出应含带方括号的命令证据段: %q", got)
	}
}

// 冒烟分支一: 可执行文件不存在 → 必须报错 (err != nil), 而非静默成功。
func TestRealSmokeRunner_MissingExe(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely_not_here.exe")
	code, out, err := realSmokeRunner(missing)
	if err == nil {
		t.Fatalf("缺失的可执行文件必须报错 (否则冒烟判据失效)")
	}
	if code != -1 {
		t.Fatalf("非 ExitError 应返回 -1, 实际 %d", code)
	}
	if out != "" {
		t.Fatalf("未启动进程不应有输出: %q", out)
	}
}

// 冒烟分支二: 进程起来了但退出码非 0 → 必须带回退出码且 err == nil
// (自升级的"冒烟失败"判定正建立在此区分上)。用测试二进制自身:
// testing 包对未知 flag 会打印用法并以 2 退出。
func TestRealSmokeRunner_ExitErrorCarriesCode(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Skipf("拿不到测试二进制路径: %v", err)
	}
	code, out, rerr := realSmokeRunner(self)
	if rerr != nil {
		t.Fatalf("已启动的进程不应报启动错误: %v (out=%q)", rerr, out)
	}
	if code == 0 {
		t.Fatalf("测试二进制收到未知 flag --version 应非 0 退出, 实际 0")
	}
	if !strings.Contains(out, "version") {
		t.Fatalf("应捕获到子进程输出 (flag 报错), 实际 %q", out)
	}
}

// 冒烟分支三: 退出码 0 → (0, 输出, nil)。python --version 是稳定来源。
func TestRealSmokeRunner_SuccessPath(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skipf("环境无 python, 跳过成功路径: %v", err)
	}
	code, out, rerr := realSmokeRunner(py)
	if rerr != nil {
		t.Fatalf("python --version 应可执行: %v", rerr)
	}
	if code != 0 {
		t.Fatalf("python --version 应退出 0, 实际 %d (out=%q)", code, out)
	}
	if !strings.Contains(strings.ToLower(out), "python") {
		t.Fatalf("应捕获到版本输出, 实际 %q", out)
	}
}
