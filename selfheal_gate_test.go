package main

// ============================================================================
// selfheal_gate_test.go —— 自愈臂「删除也是演进」闸门的行为哨兵 (20261005)
//
// 缺陷 (现场核对后确认, 见 selfheal.py 对应分支注释):
//   ① selfheal.py 的 MISSING 分支从不问 git —— 已提交的文件删除被永久判为
//      「文件丢失」, 而恢复臂只认 ROLLED_BACK -> 落到快照分支 -> 快照里没有
//      -> [NO-SNAP] skipped -> 每次巡检都报红且永不自愈。
//      guard 侧早有 is_deleted_in_head(110 行), selfheal 侧没有 -> 两个脚本
//      对同一事实给出相反判定。
//   ② snapshot_too_old 对不存在的文件调 os.path.getmtime(fp) -> 抛
//      FileNotFoundError 冒泡出 selfheal(), 整个自愈循环在到达恢复逻辑之前被打断。
//
// 端到端跑真实脚本(不 mock), 钉住三条行为:
//   1. 已提交文件被删且未提交删除 -> 用 git HEAD 版本恢复, rc=0 (真异常必须治)
//   2. 删除已提交                  -> 判正常演进, 不恢复, 且从基线移除
//   3. doctor 对已提交删除         -> 判健康, 不报「丢失」(两入口同构)
//
// 为什么不是 Python 自检: 行为与接线是包级属性, 必须进 go test 回归才有人跑。
// ============================================================================

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	selfhealPyFile   = "defense_system/selfheal.py"
	selfhealAuxFile  = "defense_system/_winquiet.py"
	selfhealWatch    = "defense_system/watchlist.py"
	selfhealBaseline = "defense_system/forge_baseline.json"
	selfhealMainSrc  = "package main\n\nfunc main() {}\n"
	selfhealProbeSrc = "package main\n\nfunc probe() {}\n\n// v1\n"
)

// selfhealRepo 是端到端夹具: 一个含 selfheal.py 副本的临时 git 仓库。
// 脚本由 __file__ 上溯两级得到 BASE, 故副本必须摆在 defense_system/ 下。
type selfhealRepo struct {
	base string
	py   string
}

func (r selfhealRepo) path(rel string) string {
	return filepath.Join(r.base, filepath.FromSlash(rel))
}

func (r selfhealRepo) git(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.base
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v (%s)", args, err, out)
	}
}

func (r selfhealRepo) commit(t *testing.T, msg string) {
	t.Helper()
	r.git(t, "add", "-A")
	r.git(t, "-c", "user.email=t@example.com", "-c", "user.name=t",
		"commit", "-q", "-m", msg)
}

func (r selfhealRepo) run(t *testing.T, mode string) (int, string) {
	t.Helper()
	cmd := exec.Command(guardGatePython(), r.py, mode)
	cmd.Dir = r.base
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("执行 selfheal.py %s 失败: %v (%s)", mode, err, out)
	return -1, ""
}

// selfhealNew 建沙箱。为什么必须带 go.mod/main.go: selfheal 的康复验证是
// go build, 沙箱编译不过 -> 「恢复成功」被误判为 rc=1 -> 判据恒红(假红)。
// 夹具必须自洽, 否则测的是夹具不是产品。
func selfhealNew(t *testing.T) selfhealRepo {
	t.Helper()
	if testing.Short() {
		t.Skip("沙箱要跑 git + go build (秒级), quick 档跳过")
	}
	base := t.TempDir()
	ds := filepath.Join(base, "defense_system")
	if err := os.MkdirAll(ds, 0o755); err != nil {
		t.Fatalf("建临时 defense_system 失败: %v", err)
	}
	for _, rel := range []string{selfhealPyFile, selfhealAuxFile, selfhealWatch} {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("读取 %s 失败 (fail-closed, 不放行): %v", rel, err)
		}
		if err := os.WriteFile(filepath.Join(ds, filepath.Base(rel)), raw, 0o644); err != nil {
			t.Fatalf("写临时 %s 失败: %v", rel, err)
		}
	}
	r := selfhealRepo{base: base, py: filepath.Join(ds, "selfheal.py")}
	for name, body := range map[string]string{
		"go.mod":   "module sandbox\n\ngo 1.21\n",
		"main.go":  selfhealMainSrc,
		"probe.go": selfhealProbeSrc,
	} {
		if err := os.WriteFile(r.path(name), []byte(body), 0o644); err != nil {
			t.Fatalf("写 %s 失败: %v", name, err)
		}
	}
	r.git(t, "init", "-q")
	// core.autocrlf=false: 否则 git checkout 按 CRLF 检出, 「逐字节恢复」判据恒假。
	r.git(t, "config", "core.autocrlf", "false")
	r.commit(t, "init")
	return r
}

