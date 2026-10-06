package main

// ============================================================================
// open_source_drift_test.go —— 开源仓漂移判据的鉴别力哨兵 (T14, 20261006)
//
// 病根 (实测): 开源仓 D:\forge_open_source 的同步 100% 手动 —— hourly 零调度、
//   watchlist 零监控, 逐文件 sha256 实测 10 个已提交源码文件未进开源仓,
//   而四套守卫全绿 (它们都不看发布面)。新增判据
//   defense_system/open_source_drift.py 之后, 判据自身也要有判据 (公理三)。
//
// 本文件钉住两件事:
//   1. 判据向量 (沙箱化): 漂移 / 缺失 / 白名单 / 空目录 -> 判定必须分别正确。
//      端到端走真实脚本 (不重实现判定), 沙箱靠 FORGE_DRIFT_ROOT / FORGE_DRIFT_OSS;
//      告警出口隔离到 t.TempDir() -> 不污染生产 .forge/alerts.jsonl。
//   2. 鉴别力自检: 脚本 --selftest 必须全绿 (脚本内置向量的第二道锁)。
//   接线 (hourly 第 22 任务 + 必需参数) 由 hourly_wiring_test.go 的判据表钉住 ——
//   本文件只负责「判据本身能不能判」。
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ossDriftPy = "defense_system/open_source_drift.py"

// ossDriftResult 对应脚本 --json 的输出 (字段名即契约)。
type ossDriftResult struct {
	Scope     int      `json:"scope"`
	Same      int      `json:"same"`
	Diff      []string `json:"diff"`
	Missing   []string `json:"missing"`
	Whitelist []string `json:"whitelist"`
	OK        bool     `json:"ok"`
	Gap       int      `json:"gap"`
}

// ossDriftExec 跑脚本, 返回 (stdout, rc)。stdout 与 stderr 分离 ——
// 告警提示行走 stderr, 不得混进 JSON (与 version_check 的 --json 契约一致)。
func ossDriftExec(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(guardGatePython(), append([]string{ossDriftPy}, args...)...)
	cmd.Env = append(pythonUTF8Env(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	rc := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("脚本启动失败: %v (%s)", err, stderr.String())
		}
		rc = ee.ExitCode()
	}
	return stdout.String(), rc
}

// ossSameStrings 顺序敏感的字符串切片相等 (本地名, 避免与 llm_http_test.go 撞名)。
func ossSameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ossDriftSandbox 建 src/dst 沙箱, 跑判据并解析 --json 输出。
func ossDriftSandbox(t *testing.T, srcFiles, dstFiles map[string]string) (ossDriftResult, int) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	for _, d := range []string{src, dst} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("建沙箱目录失败: %v", err)
		}
	}
	write := func(base string, files map[string]string) {
		for n, c := range files {
			if err := os.WriteFile(filepath.Join(base, n), []byte(c), 0o644); err != nil {
				t.Fatalf("写沙箱文件失败: %v", err)
			}
		}
	}
	write(src, srcFiles)
	write(dst, dstFiles)
	env := []string{
		"FORGE_DRIFT_ROOT=" + src,
		"FORGE_DRIFT_OSS=" + dst,
		"FORGE_ALERTS_PATH=" + filepath.Join(dir, "alerts.jsonl"),
		"FORGE_OSSDRIFT_STATE=" + filepath.Join(dir, "state.json"),
	}
	out, rc := ossDriftExec(t, env, "--json")
	var res ossDriftResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json 输出不可解析: %v\n%s", err, out)
	}
	return res, rc
}

// TestOpenSourceDriftVectors 四场景中的三个: 漂移 / 缺失 / 白名单。
func TestOpenSourceDriftVectors(t *testing.T) {
	src := map[string]string{
		"same.go":    "package main\n",
		"changed.go": "package main\n// new\n",
		"added.go":   "package main\n// added\n",
		"go.mod":     "module forge\n",
	}
	dst := map[string]string{
		"same.go":    "package main\n",
		"changed.go": "package main\n",
		"go.mod":     "module github.com/x/forge\n",
	}
	res, rc := ossDriftSandbox(t, src, dst)
	if rc != 1 {
		t.Errorf("有漂移时 rc 应为 1, 实得 %d", rc)
	}
	if !ossSameStrings(res.Diff, []string{"changed.go"}) {
		t.Errorf("diff = %v, want [changed.go] (改了没同步)", res.Diff)
	}
	if !ossSameStrings(res.Missing, []string{"added.go"}) {
		t.Errorf("missing = %v, want [added.go] (新增没同步)", res.Missing)
	}
	if !ossSameStrings(res.Whitelist, []string{"go.mod"}) {
		t.Errorf("whitelist = %v, want [go.mod] (设计性分歧不算漂移)", res.Whitelist)
	}
	if res.OK || res.Gap != 2 {
		t.Errorf("ok/gap 判定错误: ok=%v gap=%d (want false/2)", res.OK, res.Gap)
	}
	if res.Same != 1 {
		t.Errorf("same = %d, want 1", res.Same)
	}
}

