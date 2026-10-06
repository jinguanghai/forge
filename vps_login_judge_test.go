package main

// ============================================================================
// vps_login_judge_test.go —— VPS 登录判据的「判据自己制造误报」回归哨兵 (20261005)
//
// 事故 (现场核对): PII purge 把白名单抹空后, 「非白名单登录」成了逻辑必然 ——
//   主人自己的 29 次登录被报成 high「真入侵」。缺陷形态是「判据自己制造误报」:
//   它不会让任何既有测试变红, 只有把「空白名单不得产生 rogue」写成断言才拦得住。
//
// 三层判据 (全部端到端跑真实脚本, 不 mock):
//   1. selftest 全绿 (判定三分支 + 告警级别 warn + 恢复闭环)
//   2. 源码结构钉死 (judge 纯函数 + invalid 不变式 + warn 告警出口)
//   3. 鉴别力: 把 invalid 分支打穿 -> selftest 必须报红
//      (「判据全绿」不等于「判据有效」—— 唯一证据是改坏生产代码后判据报红)
// ============================================================================

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const vpsLoginRel = "defense_system/vps_login_check.py"

// vpsLoginRun 跑脚本并返回 (rc, 合并输出)。告警出口/去重状态用环境变量重定向到临时
// 目录 —— 判据跑出真违规时不得把测试噪声写进生产 alerts.jsonl。
func vpsLoginRun(t *testing.T, script string, args ...string) (int, string) {
	t.Helper()
	py, err := exec.LookPath(guardGatePython())
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	dir := t.TempDir()
	cmd := exec.Command(py, append([]string{script}, args...)...)
	cmd.Env = append(pythonUTF8Env(),
		"FORGE_ALERTS_PATH="+filepath.Join(dir, "alerts.jsonl"),
		"FORGE_VPS_LOGIN_STATE="+filepath.Join(dir, "state.json"))
	out, rerr := cmd.CombinedOutput()
	if rerr == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(rerr, &ee) {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("执行 %s 失败: %v (%s)", script, rerr, out)
	return -1, ""
}

// stripPyComments 剥掉 Python 注释 —— 判据只认代码行, 注释里提及不算实现
// (同型教训: hourly 接线哨兵用全文件 Contains 会漏检「删代码留注释」)。
func stripPyComments(src string) string {
	var b strings.Builder
	for _, ln := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	return b.String()
}

// TestVPSLoginJudgeSelftest 判定逻辑自证 (纯函数三分支 + 告警级别 + 恢复闭环)。
func TestVPSLoginJudgeSelftest(t *testing.T) {
	rc, out := vpsLoginRun(t, vpsLoginRel, "--selftest")
	if rc != 0 {
		t.Fatalf("判定自检应通过 (rc=0), 实际 rc=%d:\n%s", rc, out)
	}
	if !strings.Contains(out, "'ok': True") || !strings.Contains(out, "'fail': 0") {
		t.Errorf("自检输出缺少通过标记:\n%s", out)
	}
}

// TestVPSLoginJudgeInvariantInSource 钉死修复的锚点: 判据被回退 -> 测试红。
func TestVPSLoginJudgeInvariantInSource(t *testing.T) {
	raw, err := os.ReadFile(vpsLoginRel)
	if err != nil {
		t.Fatalf("读取失败 (fail-closed): %v", err)
	}
	code := stripPyComments(string(raw))
	wants := []string{
		"def judge(accepted, wl_ips):", // 判定抽成纯函数 (死程序可直接验证)
		"return 'invalid', []",         // 不变式: 白名单空时 rogue 恒为空
		"emit_judge_invalid(",          // 判据失效告警出口存在
		"'metric': 'vps_login_nowl'",   // 失效告警的独立 metric (不混进真入侵)
		"'sev': 'warn'",                // 失效是 warn, 不是 high
		"empty_wl_invalid_no_rogue",    // 回归判据向量本身
	}
	for _, w := range wants {
		if !strings.Contains(code, w) {
			t.Errorf("判据被回退: 源码(剥注释后)缺少 %q —— 空白名单误报缺陷会复发", w)
		}
	}
	// 反向断言: 真入侵告警臂必须还在 (修复不得把真判据一起关掉)
	if !strings.Contains(code, "'metric': 'vps_login_rogue'") {
		t.Error("真入侵告警臂 (vps_login_rogue) 缺失 —— 修复把真判据一起关掉了")
	}
}

// TestVPSLoginJudgeMutationDetected 鉴别力: 打穿 invalid 分支 -> 自检必须报红。
// 复制三个互相依赖的文件到临时目录: 脚本用 __file__ 推导 BASE 并 import 同目录模块。
func TestVPSLoginJudgeMutationDetected(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{vpsLoginRel, "defense_system/vps_env.py",
		"defense_system/alert_sink.py"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatalf("写 %s 失败: %v", f, err)
		}
	}
	mut := filepath.Join(dir, filepath.Base(vpsLoginRel))
	b, err := os.ReadFile(mut)
	if err != nil {
		t.Fatalf("读取变异副本失败: %v", err)
	}
	broken := strings.Replace(string(b),
		"    if not wl_ips:\n        return 'invalid', []\n",
		"    if False:\n        return 'invalid', []\n", 1)
	if broken == string(b) {
		t.Fatal("变异未命中 —— 判据锚点漂移, 本用例已失效 (fail-closed)")
	}
	if err := os.WriteFile(mut, []byte(broken), 0o644); err != nil {
		t.Fatalf("写变异副本失败: %v", err)
	}
	rc, out := vpsLoginRun(t, mut, "--selftest")
	if rc == 0 {
		t.Errorf("把 invalid 分支打穿后自检仍全绿 —— 判据没有鉴别力:\n%s", out)
	}
}
