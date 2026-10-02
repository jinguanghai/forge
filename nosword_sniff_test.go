package main

// nosword_sniff_test.go — 算式候选嗅探 (nosword_sniff.go) 的结构约束。
// helper 见 nosword_lexer_test.go。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

var nswSniffDecls = []string{
	"nswBareConstRe", "nswDateRe", "nswYearMonRe", "nswRangeRe", "nswNumPairRe",
	"nswMultiSegRe", "nswUnaryBare", "nswHugeIntRe", "nswRangeDivRe", "nswLeadZeroRe",
	"nswParenRangeRe", "nswSlashPlusRe", "nswRatioWords", "nswPureFractionRe", "nswSlashListRe",
	"nswInCodeFence", "nswEvalMarkLine", "nswInInlineCode", "nswTableRow",
	"nswRatioWordBefore", "nswHanBefore", "nswNumFollows",
}

func TestNSWSniff_Ownership(t *testing.T) {
	nswTestOwnership(t, "nosword_sniff.go", nswSniffDecls, "nosword.go", "nosword_eval.go", "nosword_cand.go")
}

func TestNSWSniff_Deterministic(t *testing.T) { nswTestDeterminism(t, "nosword_sniff.go") }

// TestNSWSniff_NoRuntimeRegexCompile 正则必须编译期编译:
// 运行期 regexp.Compile 每次重新编译, 且把编译错误推迟到运行期。
func TestNSWSniff_NoRuntimeRegexCompile(t *testing.T) {
	n := 0
	for _, file := range nswTestAllFiles {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", file, err)
		}
		ast.Inspect(f, func(nd ast.Node) bool {
			ce, ok := nd.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := ce.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "regexp" {
				if sel.Sel.Name == "Compile" {
					t.Errorf("%s 出现 regexp.Compile —— 必须用 MustCompile (编译期失败优于运行期)", file)
				}
				if sel.Sel.Name == "MustCompile" {
					n++
				}
			}
			return true
		})
	}
	if n < 5 {
		t.Fatalf("仅扫到 %d 处 regexp.MustCompile, 疑似扫描失效", n)
	}
	t.Logf("扫描 %d 个无剑文件, %d 处 regexp.MustCompile 均为编译期编译", len(nswTestAllFiles), n)
}
