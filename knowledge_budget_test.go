package main

// knowledge_budget_test.go — T42 (20261010) 内外层预算不变式
//
// 背景 (T42): knowledge gate 的 auto 端点回退总预算曾 = 30s(wikidata) +
// 10s(dbpedia) = 40s, 而 forge 外层 knowledge ExecTimeout = 30s
// (forge_lang.go)。内层 > 外层 → wikidata 慢过约 20s 时外层先超时,
// dbpedia 回退逻辑永远执行不到 (死代码), 用户只看到 "context deadline
// exceeded" 而非「已切备用端点」。
//
// 本判据把「内层默认总预算 < 外层硬超时」钉成可执行不变式:
// 改 knowledge_gate.go 的任一预算常量, 或改 forge_lang.go 的 ExecTimeout,
// 只要不同步另一处 → 报红。判据读的是两侧源码文本 (两个文件各自独立编译,
// 无法互相 import), 与 deps_sentinel_test.go 同范式。

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const knowledgeGateSrcPath = ".forge/forge-tools/knowledge_gate.go"

// knowledgeBudgetViolation 返回违规描述, 空串 = 合规。纯函数, 可喂变异值。
//
// 两条不变式:
//  1. 默认 timeout 必须「严格小于」外层 ExecTimeout —— 非 auto 模式用完整
//     timeout 作单查询窗口, 相等则窗口+进程开销会撞外层边界。
//  2. auto 切分总和必须「严格小于」默认 timeout —— 否则切分形同虚设。
func knowledgeBudgetViolation(defTimeout, wikiPct, dbpPct, outerTimeout int) string {
	if defTimeout >= outerTimeout {
		return fmt.Sprintf("默认 timeout(%ds) 必须 < 外层 ExecTimeout(%ds) —— "+
			"否则非 auto 单查询窗口 + 进程开销会撞外层边界", defTimeout, outerTimeout)
	}
	inner := float64(defTimeout) * float64(wikiPct+dbpPct) / 100.0
	if inner >= float64(defTimeout) {
		return fmt.Sprintf("auto 切分总预算 %.1fs (= %ds * (%d%%+%d%%)) 必须 < 默认 timeout %ds",
			inner, defTimeout, wikiPct, dbpPct, defTimeout)
	}
	return ""
}

// mustParseInt 从文件中提取正则第一捕获组并转 int (fail-closed: 解析不到即 Fatal)。
func mustParseInt(t *testing.T, path, pattern, label string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	m := regexp.MustCompile(pattern).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("在 %s 中找不到 %s (模式 %s) —— 判据的前提失效, fail-closed", path, label, pattern)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("%s 的 %s 不是整数: %q", path, label, m[1])
	}
	return n
}

// TestKnowledgeAutoBudgetWithinOuterTimeout 钉住 T42 不变式。
func TestKnowledgeAutoBudgetWithinOuterTimeout(t *testing.T) {
	outer := mustParseInt(t, "forge_lang.go",
		`(?s)"knowledge":\s*\{.*?ExecTimeout:\s*(\d+)\s*\*\s*time\.Second`,
		"knowledge ExecTimeout")
	def := mustParseInt(t, knowledgeGateSrcPath,
		`knowledgeDefaultTimeout\s*=\s*(\d+)`, "knowledgeDefaultTimeout")
	wikiPct := mustParseInt(t, knowledgeGateSrcPath,
		`knowledgeWikiBudgetPct\s*=\s*(\d+)`, "knowledgeWikiBudgetPct")
	dbpPct := mustParseInt(t, knowledgeGateSrcPath,
		`knowledgeDbpediaBudgetPct\s*=\s*(\d+)`, "knowledgeDbpediaBudgetPct")

	if v := knowledgeBudgetViolation(def, wikiPct, dbpPct, outer); v != "" {
		t.Errorf("T42 预算不变式被破坏: %s\n"+
			"修法: 同步 knowledge_gate.go 的预算常量与 forge_lang.go 的 knowledge ExecTimeout", v)
	}
}

// TestKnowledgeBudgetDetectorSelfCheck 元判据: 判据自身要有区分度
// (合规放行, 三类变异各报红)。
func TestKnowledgeBudgetDetectorSelfCheck(t *testing.T) {
	cases := []struct {
		name             string
		def, w, d, outer int
		wantViol         bool
	}{
		{"现状配置应合规", 26, 40, 55, 30, false},
		{"旧版形态(默认==外层, auto 吃满)应报红", 30, 100, 33, 30, true},
		{"外层被调小应报红", 26, 60, 25, 20, true},
		{"切分占比 >= 100% 应报红", 26, 70, 40, 30, true},
	}
	for _, c := range cases {
		got := knowledgeBudgetViolation(c.def, c.w, c.d, c.outer) != ""
		if got != c.wantViol {
			t.Errorf("%s: 变异检测 = %v, 期望 %v (def=%d w=%d d=%d outer=%d)",
				c.name, got, c.wantViol, c.def, c.w, c.d, c.outer)
		}
	}
}

