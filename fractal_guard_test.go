package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ══════════════════════════════════════════════════════════════════
// 分形守卫 (Fractal Guard) —— 永久底层结构约束
//
// 分形 = 自相似 + 尺度不变。本守卫把"结构美感"翻译成死程序能判的四条规则:
//
//	F1 自相似   顶层结构指纹 ∈ 有限模板集 (复杂指纹 = 上帝文件)
//	F2 尺度不变 文件规模有上限, 无超大尺度
//	F3 单元有界 单文件 ≤500 行, 单函数 ≤150 行
//	F4 测试镜像 实现文件应有同名测试 (自相似在测试层的体现)
//
// 判定权完全在死程序; 存量违规以"豁免清单"外置 (.forge/fractal_baseline.json)。
// 清单只能收缩不能扩张 —— 新增违规立即失败, 改造后清单可手工删条目。
// ══════════════════════════════════════════════════════════════════

const (
	fractalMaxFileLines    = 500
	fractalMaxFuncLines    = 150
	fractalComplexShapeLen = 6
	fractalBaselinePath    = ".forge/fractal_baseline.json"
	fractalDumpBegin       = "<<<FRACTAL_JSON_BEGIN>>>"
	fractalDumpEnd         = "<<<FRACTAL_JSON_END>>>"
)

type fractalViolation struct {
	Rule  string `json:"rule"`
	Item  string `json:"item"`
	Val   int    `json:"val"`
	Limit int    `json:"limit"`
}

func (v fractalViolation) key() string {
	return v.Rule + "|" + v.Item
}

func (v fractalViolation) String() string {
	if v.Rule == "F4" {
		return "F4  " + v.Item + "  无对应测试"
	}
	return fmt.Sprintf("%s  %-30s %5d > %d", v.Rule, v.Item, v.Val, v.Limit)
}

type fractalBaseline struct {
	Violations []fractalViolation `json:"violations"`
}

// fractalShape 把文件顶层声明序列折叠成指纹。
//
//	"VF"      = 若干变量后跟若干函数      (普通模块)
//	"TMFM..." = 类型/方法交替            (上帝文件征兆)
func fractalShape(f *ast.File) string {
	var sb strings.Builder
	prev := byte(0)
	for _, d := range f.Decls {
		var k byte
		switch dd := d.(type) {
		case *ast.FuncDecl:
			if dd.Recv != nil {
				k = 'M'
			} else {
				k = 'F'
			}
		case *ast.GenDecl:
			switch dd.Tok {
			case token.TYPE:
				k = 'T'
			case token.VAR:
				k = 'V'
			case token.CONST:
				k = 'C'
			case token.IMPORT:
				continue
			default:
				k = '?'
			}
		default:
			k = '?'
		}
		if k != prev {
			sb.WriteByte(k)
			prev = k
		}
	}
	return sb.String()
}

// fractalBase 剥离平台后缀, 用于测试配对判定。
func fractalBase(name string) string {
	b := strings.TrimSuffix(name, ".go")
	b = strings.TrimSuffix(b, "_test")
	for _, suf := range []string{"_windows", "_other", "_linux", "_darwin"} {
		if strings.HasSuffix(b, suf) {
			return strings.TrimSuffix(b, suf)
		}
	}
	return b
}

// fractalCheckFile 单文件判定 —— 自检测试走的就是这条路径。
func fractalCheckFile(fset *token.FileSet, name string, f *ast.File) []fractalViolation {
	var vs []fractalViolation

	if lines := fset.Position(f.End()).Line; lines > fractalMaxFileLines {
		vs = append(vs, fractalViolation{"F2", name, lines, fractalMaxFileLines})
	}
	if shape := fractalShape(f); len(shape) > fractalComplexShapeLen {
		vs = append(vs, fractalViolation{"F1", name, len(shape), fractalComplexShapeLen})
	}
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		n := fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
		if n > fractalMaxFuncLines {
			vs = append(vs, fractalViolation{"F3", name + ":" + fd.Name.Name, n, fractalMaxFuncLines})
		}
	}
	return vs
}

// fractalScan 扫描包目录, 返回全部违规 (尚未与基线比对)。
func fractalScan(dir string) ([]fractalViolation, int, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	var vs []fractalViolation
	implBase := map[string]bool{}
	testBase := map[string]bool{}
	nFiles := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, "_test.go") {
			testBase[fractalBase(name)] = true
			continue
		}
		implBase[fractalBase(name)] = true

		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, 0, fmt.Errorf("解析 %s 失败: %w", name, err)
		}
		nFiles++
		vs = append(vs, fractalCheckFile(fset, name, f)...)
	}

	for b := range implBase {
		if !testBase[b] {
			vs = append(vs, fractalViolation{"F4", b + ".go", 0, 1})
		}
	}

	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Rule != vs[j].Rule {
			return vs[i].Rule < vs[j].Rule
		}
		return vs[i].Item < vs[j].Item
	})
	return vs, nFiles, nil
}

