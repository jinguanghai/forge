package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHasPackageClause(t *testing.T) {
	pos := []string{
		"package main",
		"package main\nimport \"fmt\"",
		"  \n\tpackage main",
		"// 注释\npackage main",
		"/* 块注释 */ package main",
	}
	for _, c := range pos {
		if !hasPackageClause(c) {
			t.Errorf("pos 误判: %q", c)
		}
	}
	// 关键反例: 字符串字面量里含 "package main" —— 旧判据 strings.Contains 在此误判
	neg := []string{
		"",
		"import \"fmt\"",
		"import \"fmt\"\nfunc main(){fmt.Println(\"package main\")}",
		"x := 1",
	}
	for _, c := range neg {
		if hasPackageClause(c) {
			t.Errorf("neg 误判: %q", c)
		}
	}
}

func TestLooksLikeFullGoProgram(t *testing.T) {
	if !looksLikeFullGoProgram("import \"fmt\"\nfunc main(){}") {
		t.Error("完整程序应 true")
	}
	if looksLikeFullGoProgram("fmt.Println(1)") {
		t.Error("片段应 false")
	}
	if looksLikeFullGoProgram("\tfmt.Println(1)") {
		t.Error("缩进片段应 false")
	}
}

// 回归: 字符串字面量含 "package main" 的完整程序。
// 旧判据 strings.Contains → 跳过包装 → 裸写 main.go → 编译期
// "main.go:1:1: expected 'package', found 'import'"。
func TestSelfHostedGoWrapRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实 go 编译 (3 次 go build + run)")
	}
	f := &Forge{ctx: context.Background(), workDir: t.TempDir()}
	code := "import \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"package main\")\n}"
	res := f.selfHostedGo(code, 铸剑炉_COMPILERS["go"], time.Now())
	if !res.OK {
		t.Fatalf("完整程序应编译成功: stage=%s err=%s", res.Stage, res.Error)
	}
	if !strings.Contains(res.Stdout, "package main") {
		t.Errorf("stdout=%q", res.Stdout)
	}
	res2 := f.selfHostedGo("fmt.Println(\"hi\")", 铸剑炉_COMPILERS["go"], time.Now())
	if !res2.OK {
		t.Fatalf("片段应成功: %s", res2.Error)
	}
	res3 := f.selfHostedGo("package main\nimport \"fmt\"\nfunc main(){fmt.Println(1)}", 铸剑炉_COMPILERS["go"], time.Now())
	if !res3.OK {
		t.Fatalf("自带声明应成功: %s", res3.Error)
	}
}