// TestOpenSourceDriftEmptyRepo 空开源仓 -> 全部 missing (判据不得静默放行)。
func TestOpenSourceDriftEmptyRepo(t *testing.T) {
	src := map[string]string{"a.go": "package main\n", "b.go": "package main\n"}
	res, rc := ossDriftSandbox(t, src, map[string]string{})
	if rc != 1 {
		t.Errorf("空仓时 rc 应为 1, 实得 %d", rc)
	}
	if len(res.Missing) != 2 || res.Same != 0 {
		t.Errorf("空仓判定错误: missing=%v same=%d (want 2/0)", res.Missing, res.Same)
	}
}

// TestOpenSourceDriftClean 内容一致 -> rc=0 (白名单内容分歧不报)。
func TestOpenSourceDriftClean(t *testing.T) {
	src := map[string]string{"a.go": "package main\n", "go.mod": "module forge\n"}
	dst := map[string]string{"a.go": "package main\n", "go.mod": "module github.com/x/forge\n"}
	res, rc := ossDriftSandbox(t, src, dst)
	if rc != 0 {
		t.Errorf("无漂移时 rc 应为 0, 实得 %d (%v)", rc, res)
	}
	if !res.OK || res.Gap != 0 {
		t.Errorf("无漂移判定错误: ok=%v gap=%d", res.OK, res.Gap)
	}
}

// TestOpenSourceDriftSelftest 脚本内置判据向量必须全绿 (第二道锁)。
func TestOpenSourceDriftSelftest(t *testing.T) {
	out, rc := ossDriftExec(t, nil, "--selftest")
	if rc != 0 {
		t.Fatalf("--selftest rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "全部通过") || strings.Contains(out, "FAIL") {
		t.Fatalf("--selftest 输出异常: %s", out)
	}
}

// TestOpenSourceDriftRealWorkspace 真实工作区: 字段自洽。
// 开源仓不存在 (换机/CI) 时 rc=2 fail-closed 是合法结果 -> Skip, 不误红。
func TestOpenSourceDriftRealWorkspace(t *testing.T) {
	dir := t.TempDir()
	env := []string{
		"FORGE_ALERTS_PATH=" + filepath.Join(dir, "alerts.jsonl"),
		"FORGE_OSSDRIFT_STATE=" + filepath.Join(dir, "state.json"),
	}
	out, rc := ossDriftExec(t, env, "--json")
	if rc == 2 {
		t.Skipf("开源仓不存在 (fail-closed): %s", strings.TrimSpace(out))
	}
	var res ossDriftResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("--json 输出不可解析: %v\n%s", err, out)
	}
	sum := res.Same + len(res.Diff) + len(res.Missing) + len(res.Whitelist)
	if sum != res.Scope {
		t.Errorf("字段不自洽: same+diff+missing+whitelist=%d != scope=%d", sum, res.Scope)
	}
	if res.Scope == 0 {
		t.Errorf("scope 为 0 —— 扫描失效")
	}
	if res.OK != (res.Gap == 0) {
		t.Errorf("ok=%v 与 gap=%d 矛盾", res.OK, res.Gap)
	}
	if res.OK != (len(res.Diff) == 0 && len(res.Missing) == 0) {
		t.Errorf("ok=%v 与 diff/missing 矛盾: %v / %v", res.OK, res.Diff, res.Missing)
	}
	t.Logf("真实工作区: scope=%d same=%d diff=%d missing=%d whitelist=%d",
		res.Scope, res.Same, len(res.Diff), len(res.Missing), len(res.Whitelist))
}
