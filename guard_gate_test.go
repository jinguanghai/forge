package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// guard_gate_test.go —— 闸门一「git 权威判定」的行为哨兵
//
// 缺陷 (20260928 实测复现, 见 forge_guard.py 的 untracked 分支注释):
//   新建源文件 -> check 的盲区检测把它自动纳入基线 -> 继续修改该文件
//   -> git_state 返回 untracked -> 旧版无此分支 -> 落到「[!!] 篡改/变更」
//   -> issues+1 -> 触发自愈臂 -> 有抹掉新代码的风险
//   (与 20260910「自愈臂吃掉新代码」同一条路径)。
//
// 本文件端到端跑真实脚本(不 mock), 把三条行为钉死:
//   1. 未跟踪文件被修改     -> 判「开发中」, 不得报「篡改/变更」
//   2. 未跟踪且 == 历史快照 -> 仍判「回滚污染」, 防线不得被削弱
//   3. git 不可用           -> 仍判「篡改/变更」, 防「一律放过」的反向退化
//
// 判据用日志关键词而非返回码: check() 末尾还要跑 deadcheck(go vet),
// 临时仓库无 go.mod 时 vet 必失败 -> rc 恒为 1, 拿 rc 判完整性会被污染。
// ============================================================================

const (
	guardGatePy  = "defense_system/forge_guard.py"
	guardGateAux = "defense_system/_winquiet.py"
	// watchlist.py 是 forge_guard.py 的【必需依赖】(单一数据源, 20260928):
	// 夹具缺它 -> `from watchlist import WATCH` 失败 -> 脚本启动即崩 ->
	// 三个用例全红。故必须一并复制。
	guardGateWatch = "defense_system/watchlist.py"
	guardGateSrcV1 = "package main\n\nfunc main() {}\n\n// v1\n"
)

// guardGateRepo 是端到端夹具: 一个含 forge_guard.py 副本的临时 git 仓库。
type guardGateRepo struct {
	base  string // 仓库根 (脚本由 __file__ 上溯两级得到 BASE, 故必须这样摆)
	guard string // 脚本绝对路径
}

func guardGateNew(t *testing.T) guardGateRepo {
	t.Helper()
	base := t.TempDir()
	ds := filepath.Join(base, "defense_system")
	if err := os.MkdirAll(ds, 0o755); err != nil {
		t.Fatalf("建临时 defense_system 失败: %v", err)
	}
	for _, rel := range []string{guardGatePy, guardGateAux, guardGateWatch} {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("读取 %s 失败 (fail-closed, 不放行): %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(ds, filepath.Base(rel)), raw, 0o644); err != nil {
			t.Fatalf("写临时 %s 失败: %v", rel, err)
		}
	}
	r := guardGateRepo{base: base, guard: filepath.Join(ds, "forge_guard.py")}
	r.write(t, guardGateSrcV1)
	r.git(t, "init", "-q") // 只 init 不 add -> probe.go 保持「未跟踪」
	return r
}

func (r guardGateRepo) write(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.base, "probe.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("写探针源文件失败: %v", err)
	}
}

func (r guardGateRepo) git(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.base
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v (%s)", args, err, out)
	}
}

func (r guardGateRepo) run(t *testing.T, mode string) string {
	t.Helper()
	cmd := exec.Command(guardGatePython(), r.guard, mode)
	cmd.Dir = r.base
	cmd.Env = pythonUTF8Env()
	out, _ := cmd.CombinedOutput() // 非零退出可能是 deadcheck 的 go vet, 故忽略
	return string(out)
}

func guardGatePython() string {
	if p := os.Getenv("PYTHON"); p != "" {
		return p
	}
	return "python"
}

