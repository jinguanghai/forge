package main

// docref_sentinel_test.go — 注释里的文件引用必须指向真实存在的文件 (20260930)。
//
// 病根: 实现改了、测试改名了, 注释里的文件名没跟着改 —— 留下"指向空气的判据"。
// 实测 (20260930): 主包内 4 处注释引用了不存在的测试文件 ——
//   audit_schema.go 引两个从未存在的哨兵名 (实现实际落在 audit_schema_test.go),
//   audit_schema_test.go 与 git_snapshot_test.go 的文件头则自指了旧名。
// 编译器不管注释, 接线哨兵只管代码符号, 人眼会漏 —— 这类腐化此前没有任何判据看得见。
//
// 注意: 本文件注释里不得写出不存在的测试文件名 —— 哨兵会把自己抓出来
// (首版注释列举了病史文件名, 直接被本哨兵判红)。要记录病史就写描述性文字。
//
// 口径:
//   1. 只查注释 (AST Comments), 不查字符串字面量 —— 代码里描述模式的字符串不是引用。
//   2. 以 "_" 开头的名字 (如 _windows_test.go) 视为模式描述, 跳过。
//   3. 存在性用全仓 basename 集合 (跳过历史副本/快照目录), 避免跨包引用误报。

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docTestRefRe 匹配注释里的测试文件引用。
var docTestRefRe = regexp.MustCompile(`[A-Za-z0-9_]+_test\.go`)

// docSkipDirs 是收集存在性时跳过的目录 (第三方 / 历史副本 / 快照)。
var docSkipDirs = map[string]bool{
	".git": true, ".forge": true, "_archive": true,
	"node_modules": true, ".forge-temp": true, "snapshots": true,
}

// collectTestBasenames 返回 repo 下所有 *_test.go 的文件名集合。
func collectTestBasenames(repo string) (map[string]bool, error) {
	got := map[string]bool{}
	err := filepath.WalkDir(repo, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限目录不阻断 (扫描范围教训: 一处报错瘫整条链)
		}
		if d.IsDir() {
			if p != repo && docSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			got[d.Name()] = true
		}
		return nil
	})
	return got, err
}

// airTestRefs 扫描 dir 下所有 .go 的注释, 返回引用了不存在测试文件的证据串。
func airTestRefs(dir string, exists map[string]bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
		if perr != nil {
			continue // 语法错误不属本哨兵职责
		}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				for _, m := range docTestRefRe.FindAllString(c.Text, -1) {
					if strings.HasPrefix(m, "_") || exists[m] {
						continue
					}
					out = append(out, fmt.Sprintf("%s:%d -> %s", e.Name(), fset.Position(c.Pos()).Line, m))
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// TestDocRefNoAirTestFileRefs 主包全部 .go 的注释不得引用不存在的测试文件。
func TestDocRefNoAirTestFileRefs(t *testing.T) {
	exists, err := collectTestBasenames(".")
	if err != nil {
		t.Fatalf("收集测试文件失败: %v", err)
	}
	if len(exists) < 50 {
		t.Fatalf("存在性集合只有 %d 个测试文件 —— 判据自身失效, 拒绝给出绿色", len(exists))
	}
	air, err := airTestRefs(".", exists)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if len(air) > 0 {
		t.Errorf("注释引用了不存在的测试文件 %d 处 (改名/删除后必须同步注释):\n  %s",
			len(air), strings.Join(air, "\n  "))
	}
}

// TestDocRefDetectorSelfCheck 检出器自检: 必须能报出空气引用, 且不误报模式描述。
// 没有这条, "测出正常" 零证据力 (检出器可能恒返回空)。
func TestDocRefDetectorSelfCheck(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("real_test.go", "package main\n")
	write("src.go", "package main\n\n// 见 real_test.go 与 ghost_test.go\n"+
		"// 模式: 各配同名 _windows_test.go\nfunc f() {}\n")

	air, err := airTestRefs(dir, map[string]bool{"real_test.go": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(air) != 1 || !strings.Contains(air[0], "ghost_test.go") {
		t.Errorf("应恰好报出 ghost_test.go 一处, 实得 %d 处: %v", len(air), air)
	}
	if strings.Contains(strings.Join(air, " "), "_windows_test.go") {
		t.Error("以 _ 开头的模式描述不应被判为空气引用")
	}
	air2, _ := airTestRefs(dir, map[string]bool{"real_test.go": true, "ghost_test.go": true})
	if len(air2) != 0 {
		t.Errorf("引用全部存在时不应报红 (防恒报红), 实得: %v", air2)
	}
}
