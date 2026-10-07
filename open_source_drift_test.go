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
	"runtime"
	"strconv"
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
	// 写入端 (T17): 走 CLI 的路径也会写状态文件 —— 顺带钉住「save_state 真被调用」。
	// 文件存在性 = 接线判据 (emit_alerts 里的调用被删即报红); 行尾检查只在 Windows
	// 有鉴别力 (Python text 模式做 os.linesep 转换), 非 Windows 上不会误红。
	if rc != 2 { // rc=2 = 判据不可用, 未走到写入路径
		if sb, err := os.ReadFile(filepath.Join(dir, "state.json")); err != nil {
			t.Errorf("判据跑完未产出状态文件 (save_state 未被调用?): %v", err)
		} else if bytes.ContainsRune(sb, '\r') {
			t.Errorf("状态文件含 CR (写入端缺 newline=\"\\n\"): %q", sb)
		}
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

// TestOpenSourceDriftStateFileIsLF 写入端判据 (T17, 20261007): 实调 save_state() 断言产物无 CR。
//
// 病根: 分形守卫报红 .forge/ossdrift_state.json CRLF x2 —— 根因是 save_state() 写状态文件
// 时缺 newline="\n" (同一文件 242 行的写入点带了该参数 -> 属漏改, 非设计)。当时的判据判的是
// 「工作区现状」: 文件被删/被忽略 -> 静默变绿, 而写法照旧 -> hourly 跑一次又生成 CRLF。
// 本用例把「文件当前干净」升级为「写入函数被钉住」—— 直调函数本身, 不经 CLI 链路。
//
// 平台前提: Python text 模式的 os.linesep 转换只在 Windows 发生 —— 非 Windows 上缺
// newline 也写不出 CR, 判据失去鉴别力, 故显式 Skip (不静默变绿)。
func TestOpenSourceDriftStateFileIsLF(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skipf("写入端行尾判据只在 Windows 有鉴别力 (Python text 模式做 os.linesep 转换)")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	pyDir, err := filepath.Abs("defense_system")
	if err != nil {
		t.Fatalf("解析脚本目录失败: %v", err)
	}
	// 直调写入函数 (不经 CLI): 钉住函数本身, 不依赖 CLI 链路是否还在调它。
	code := "import sys; sys.path.insert(0, " + strconv.Quote(pyDir) + "); " +
		"import open_source_drift as m; m.save_state({'probe': '写端', 'n': 1}); " +
		"print(m.state_path())"
	cmd := exec.Command(guardGatePython(), "-c", code)
	cmd.Env = append(pythonUTF8Env(),
		"FORGE_OSSDRIFT_STATE="+state,
		"FORGE_ALERTS_PATH="+filepath.Join(dir, "alerts.jsonl"),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("实调 save_state 失败: %v\n%s", err, stderr.String())
	}
	// 环境变量没生效 = 会写到生产 .forge/ossdrift_state.json (测试污染生产)。
	if got := strings.TrimSpace(stdout.String()); got != state {
		t.Fatalf("state_path() = %q, want %q (隔离失效, 会写生产文件)", got, state)
	}
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatalf("save_state 未产出文件: %v", err)
	}
	if bytes.ContainsRune(b, '\r') {
		t.Errorf("状态文件含 CR —— 写入端缺 newline=\"\\n\" (污染源仍在): %q", b)
	}
	if !bytes.ContainsRune(b, '\n') || len(b) < 3 {
		t.Errorf("状态文件内容异常 (空/无换行): %q", b)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Errorf("状态文件不是合法 JSON: %v (%q)", err, b)
	} else if back["probe"] != "写端" {
		t.Errorf("回读不一致 (编码/ensure_ascii?): %v", back)
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
