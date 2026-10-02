package main

// ============================================================================
// version_test.go —— 版本号判据的接线哨兵 (20261002)
//
// 缺陷: AppVersion 自 v3.0.0 起冻结 —— 325 提交 / 0 版本 tag / 0 条判据。
// 根因不是"忘改", 是【没有死程序钉住】: 无判据的约定必然腐化 (同 .forge/forge-tools
// 内 11MB 死代码躺数月而四套守卫全绿)。实际代价: exe mtime 13:34:10 早于最新提交
// 13:38:14 —— "跑的是哪一版"当场不可判。
//
// 本文件把五件事钉死 (接线只能由死程序判定):
//   1. 判据向量全通过 (version_check.py --selftest, 判据本身可测)
//   2. 真实一致性: AppVersion == 最近 v* tag (硬判据, 违规即红)
//   3. 判据来自单一数据源 watchlist.VERSION_SPEC (不得有第二份清单)
//   4. 消费者必须走唯一渲染入口 versionString() (漂移的第二个来源)
//   5. 构建事实注入两个入口 (self gate + upgrade) 都在 —— 防"改一处漏一处"
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const versionCheckRel = "defense_system/version_check.py"

// versionFacts 是 version_check.py --json 输出的事实子集。
type versionFacts struct {
	AppVersion string   `json:"app_version"`
	NewestTag  string   `json:"newest_tag"`
	ExeVersion string   `json:"exe_version"`
	Violations []string `json:"violations"`
	Infos      []string `json:"infos"`
}

func versionCheckRun(t *testing.T, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(guardGatePython(), append([]string{versionCheckRel}, args...)...)
	// PYTHONDONTWRITEBYTECODE: 避免探针在 defense_system/ 留下 __pycache__ 盲区垃圾。
	cmd.Env = pythonUTF8Env()
	out, err := cmd.Output() // 只取 stdout: 告警提示走 stderr, 混进来 JSON 就不可解析
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("version_check.py 执行失败 (fail-closed, 不放行): %v", err)
		}
	}
	return out
}

// TestVersionCheckSelftest 判据向量必须全通过 —— 判据本身可测, 不靠"改坏真文件"。
func TestVersionCheckSelftest(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(guardGatePython(), versionCheckRel, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--selftest 失败 (判据向量不符): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0 失败") {
		t.Errorf("selftest 未报「0 失败」: %s", out)
	}
}

// TestVersionMatchesTag 钉住「源码版本号 == 最近 v* tag」——
// 改了版本号不打 tag / 打了 tag 不改版本号, 两种漂移都在这里报红。
func TestVersionMatchesTag(t *testing.T) {
	t.Parallel()
	out := versionCheckRun(t, "--json")
	body := out
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		body = out[i:]
	}
	var f versionFacts
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("探针输出不是合法 JSON (fail-closed): %v\n%s", err, out)
	}
	if f.AppVersion == "" {
		t.Fatalf("app_version 为空 —— 判据失效 (fail-closed)")
	}
	if len(f.Violations) != 0 {
		t.Errorf("版本号判据违规 %d 项: %v", len(f.Violations), f.Violations)
	}
	t.Logf("版本 v%s / 最近 tag %s / exe %s", f.AppVersion, f.NewestTag, f.ExeVersion)
}

// TestVersionSingleSource 判据必须来自 watchlist.VERSION_SPEC (两处各写一份必然漂移)。
func TestVersionSingleSource(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("defense_system/watchlist.py")
	if err != nil {
		t.Fatalf("读取 watchlist.py 失败 (fail-closed): %v", err)
	}
	if !strings.Contains(string(raw), "VERSION_SPEC") {
		t.Errorf("watchlist.py 缺 VERSION_SPEC —— 判据单一数据源失效")
	}
	src, err := os.ReadFile(versionCheckRel)
	if err != nil {
		t.Fatalf("读取 %s 失败 (fail-closed): %v", versionCheckRel, err)
	}
	if !strings.Contains(string(src), "from watchlist import VERSION_SPEC") {
		t.Errorf("%s 未从 watchlist import 判据 —— 存在第二份清单", versionCheckRel)
	}
}

// versionConsumersRe 从 watchlist.VERSION_SPEC 解析消费者清单 ——
// 判据写死文件名 = 新增输出点时哨兵静默通过 (lesson: 判据必须扫全包)。
var versionConsumersRe = regexp.MustCompile(`"consumers":\s*\[([^\]]*)\]`)

func TestVersionConsumersUseVersionString(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("defense_system/watchlist.py")
	if err != nil {
		t.Fatalf("读取 watchlist.py 失败 (fail-closed): %v", err)
	}
	m := versionConsumersRe.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("未能从 VERSION_SPEC 解析 consumers (fail-closed, 判据结构变了?)")
	}
	var files []string
	for _, part := range strings.Split(m[1], ",") {
		p := strings.Trim(strings.TrimSpace(part), `"'`)
		if p != "" {
			files = append(files, p)
		}
	}
	if len(files) == 0 {
		t.Fatalf("consumers 为空 —— 漂移检测失效")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("消费者 %s 读取失败: %v", f, err)
			continue
		}
		if !strings.Contains(string(b), "versionString()") {
			t.Errorf("%s 未使用唯一渲染入口 versionString() —— 版本号可能被复制成第二份来源", f)
		}
	}
	t.Logf("版本号消费者 %d 个均走 versionString()", len(files))
}

// TestVersionStringFormat 版本串形态: 必须以 "v"+AppVersion 开头 (纯函数, 直接判定)。
func TestVersionStringFormat(t *testing.T) {
	t.Parallel()
	if AppVersion == "" {
		t.Fatal("AppVersion 为空")
	}
	s := versionString()
	if !strings.HasPrefix(s, "v"+AppVersion) {
		t.Errorf("versionString() = %q, 期望以 %q 开头", s, "v"+AppVersion)
	}
	if strings.Contains(s, "vv") {
		t.Errorf("versionString() 出现双 v: %q", s)
	}
}

// TestBuildLdflagsWired 构建事实注入必须两个入口都在 (self gate + upgrade):
// 只改一处 = 另一条构建路径出来的 exe 报不出「跑的是哪一版」。
func TestBuildLdflagsWired(t *testing.T) {
	t.Parallel()
	gs, err := os.ReadFile("git_snapshot.go")
	if err != nil {
		t.Fatalf("读取 git_snapshot.go 失败 (fail-closed): %v", err)
	}
	if !strings.Contains(string(gs), "func buildLdflags(") {
		t.Errorf("git_snapshot.go 缺 buildLdflags —— 注入串无单一数据源")
	}
	for _, f := range []string{"forge_self.go", "upgrade.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s 失败 (fail-closed): %v", f, err)
		}
		if !strings.Contains(string(b), "buildLdflags(") {
			t.Errorf("%s 未调用 buildLdflags —— 该构建路径的 exe 报不出提交哈希", f)
		}
	}
}