// pythonUTF8Env 是所有 python 子进程的统一环境: 强制 UTF-8 输出 + 不留 __pycache__。
//
// 缺口(20261002, 事故同源): 原先各测试只设 PYTHONDONTWRITEBYTECODE。Windows 上
// python 的 stdout 被重定向(非终端)时按系统代码页(cp936/GBK)编码, 而 Go 侧判据是
// UTF-8 字面量 —— 中文经 json.Unmarshal 会被替换成 U+FFFD, strings.Contains 永远
// 假: 4 个测试长期假红(TestRedcardSimRealRun / TestBenchReportSelftest /
// TestVersionCheckSelftest / TestRelationGateFailsClosedOnEmptyRoot)。
// 后果不是"4 条测试难看", 而是红灯可信度掉到 20% —— 与「19 条告警 18 条误报」
// 是同一个病: 判据与语义脱节, 于是人和自动化都学会忽略红灯。
// (生产侧同一修法见 main_startup.go 的 sense.py 调用。)
func pythonUTF8Env() []string {
	return append(os.Environ(),
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONIOENCODING=utf-8",
		"PYTHONUTF8=1",
	)
}

// guardGateLines 摘出判定相关日志行, 让失败信息可读(整份日志太长)。
func guardGateLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "[GATE-GIT]") || strings.Contains(l, "[!!]") ||
			strings.Contains(l, "[CHECK]") {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	return strings.Join(keep, "\n")
}

// TestGuardGateUntrackedIsNotTamper 钉住修复: 未跟踪文件被修改不得判篡改。
// 变异体: 删掉 forge_guard.py 的 untracked 分支 -> 本用例必须报红。
func TestGuardGateUntrackedIsNotTamper(t *testing.T) {
	t.Parallel()
	r := guardGateNew(t)
	r.run(t, "init") // 基线吸收 probe.go (模拟 check 的盲区检测)
	r.write(t, "package main\n\nfunc main() {}\n\n// v2 changed\n")
	out := r.run(t, "check")
	if !strings.Contains(out, "未被 git 跟踪") {
		t.Errorf("未走 untracked 分支 (期望日志含「未被 git 跟踪」):\n%s", guardGateLines(out))
	}
	if strings.Contains(out, "篡改/变更") {
		t.Errorf("未跟踪文件被误判为篡改 -> 会触发自愈臂抹掉新代码:\n%s", guardGateLines(out))
	}
	if !strings.Contains(out, "全部文件完好") {
		t.Errorf("完整性判定未通过:\n%s", guardGateLines(out))
	}
}

// TestGuardGateRollbackStillCaught 钉住防线: 未跟踪 + 内容==历史快照 仍须报红。
// 若把 untracked 分支简化成「无条件放过」, 本用例必须报红。
func TestGuardGateRollbackStillCaught(t *testing.T) {
	t.Parallel()
	r := guardGateNew(t)
	r.run(t, "init")
	body := "package main\n\nfunc main() {}\n\n// rolled back\n"
	r.write(t, body)
	snap := filepath.Join(r.base, "defense_system", "snapshots", "snap_20260101_000000")
	if err := os.MkdirAll(snap, 0o755); err != nil {
		t.Fatalf("建快照目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(snap, "probe.go"), []byte(body), 0o644); err != nil {
		t.Fatalf("写快照失败: %v", err)
	}
	out := r.run(t, "check")
	if !strings.Contains(out, "回滚污染") {
		t.Errorf("未跟踪 + 内容==历史快照 必须仍判回滚污染 (防线被削弱):\n%s", guardGateLines(out))
	}
}

// TestGuardGateNoGitStillCaught 反向哨兵: git 不可用时必须保守判篡改。
// 防止修复被写成「凡是 git 说不清的一律放过」—— 那会把真篡改一起放过去。
func TestGuardGateNoGitStillCaught(t *testing.T) {
	t.Parallel()
	r := guardGateNew(t)
	r.run(t, "init")
	if err := os.RemoveAll(filepath.Join(r.base, ".git")); err != nil {
		t.Fatalf("移除 .git 失败: %v", err)
	}
	r.write(t, "package main\n\nfunc main() {}\n\n// tampered\n")
	out := r.run(t, "check")
	if !strings.Contains(out, "篡改/变更") {
		t.Errorf("git 不可用时必须保守判篡改 (防「一律放过」反向退化):\n%s", guardGateLines(out))
	}
}