// baselineKeys 读回基线条目名 (判「基线是否跟着删除前进」)。
func (r selfhealRepo) baselineKeys(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(r.path(selfhealBaseline))
	if err != nil {
		t.Fatalf("读基线失败: %v", err)
	}
	var bl map[string]json.RawMessage
	if err := json.Unmarshal(raw, &bl); err != nil {
		t.Fatalf("基线解析失败: %v", err)
	}
	out := map[string]bool{}
	for k := range bl {
		out[k] = true
	}
	return out
}

// TestSelfhealRestoresUncommittedDeletion 钉住真异常: HEAD 里还在、工作区没了 -> 必须恢复。
func TestSelfhealRestoresUncommittedDeletion(t *testing.T) {
	r := selfhealNew(t)
	if rc, out := r.run(t, "snapshot"); rc != 0 {
		t.Fatalf("建立健康快照应成功, 实际 rc=%d:\n%s", rc, out)
	}
	if err := os.Remove(r.path("probe.go")); err != nil {
		t.Fatalf("删除 probe.go 失败: %v", err)
	}
	rc, out := r.run(t, "selfheal")
	if rc != 0 {
		t.Errorf("未提交的删除应被自愈康复 (rc=0), 实际 rc=%d:\n%s", rc, out)
	}
	got, err := os.ReadFile(r.path("probe.go"))
	if err != nil {
		t.Fatalf("probe.go 未被恢复 —— 自愈臂对丢失文件无效: %v\n%s", err, out)
	}
	if string(got) != selfhealProbeSrc {
		t.Errorf("恢复内容与 HEAD 版本不一致:\n got=%q\nwant=%q", got, selfhealProbeSrc)
	}
	if !strings.Contains(out, "[GATE-GIT]") {
		t.Errorf("恢复未经 git 权威路径 (日志无 [GATE-GIT]):\n%s", out)
	}
}

// TestSelfhealAcceptsCommittedDeletion 钉住反向: 删除已提交 = 正常演进 -> 不恢复 + 基线移除。
// 反向用例不可省: 只测「能恢复」会让「一律恢复」这种过度自愈也全绿。
func TestSelfhealAcceptsCommittedDeletion(t *testing.T) {
	r := selfhealNew(t)
	if rc, out := r.run(t, "snapshot"); rc != 0 {
		t.Fatalf("建立健康快照应成功, 实际 rc=%d:\n%s", rc, out)
	}
	if err := os.Remove(r.path("probe.go")); err != nil {
		t.Fatalf("删除 probe.go 失败: %v", err)
	}
	r.commit(t, "del probe")
	rc, out := r.run(t, "selfheal")
	if rc != 0 {
		t.Errorf("已提交的删除是正常演进 (rc 应 0), 实际 rc=%d:\n%s", rc, out)
	}
	if _, err := os.Stat(r.path("probe.go")); err == nil {
		t.Errorf("已提交的删除被恢复 —— 自愈臂把演进当篡改:\n%s", out)
	}
	if !strings.Contains(out, "正常演进") {
		t.Errorf("未走「已不存在于 HEAD」分支 (日志无「正常演进」):\n%s", out)
	}
	if r.baselineKeys(t)["probe.go"] {
		t.Errorf("基线仍留已删除文件 -> 每轮巡检反复报红 (需 remove_from_baseline_files)")
	}
}

// TestSelfhealDoctorIgnoresCommittedDeletion 钉住诊断入口与主循环同构。
func TestSelfhealDoctorIgnoresCommittedDeletion(t *testing.T) {
	r := selfhealNew(t)
	if rc, out := r.run(t, "snapshot"); rc != 0 {
		t.Fatalf("建立健康快照应成功, 实际 rc=%d:\n%s", rc, out)
	}
	if err := os.Remove(r.path("probe.go")); err != nil {
		t.Fatalf("删除 probe.go 失败: %v", err)
	}
	r.commit(t, "del probe")
	rc, out := r.run(t, "doctor")
	if rc != 0 {
		t.Errorf("doctor 对已提交的删除应判健康 (rc=0), 实际 rc=%d:\n%s", rc, out)
	}
	if strings.Contains(out, "[丢失]") {
		t.Errorf("doctor 把正常演进报成文件丢失:\n%s", out)
	}
}
