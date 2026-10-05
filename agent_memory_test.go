// agent_memory_test.go — 记忆锚点装配的确定性与剔除契约。
//
// 本文件的断言对象是"前缀缓存的地基": reorderMemoryJSON 的确定性序、
// buildMemoryTailText 的动态字段剔除、memoryTailDiff 的字段级差异。
// 这三者任一失效 → DeepSeek 前缀缓存命中率断崖, 且症状是"钱变多"而非"报错"。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func writeTestMemory(t *testing.T, dir, content string) {
	t.Helper()
	p := memoryFilePath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
}

func TestReorderMemoryJSON_StableOrderThenExtraSorted(t *testing.T) {
	in := []byte(`{"zeta":"z","axioms":"a","alpha":"x","identity":"i"}`)
	out := string(reorderMemoryJSON(in))
	if !json.Valid([]byte(out)) {
		t.Fatalf("输出不是合法 JSON: %s", out)
	}
	iID := strings.Index(out, `"identity"`)
	iAx := strings.Index(out, `"axioms"`)
	iAl := strings.Index(out, `"alpha"`)
	iZe := strings.Index(out, `"zeta"`)
	for _, c := range []struct {
		n string
		i int
	}{{"identity", iID}, {"axioms", iAx}, {"alpha", iAl}, {"zeta", iZe}} {
		if c.i < 0 {
			t.Fatalf("字段 %s 丢失: %s", c.n, out)
		}
	}
	if iID > iAx {
		t.Errorf("稳定字段未按 memStableOrder 排序 (identity 应在 axioms 前): %s", out)
	}
	if !(iAx < iAl && iAl < iZe) {
		t.Errorf("未知字段未按字典序追加尾部 (axioms<alpha<zeta): %s", out)
	}
}

func TestReorderMemoryJSON_Deterministic(t *testing.T) {
	in := []byte(`{"b":1,"a":2,"identity":"i","c":3}`)
	first := string(reorderMemoryJSON(in))
	for i := 0; i < 50; i++ {
		if got := string(reorderMemoryJSON(in)); got != first {
			t.Fatalf("第 %d 次输出不同 —— 非确定性序会打断前缀缓存:\n %s\n %s", i, got, first)
		}
	}
}

func TestReorderMemoryJSON_InvalidPassthrough(t *testing.T) {
	in := []byte("这不是 JSON")
	if got := reorderMemoryJSON(in); string(got) != string(in) {
		t.Errorf("非法输入应原样返回 (不破坏注入), got %q", got)
	}
}

func TestCurrentActiveTask_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	if got := currentActiveTask(dir); got != "" {
		t.Errorf("无 memory.json 时应返回空串, got %q", got)
	}
}

func TestCurrentActiveTask_ReadsField(t *testing.T) {
	dir := t.TempDir()
	writeTestMemory(t, dir, `{"identity":"x","active_task":"B3 批5"}`)
	if got := currentActiveTask(dir); got != "B3 批5" {
		t.Errorf("active_task = %q, want %q", got, "B3 批5")
	}
}

func TestBuildMemoryTailText_StripsDynamicFields(t *testing.T) {
	dir := t.TempDir()
	writeTestMemory(t, dir, `{"identity":"i","key_findings":[{"a":1}],"folded_memory":"x"}`)
	out := buildMemoryTailText(dir)
	for _, f := range dynamicMemoryFields {
		if strings.Contains(out, `"`+f+`"`) {
			t.Errorf("动态字段 %s 未剔除, 会打断前缀缓存: %s", f, out)
		}
	}
	if !strings.Contains(out, `"identity"`) {
		t.Errorf("稳定字段丢失: %s", out)
	}
}

func TestMemoryTailDiff_AddedChangedRemoved(t *testing.T) {
	old := `{"identity":"i","role":"r","gates":"g"}`
	nw := `{"identity":"i","role":"R","axioms":"a"}`
	diff := memoryTailDiff(old, nw)
	if !strings.Contains(diff, "role: ") {
		t.Errorf("变化字段未报告: %q", diff)
	}
	if !strings.Contains(diff, "axioms: ") {
		t.Errorf("新增字段未报告: %q", diff)
	}
	if !strings.Contains(diff, "gates: <已删除>") {
		t.Errorf("删除字段未报告: %q", diff)
	}
	if strings.Contains(diff, "identity") {
		t.Errorf("未变化字段不应出现 (省 token): %q", diff)
	}
}