// ──────────────────────────────────────────────────────────────────
// T43 (20261010): 端点网络路径 + curl 超时误报守卫
//
// 背景: dbpedia 经环境代理实测 0/6 成功(全超时), 直连 13/15 成功 —— 两端点需要
// 相反的出口; 而旧版 _http_get 一律不带 --noproxy, 备用端点在生产中必然失败。
// 附带: curl 超时(rc=28)时 -w 仍输出 "000", 而 "000".isdigit() 为真 → 旧版提前
// return, 下游报 http_error, 把「连接超时」伪装成「HTTP 错误」。
//
// 本判据把上述修法钉成结构约束 (与 knowledge_budget_test.go 同范式: 读源码文本,
// 因为 knowledge_gate.go 是独立编译的单文件, 无法 import)。
// ──────────────────────────────────────────────────────────────────

// knowledgePathViolation 检查 knowledge_gate.go 源码是否满足 T43 约束。
// 返回违规描述列表, 空 = 合规。纯函数, 可喂变异文本。
func knowledgePathViolation(src string) []string {
	var v []string
	if n := strings.Count(src, `"path": "%s"`); n != 2 {
		v = append(v, fmt.Sprintf(`模板中 "path": "%%s" 占位符应为 2 处, 实为 %d 处 `+
			`(端点路径必须由 Go 常量注入, 不得在模板里另写一份字面量)`, n))
	}
	if !strings.Contains(src, "knowledgeWikidataPath, knowledgeDbpediaPath") {
		v = append(v, "buildKnowledgePy 的 Sprintf 参数未注入 knowledgeWikidataPath/knowledgeDbpediaPath")
	}
	if !strings.Contains(src, `code != "000"`) {
		v = append(v, `缺少 code != "000" 守卫: curl 超时时 -w 仍输出 000, 而 "000".isdigit() 为真`)
	}
	if !strings.Contains(src, "p.returncode == 0") {
		v = append(v, "缺少 returncode == 0 守卫: curl 传输失败仍可能被当成有效响应")
	}
	if !strings.Contains(src, "if not curl_missing:") {
		v = append(v, "curl 失败仍会回退 urllib: 双份尝试各吃一份 timeout, 可撞穿外层预算")
	}
	if !strings.Contains(src, `"--noproxy", "*"`) {
		v = append(v, `缺少 --noproxy "*": direct 路径无法绕过环境代理`)
	}
	if !strings.Contains(src, "ProxyHandler({})") {
		v = append(v, "urllib 兜底未实现 direct 路径 (缺空 ProxyHandler)")
	}
	return v
}

// mustParseStr 从文件中提取正则第一捕获组 (fail-closed)。
func mustParseStr(t *testing.T, path, pattern, label string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	m := regexp.MustCompile(pattern).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("在 %s 中找不到 %s (模式 %s) —— 判据的前提失效, fail-closed", path, label, pattern)
	}
	return m[1]
}

// TestKnowledgeEndpointPathsPinned 钉住 T43: 端点路径表值 + 模板注入形态。
func TestKnowledgeEndpointPathsPinned(t *testing.T) {
	wiki := mustParseStr(t, knowledgeGateSrcPath,
		`knowledgeWikidataPath\s*=\s*"(\w+)"`, "knowledgeWikidataPath")
	dbp := mustParseStr(t, knowledgeGateSrcPath,
		`knowledgeDbpediaPath\s*=\s*"(\w+)"`, "knowledgeDbpediaPath")

	if wiki != "proxy" {
		t.Errorf("wikidata 路径 = %q, 期望 proxy (经环境代理实测 6/6 成功 3.2-5.5s)", wiki)
	}
	if dbp != "direct" {
		t.Errorf("dbpedia 路径 = %q, 期望 direct (经代理 0/6 全超时, 直连 13/15 成功)", dbp)
	}

	b, err := os.ReadFile(knowledgeGateSrcPath)
	if err != nil {
		t.Fatalf("读 %s: %v", knowledgeGateSrcPath, err)
	}
	if v := knowledgePathViolation(string(b)); len(v) > 0 {
		t.Errorf("T43 结构约束被破坏:\n  - %s", strings.Join(v, "\n  - "))
	}
}

// TestKnowledgePathDetectorSelfCheck 元判据: 判据自身要有区分度
// (现状放行, 七类变异各报红)。
func TestKnowledgePathDetectorSelfCheck(t *testing.T) {
	b, err := os.ReadFile(knowledgeGateSrcPath)
	if err != nil {
		t.Fatalf("读 %s: %v", knowledgeGateSrcPath, err)
	}
	base := string(b)
	if v := knowledgePathViolation(base); len(v) != 0 {
		t.Fatalf("现状源码应合规, 却报红: %v", v)
	}
	muts := []struct{ name, drop string }{
		{"模板占位符改字面量", `"path": "%s"`},
		{"Sprintf 未注入常量", "knowledgeWikidataPath, knowledgeDbpediaPath"},
		{"000 守卫被删", `code != "000"`},
		{"returncode 守卫被删", "p.returncode == 0"},
		{"urllib 回退恢复", "if not curl_missing:"},
		{"--noproxy 被删", `"--noproxy", "*"`},
		{"ProxyHandler 被删", "ProxyHandler({})"},
	}
	for _, m := range muts {
		mutated := strings.ReplaceAll(base, m.drop, "")
		if mutated == base {
			t.Errorf("变异 %q 未生效 (片段 %q 在源码中不存在) —— 判据锚点已漂移",
				m.name, m.drop)
			continue
		}
		if len(knowledgePathViolation(mutated)) == 0 {
			t.Errorf("变异 %q 未报红: 判据对该约束无区分度", m.name)
		}
	}
}