// fractalDump 打印候选基线清单 (机器可读, 由死程序生成, 非人工誊抄)。
func fractalDump(t *testing.T, vs []fractalViolation) {
	t.Helper()
	bl := fractalBaseline{Violations: vs}
	if bl.Violations == nil {
		bl.Violations = []fractalViolation{}
	}
	b, _ := json.MarshalIndent(bl, "", " ")
	fmt.Println(fractalDumpBegin)
	fmt.Println(string(b))
	fmt.Println(fractalDumpEnd)
	t.Logf("候选基线清单已打印 (%d 条), 请写入 %s", len(bl.Violations), fractalBaselinePath)
}

// TestFractalGuard 主守卫: 实际违规必须是基线的子集。
func TestFractalGuard(t *testing.T) {
	vs, nFiles, err := fractalScan(".")
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}

	raw, err := os.ReadFile(fractalBaselinePath)
	if err != nil {
		fractalDump(t, vs)
		t.Fatalf("基线清单缺失 (%v) —— 当前清单已打印, 请写入 %s", err, fractalBaselinePath)
	}
	var bl fractalBaseline
	if err := json.Unmarshal(raw, &bl); err != nil {
		t.Fatalf("基线清单格式错误: %v", err)
	}

	allowed := map[string]bool{}
	for _, v := range bl.Violations {
		allowed[v.key()] = true
	}
	cur := map[string]bool{}
	var added []fractalViolation
	for _, v := range vs {
		cur[v.key()] = true
		if !allowed[v.key()] {
			added = append(added, v)
		}
	}
	var healed []fractalViolation
	for _, v := range bl.Violations {
		if !cur[v.key()] {
			healed = append(healed, v)
		}
	}

	if len(added) > 0 {
		t.Errorf("❌ 新增结构违规 %d 条 —— 分形退化:", len(added))
		for _, v := range added {
			t.Errorf("     %s", v)
		}
	}
	if len(healed) > 0 {
		t.Logf("✅ 基线可收缩: %d 条已修复, 请从 %s 删除:", len(healed), fractalBaselinePath)
		for _, v := range healed {
			t.Logf("     %s", v)
		}
	}
	t.Logf("分形守卫: 实现文件 %d 个 | 基线允许 %d 条 | 实际违规 %d 条", nFiles, len(bl.Violations), len(vs))
}

// TestFractalShapeRule 自检1: 指纹计算是否正确。
func TestFractalShapeRule(t *testing.T) {
	cases := []struct{ src, want string }{
		{"package p\n\nimport \"fmt\"\n\nvar a = 1\n\nfunc f() { fmt.Println(a) }\n", "VF"},
		{"package p\n\ntype T struct{}\n\nfunc (t T) M() {}\n\nfunc (t T) N() {}\n", "TM"},
		{"package p\n\nconst C = 1\n\ntype T int\n\nfunc (t T) M() {}\n\nfunc (t T) N() {}\n", "CTM"},
		{"package p\n\nfunc a() {}\n\nfunc b() {}\n\nfunc c() {}\n", "F"},
	}
	for i, c := range cases {
		f, err := parser.ParseFile(token.NewFileSet(), fmt.Sprintf("case%d.go", i), c.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("用例 %d 解析失败: %v", i, err)
		}
		if got := fractalShape(f); got != c.want {
			t.Errorf("用例 %d: shape=%q, want %q", i, got, c.want)
		}
	}
}

// TestFractalBaseRule 自检2: 平台后缀剥离。
func TestFractalBaseRule(t *testing.T) {
	cases := map[string]string{
		"asr.go":                   "asr",
		"asr_other.go":             "asr",
		"asr_windows.go":           "asr",
		"readline_windows.go":      "readline",
		"readline_other.go":        "readline",
		"nosword.go":               "nosword",
		"nosword_test.go":          "nosword",
		"asr_other_test.go":        "asr",
		"readline_windows_test.go": "readline",
	}
	for in, want := range cases {
		if got := fractalBase(in); got != want {
			t.Errorf("fractalBase(%q)=%q, want %q", in, got, want)
		}
	}
}

// TestFractalThresholdRule 自检3: 阈值判定真的能抓到 —— 走真实判定路径。
func TestFractalThresholdRule(t *testing.T) {
	// 构造 620 行的单函数文件: 必须同时触发 F2(文件) 与 F3(函数)
	var sb strings.Builder
	sb.WriteString("package p\n\nfunc big() {\n")
	for i := 0; i < 600; i++ {
		sb.WriteString("\t_ = 1\n")
	}
	sb.WriteString("}\n")

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", sb.String(), parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("合成文件解析失败: %v", err)
	}
	got := map[string]bool{}
	for _, v := range fractalCheckFile(fset, "synthetic.go", f) {
		got[v.Rule] = true
	}
	if !got["F2"] {
		t.Errorf("合成 620 行文件未触发 F2 —— 文件行数判定失效")
	}
	if !got["F3"] {
		t.Errorf("合成 620 行函数未触发 F3 —— 函数长度判定失效")
	}

	// 反向: 合规小文件不得误报
	small, err := parser.ParseFile(token.NewFileSet(), "small.go", "package p\n\nfunc s() { _ = 1 }\n", parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("小文件解析失败: %v", err)
	}
	if vs := fractalCheckFile(token.NewFileSet(), "small.go", small); len(vs) != 0 {
		t.Errorf("合规小文件被误报 %d 条: %v", len(vs), vs)
	}
}