// promptMapBudgetRunes 的定义已移到 agent_memory.go (20261004) —— 预算描述的是
// 生产常量 systemPrompt, 定义留在测试里会让 axiom_carriers.json 的描述数字无处可校。

// promptMapIssues 检查认知地图的完整性, 返回问题清单 (空 = 合格)。
// 抽成纯函数是为了让判据本身可被变异用例直接喂入 —— 判据自身也要有判据。
func promptMapIssues(sp string, gates []string) []string {
	var out []string
	if n := utf8.RuneCountInString(sp); n > promptMapBudgetRunes {
		out = append(out, fmt.Sprintf("长度 %d 字符 超预算 %d —— 先删冗余再谈新增", n, promptMapBudgetRunes))
	}
	iMap := strings.Index(sp, "<sword_furnace>")
	iDisc := strings.Index(sp, "<discipline>")
	iRules := strings.Index(sp, "<critical_rules>")
	if iMap < 0 || iDisc < 0 || iRules < 0 {
		out = append(out, fmt.Sprintf("认知地图段缺失: sword=%d discipline=%d rules=%d", iMap, iDisc, iRules))
	} else if !(iMap < iDisc && iDisc < iRules) {
		out = append(out, fmt.Sprintf("地图必须排在 <critical_rules> 之前: sword=%d disc=%d rules=%d", iMap, iDisc, iRules))
	}
	for _, tag := range []string{"</sword_furnace>", "</discipline>"} {
		if !strings.Contains(sp, tag) {
			out = append(out, "地图段未闭合: 缺 "+tag)
		}
	}
	for _, g := range gates {
		if !strings.Contains(sp, g) {
			out = append(out, fmt.Sprintf("gate %q 未出现在地图中 —— 模型只能靠猜", g))
		}
	}
	if strings.Contains(sp, "中医药") {
		out = append(out, "含 '中医药' 会误伤 tcm 禁用用例 (见 gate_registry_test.go)")
	}
	if !strings.HasPrefix(sp, "你是 铸剑炉") {
		out = append(out, "未以身份段开头 (前缀缓存地基)")
	}
	if strings.Count(sp, "`") != 0 {
		out = append(out, "常量内不得含反引号 (raw string 会编译失败)")
	}
	return out
}

// TestSystemPromptMap_BudgetAndCoverage 认知地图哨兵 (20261003):
//
//	动机: systemPrompt 是 LLM 每窗口开工时的认知地图。但"地图越长越好"是错觉 ——
//	超预算即注意力稀释, 关键纪律(生成与执行分离/证据先于声称)被淹没, 症状是
//	"钱变多 + 模型开始飘"而非报错。故钉住: 地图段存在且排在纪律段之前、
//	长度有预算、注册表里每个 gate 都被投影到地图中 (漏投影 = 模型靠猜)。
func TestSystemPromptMap_BudgetAndCoverage(t *testing.T) {
	for _, msg := range promptMapIssues(systemPrompt, gateNames()) {
		t.Error(msg)
	}
}

// TestPromptMapIssues_MutationSelfCheck 判据自身的判据: 变异体必须全部被捕获。
// 防的是"判据恒真/恒假"这类静默失效 —— 判据错了比没有判据更危险。
func TestPromptMapIssues_MutationSelfCheck(t *testing.T) {
	base := systemPrompt
	variants := []struct {
		name string
		sp   string
	}{
		{"gate 漏投影", strings.Replace(base, "media(图/视频)", "MMX(图/视频)", 1)},
		{"地图段挪到纪律段之后", strings.Replace(base, "<sword_furnace>", "<zzz>", 1) + "<sword_furnace>"},
		{"超预算", base + strings.Repeat("冗", promptMapBudgetRunes)},
		{"段未闭合", strings.Replace(base, "</discipline>", "", 1)},
		{"未以身份段开头", "序言\n" + base},
	}
	for _, v := range variants {
		if len(promptMapIssues(v.sp, gateNames())) == 0 {
			t.Errorf("变异 %q 未被判据捕获 —— 判据失效", v.name)
		}
	}
	// 反向断言: 真实 systemPrompt 必须零问题, 否则判据恒真 (报什么都报红 = 没信息)
	if issues := promptMapIssues(systemPrompt, gateNames()); len(issues) != 0 {
		t.Errorf("真实 systemPrompt 被判有问题: %v", issues)
	}
	// gate 集合为空时不得因"漏投影"报红 (判据不依赖调用方传什么)
	if issues := promptMapIssues(base, nil); len(issues) != 0 {
		t.Errorf("空 gate 集合下应零问题: %v", issues)
	}
}
