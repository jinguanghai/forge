package main

// fractal_cyclo_test.go — F5 圈复杂度维 (T35 P1, 20261017)
//
// 为什么需要 F5: F3 管「函数行数」, 但行数与分支密度是两件事 —— 把长函数按空行
// 切短, 分支一个没少。实测(20261017 全包 2992 个函数): p50=4 / p90=10 / p95=13 /
// p99=21 / max=50; 超 30 的有 8 个, 其中 nosword_cand.go 独占 2 个。
//
// 口径 (唯一, 可复现, 与 gocyclo 一致):
//
//	cyclomatic = 1 + if + for + range + case(非 default) + select comm(非 default)
//	             + && + ||
//	嵌套函数字面量里的分支计入外层函数 (与 gocyclo 相同)。
//
// 阈值 30 的来由 (结构意图, 不贴极值): 30 是 gocyclo 的默认阈值 —— 外部公认口径,
// 不是本项目拍的数字。实测极值 50 → 裕度 -20, 判据确实在报红 (有区分度)。
// 为什么不取 p95=13: 阈值一旦贴住分布重心, 一次性引入 91 条债务 —— 水位从 2 涨到 93,
// 基线就从「待修债务」退化成「台账」。这是 F1 恒真缺陷的反向同型: 判据活着, 但没人能修。
//
// 为什么实现放 _test.go: ① fractal_guard_test.go 已 457 行 (F2 限 500), 加不进去;
// ② 放生产文件会同时进 judgement_reach (生产侧零引用的顶层函数) 与判据台账
// (judgement_ledger.py 的 scan_go_consts 只扫非测试文件) —— 为一条判据制造两处
// 登记负担。结构约束本就是提交时拦, 归测试文件是它的正确归属。
//
// 验证等级: 度量口径由 TestFractalCyclomaticSelfCheck 用 8 个合成向量钉死 (含
// switch/select/逻辑运算/嵌套字面量), 判定接线由 TestFractalGuard 的基线子集断言
// 与变异清单两条 (阈值放宽 / 口径反转) 共同证明 —— 两层都有反例, 不靠「看着对」。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// fractalMaxCyclomatic 是 F5 的阈值: 单函数圈复杂度上限。
// 与 fractalMaxFuncLines 同属「单元有界」, 但量的是分支密度而非行数。
const fractalMaxCyclomatic = 30

// fractalCyclomatic 计算函数体圈复杂度 (gocyclo 口径, 见文件头)。
func fractalCyclomatic(body *ast.BlockStmt) int {
	n := 1
	ast.Inspect(body, func(nd ast.Node) bool {
		switch x := nd.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			n++
		case *ast.CaseClause:
			if x.List != nil { // default 的 List 为 nil, 不计
				n++
			}
		case *ast.CommClause:
			if x.Comm != nil { // default 不计
				n++
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

// fractalFuncCyclomatic 对单个函数声明取圈复杂度 (无函数体 = 外部声明, 记 1)。
func fractalFuncCyclomatic(fd *ast.FuncDecl) int {
	if fd.Body == nil {
		return 1
	}
	return fractalCyclomatic(fd.Body)
}

// TestFractalCyclomaticSelfCheck 是 F5 的「判据的判据」: 度量自身必须有区分度。
//
// 没有这层, 度量退化成常量 (返回 1 / 只计 default 分支) 时主守卫照样全绿 ——
// 违规变少永远满足「实际 ⊆ 基线」的子集判定。F1 曾因度量值域有上限而恒真数月,
// 同一个坑不再踩第二次。
func TestFractalCyclomaticSelfCheck(t *testing.T) {
	const src = `package p

func plain() {}

func oneIf(a bool) {
	if a {
	}
}

func ifElseIf(a, b bool) {
	if a {
	} else if b {
	}
}

func loops(xs []int) {
	for i := 0; i < 1; i++ {
	}
	for range xs {
	}
}

func sw(i int) {
	switch i {
	case 1:
	case 2:
	case 3:
	default:
	}
}

func sel(c chan int) {
	select {
	case <-c:
	default:
	}
}

func logic(a, b, c bool) { _ = a && b || c }

func nested(a bool) {
	f := func() {
		if a {
		}
	}
	f()
}
`
	want := map[string]int{
		"plain":    1, // 无分支
		"oneIf":    2,
		"ifElseIf": 3, // else-if 是嵌套 IfStmt
		"loops":    3, // for + range
		"sw":       4, // 3 个 case, default 不计
		"sel":      2, // 1 个 comm, default 不计
		"logic":    3, // && 与 || 各计 1
		"nested":   2, // 函数字面量里的 if 计入外层
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synth.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("合成源码解析失败: %v", err)
	}
	seen := 0
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		w, ok := want[fd.Name.Name]
		if !ok {
			t.Fatalf("合成源码出现未登记函数 %q —— 向量表与源码失配", fd.Name.Name)
		}
		seen++
		if got := fractalFuncCyclomatic(fd); got != w {
			t.Errorf("%s: 圈复杂度 %d, 期望 %d —— 度量口径漂移(gocyclo: 1+if/for/range/case/comm/&&/||)",
				fd.Name.Name, got, w)
		}
	}
	if seen != len(want) {
		t.Fatalf("只测到 %d 个函数, 向量表 %d 个 —— 有向量没跑到", seen, len(want))
	}
	t.Logf("F5 度量自检: %d 个合成向量全部命中 (含 switch/select/逻辑运算/嵌套字面量)", seen)
}
