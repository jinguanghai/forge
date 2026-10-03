package main

// axiom_ref_sentinel_test.go — 公理编号引用哨兵 (20261003)
//
// 背景: memory.json 的 axioms 于 20261003 由 7 条合并为 4 条, 全仓库源码注释
// 里的"公理X"引用同步重编号。此前编号引用【无任何判据】—— 编译器不管注释,
// 合并/重排公理时注释会静默指向不存在的编号 (与"注释里的文件引用会腐化"同源)。
//
// 本哨兵把"源码引用的公理编号必须真实存在"固化为死程序判定 (公理三)。
// 单一数据源: 合法编号集合从 memory.json 的 axioms 字段现场解析, 不硬编码
// (硬编码会在下次改公理时腐化 —— 正是本哨兵要防的病)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 匹配"公理<中文数字>"。定义行形如 "公理三: ...", 引用形如 "(公理三)"。
var axiomRefRe = regexp.MustCompile(`公理([一二三四五六七八九十])`)

// 定义行: "公理X" 紧跟冒号。只认这种形态, 避免正文里的顺带提及被误当定义。
var axiomDefRe = regexp.MustCompile(`公理([一二三四五六七八九十])\s*[:：]`)

func axiomLegalSet(t *testing.T, wd string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(wd, "memory.json"))
	if err != nil {
		t.Skip("无 memory.json (非主仓库环境), 跳过公理编号哨兵")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("memory.json 不可解析: %v", err)
	}
	raw, _ := m["axioms"].(string)
	legal := map[string]bool{}
	for _, mm := range axiomDefRe.FindAllStringSubmatch(raw, -1) {
		legal[mm[1]] = true
	}
	if len(legal) == 0 {
		t.Fatalf("memory.json.axioms 未解析出任何公理定义 (格式变化?), fail-closed 中止")
	}
	return legal
}

func TestAxiomRefSentinel_NoGhostNumbers(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	legal := axiomLegalSet(t, wd)

	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatal(err)
	}
	scanned, refs := 0, 0
	var bad []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		scanned++
		b, err := os.ReadFile(filepath.Join(wd, e.Name()))
		if err != nil {
			continue
		}
		for _, mm := range axiomRefRe.FindAllStringSubmatch(string(b), -1) {
			refs++
			if !legal[mm[1]] {
				bad = append(bad, e.Name()+" -> 公理"+mm[1])
			}
		}
	}
	// 判据自身的前置条件 (公理三推论: 判据也要有判据) —— 扫描逻辑失效必须报红,
	// 否则"零命中"会被误读成"全部合规"。
	if scanned < 50 {
		t.Fatalf("只扫到 %d 个 .go 文件, 扫描逻辑疑似失效 (主包应远超此数)", scanned)
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("源码引用了不存在的公理编号 (合法集合: %v, 共 %d 处违规):\n%s",
			axiomKeys(legal), len(bad), strings.Join(bad, "\n"))
	}
	t.Logf("扫描 %d 个 .go 文件, 命中 %d 处公理引用, 全部落在合法集合 %v 内",
		scanned, refs, axiomKeys(legal))
}

// 判据的判据: 正则在已知输入上的行为必须符合预期, 否则整个哨兵恒真。
func TestAxiomRefSentinel_RegexSelfCheck(t *testing.T) {
	for _, s := range []string{"公理一", "公理三", "(公理四)", "公理二: xxx"} {
		if !axiomRefRe.MatchString(s) {
			t.Fatalf("引用正则漏匹配 %q", s)
		}
	}
	for _, s := range []string{"公理", "公理X", "原理一", "公理 x"} {
		if axiomRefRe.MatchString(s) {
			t.Fatalf("引用正则误匹配 %q", s)
		}
	}
	for s, want := range map[string]string{"公理一: x": "一", "公理三：y": "三"} {
		mm := axiomDefRe.FindStringSubmatch(s)
		if mm == nil || mm[1] != want {
			t.Fatalf("定义正则未从 %q 解析出 %q (得 %v)", s, want, mm)
		}
	}
	if axiomDefRe.MatchString("公理三 是架构即测试") {
		t.Fatal("定义正则把无冒号的顺带提及误判为定义")
	}
}

func axiomKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, "公理"+k)
	}
	sort.Strings(out)
	return out
}
