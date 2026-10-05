package main

// hygiene_owner_refs_test.go — manifest owner 字段的"引用有效性"判据 (20261003)
//
// 缺口: hygiene_forge_test.go 只校验 owner 非空("必须写明产生点, 否则失控时找谁"),
// 却不校验 owner 指向的文件/函数是否真实存在 —— 「有值」 != 「值有效」。
// 实测 20261003: deploy_pending.json 的 owner 写作 "self_gate.go deployPendingPath()",
// 而 self_gate.go 早在拆分中消失、deployPendingPath() 全仓零定义, 哨兵却常年全绿
// (该条目与那个孤儿文件已同批删除)。
//
// owner 的用途是"失控时找谁": 指向不存在的符号 = 找不到人 = 判据形同虚设。
// 判据(逻辑矛盾型, 不依赖概率): owner 里出现的 *.go/*.py/*.ps1 文件名必须存在于仓库;
// owner 里出现的 foo() 必须能解析到 func foo / def foo 定义 —— 声称的产生点不存在即矛盾。
//
// 判据自身也有判据: 校验抽成纯函数 hygieneOwnerRefProblems, 由 TestHygieneOwnerRefsSharp
// 注入变异/健康输入双向钉住。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	ownerFileRefRe = regexp.MustCompile(`([A-Za-z0-9_\-]+\.(?:go|py|ps1))`)
	ownerFuncRefRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\(\)`)
	goFuncDefRe    = regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	pyFuncDefRe    = regexp.MustCompile(`(?m)^\s*def ([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

// ownerScanSkipDirs 扫描时跳过的目录 (版本库/依赖/归档/缓存, 与其余 hygiene 判据同口径)。
var ownerScanSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "_archive": true,
	"__pycache__": true, ".forge-temp": true, "toolcache": true,
}

// hygieneOwnerRefProblems 校验 owner 引用有效性 (纯函数, 便于注入变异输入)。
// files = 仓库内全部文件名(basename), defs = 仓库内全部已定义函数名。空返回 = 全部有效。
func hygieneOwnerRefProblems(owners map[string]string, files, defs map[string]bool) []string {
	var bad []string
	for name, owner := range owners {
		for _, fn := range ownerFileRefRe.FindAllString(owner, -1) {
			if !files[fn] {
				bad = append(bad, fmt.Sprintf("%s: owner 引用 %q, 全仓无此文件 (产生点不存在)", name, fn))
			}
		}
		for _, m := range ownerFuncRefRe.FindAllStringSubmatch(owner, -1) {
			if !defs[m[1]] {
				bad = append(bad, fmt.Sprintf("%s: owner 提及 %s(), 全仓无定义 (产生点不存在)", name, m[1]))
			}
		}
	}
	sort.Strings(bad)
	return bad
}

// scanRepoFilesAndDefs 一次遍历收集全仓文件名 + .go/.py 里的函数定义名。
// 遍历失败(无权限/软链)只跳过该处, 不瘫痪整条自检链。
func scanRepoFilesAndDefs(root string) (map[string]bool, map[string]bool, error) {
	files := map[string]bool{}
	defs := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if ownerScanSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		files[d.Name()] = true
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".py") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		s := string(b)
		for _, m := range goFuncDefRe.FindAllStringSubmatch(s, -1) {
			defs[m[1]] = true
		}
		for _, m := range pyFuncDefRe.FindAllStringSubmatch(s, -1) {
			defs[m[1]] = true
		}
		return nil
	})
	return files, defs, err
}

// manifestOwners 取清单里全部 (name -> owner) 对, 含 subdirs 内层条目。
func manifestOwners(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(forgeManifestFile)
	if err != nil {
		t.Skipf("清单不在位 (%v) —— 开源仓库无此文件", err)
	}
	var root map[string]interface{}
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("清单不可解析: %v", err)
	}
	owners := map[string]string{}
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			name, _ := x["name"].(string)
			owner, _ := x["owner"].(string)
			if name != "" && owner != "" {
				owners[name] = owner
			}
			for _, vv := range x {
				walk(vv)
			}
		case []interface{}:
			for _, vv := range x {
				walk(vv)
			}
		}
	}
	walk(root)
	return owners
}

// TestHygieneOwnerRefsExist 清单里每条 owner 的产生点必须真实存在。
func TestHygieneOwnerRefsExist(t *testing.T) {
	owners := manifestOwners(t)
	if len(owners) < 20 {
		t.Fatalf("带 owner 的条目仅 %d 条, 清单疑似被削 —— 判据覆盖面不足", len(owners))
	}
	files, defs, err := scanRepoFilesAndDefs(".")
	if err != nil {
		t.Fatalf("扫描仓库失败: %v", err)
	}
	// 探针自检: 扫描/解析若退化, 判据会恒绿 —— 先钉住探针自身有效
	if len(files) < 100 {
		t.Fatalf("仅扫到 %d 个文件, 扫描范围疑似退化 (判据会恒绿)", len(files))
	}
	if len(defs) < 50 {
		t.Fatalf("仅解析到 %d 个函数定义, 定义正则疑似失效 (判据会恒绿)", len(defs))
	}
	for _, p := range hygieneOwnerRefProblems(owners, files, defs) {
		t.Error(p)
	}
}

// TestHygieneOwnerRefsSharp 判据的判据: 变异输入必须报红, 健康输入必须放行。
func TestHygieneOwnerRefsSharp(t *testing.T) {
	files := map[string]bool{"real.py": true}
	defs := map[string]bool{"realFunc": true}
	if got := hygieneOwnerRefProblems(map[string]string{"a.json": "real.py realFunc()"}, files, defs); len(got) != 0 {
		t.Fatalf("健康输入不该报红: %v", got)
	}
	if got := hygieneOwnerRefProblems(map[string]string{"a.json": "ghost.go"}, files, defs); len(got) != 1 {
		t.Fatalf("引用不存在的文件必须报红, got %v", got)
	}
	if got := hygieneOwnerRefProblems(map[string]string{"a.json": "real.py ghostFunc()"}, files, defs); len(got) != 1 {
		t.Fatalf("引用不存在的函数必须报红, got %v", got)
	}
	if got := hygieneOwnerRefProblems(map[string]string{"a.json": "ghost.go ghostFunc()"}, files, defs); len(got) != 2 {
		t.Fatalf("双假引用应报两条, got %v", got)
	}
	// 剥离元语境: owner 里没有 *.go/*.py 也没有 foo() 时不该乱报
	if got := hygieneOwnerRefProblems(map[string]string{"a.json": "人工维护"}, files, defs); len(got) != 0 {
		t.Fatalf("无引用形态的 owner 不该报红: %v", got)
	}
}
