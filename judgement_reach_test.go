package main

// judgement_reach_test.go — 「判据写了但不跑」元判据 (T53, 20261014)
//
// 病根: 判据有三种形态 —— ①有判据 ②没判据 ③**写了、测了、但不跑**。
// 第三种最隐蔽: 源码在、测试全绿, 而生产侧零调用 -> 链接器 DCE 把它整棵剔除
// (实测 memory_budget.go: go tool nm 里 memoryBudget* 符号 0 个) -> 那次源码
// 改动对 exe 零行为影响, 而「测试全绿」给出的是「已经守住」的错觉。
//
// 判据: 生产侧零引用、仅测试引用的顶层函数, 必须逐条登记定位:
//   runtime = 运行时拦 (必须生产可达; 出现在候选里即为缺陷)
//   commit  = 提交时拦 (测试期判据), 生产不可达属设计
//   mirror  = 跨语言口径镜像 / 对照实现 (生产判据在 Python 侧)
//   dup     = 功能已被别处内联等价接管 (重复实现)
//   helper  = 测试辅助 (名字带 ForTest / 重置全局态)
//
// 双向: 候选不在清单 -> 报红 (新写的「不跑的判据」); 清单项已可达/已删除 ->
// 也报红 (清单腐烂, 防「登记了就当没看见」)。
//
// 为什么是 Go 哨兵而非 Python 脚本: defense_system/deadcode_scan.py 只扫生产
// 文件内部(排除 _test.go), 天然看不见「仅测试引用」这一类 —— 盲区正是 T53。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// judgementReachExempt 登记「生产侧零引用、仅测试引用」的顶层函数及其定位。
//
// 新增条目必须回答: 它是「运行时拦」还是「提交时拦」? 若答不上来, 默认按
// 运行时拦处理 —— 那就要接线, 而不是登记。
var judgementReachExempt = map[string]string{
	"keyFindingsMissingTS":  "commit: 提交时报红 (TestKeyFindingsAllHaveTS 读真实 memory.json 断言)",
	"auditEventNames":       "commit: 提交时拦 (audit_schema_test.go 用它对审计 event 名单做完整性断言)",
	"auditEventRequired":    "commit: 提交时拦 (agent_audit_test.go 用 it 断言生产写入的必填字段齐全)",
	"routeForURL":           "mirror: 跨语言口径镜像 (生产判据在 Python netroute.py; 本函数供向量对照钉住两侧一致)",
	"nswIsCandidate":        "mirror: 一期兼容入口, nosword_wire_test.go 已登记「仅测试调用, 不得删除」",
	"cookedBaseline":        "dup: 功能已被 console_guard_windows.go 的 ensureCookedForApproval 内联等价接管",
	"reloadHolidaysForTest": "helper: 测试辅助 (名字带 ForTest, 节假日表用例专用)",
	"resetReqShape":         "helper: 测试隔离 (清进程内前缀基线, 每个 cache 用例的 Cleanup)",
}

// judgementReachSkipDirs 扫描时跳过的目录 (与 deadcode_scan.py 同口径)。
var judgementReachSkipDirs = map[string]bool{
	".git": true, "_archive": true, ".forge": true, ".forge-temp": true,
	"node_modules": true, "deno": true, "tcc": true, "snapshots": true,
}

// scanTestOnlyFuncs 返回「生产侧零引用、仅测试引用」的顶层函数 -> 定义文件。
//
// 口径: 直接引用计数 (不做传递闭包) —— 宁可漏报(保守)不可误报(狼来了)。
func scanTestOnlyFuncs(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	prodRef, testRef := map[string]int{}, map[string]int{}
	defCnt := map[string]int{}
	defFile := map[string]string{}
	root := "."
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && judgementReachSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		af, perr := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("解析 %s 失败: %v", p, perr)
		}
		isTest := strings.HasSuffix(p, "_test.go")
		ast.Inspect(af, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			if isTest {
				testRef[id.Name]++
			} else {
				prodRef[id.Name]++
			}
			return true
		})
		if isTest {
			return nil
		}
		base := filepath.Base(p)
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Name == nil {
				continue
			}
			defCnt[fd.Name.Name]++
			defFile[fd.Name.Name] = base
		}
		return nil
	})
	if err != nil {
		t.Fatalf("扫描 Go 源码失败: %v", err)
	}
	out := map[string]string{}
	for name, dc := range defCnt {
		if name == "init" || name == "main" {
			continue // Go 运行时自动调用, 不靠显式引用
		}
		if prodRef[name]-dc > 0 {
			continue
		}
		if testRef[name] == 0 {
			continue // 连测试都不用 = 纯死代码, 归 deadcode_scan.py 管
		}
		out[name] = defFile[name]
	}
	return out
}

// TestJudgementReach_NoUnregisteredTestOnlyFunc 核心判据 (双向)。
func TestJudgementReach_NoUnregisteredTestOnlyFunc(t *testing.T) {
	got := scanTestOnlyFuncs(t)
	var unreg, stale []string
	for name, file := range got {
		if _, ok := judgementReachExempt[name]; !ok {
			unreg = append(unreg, name+" ("+file+")")
		}
	}
	for name := range judgementReachExempt {
		if _, ok := got[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(unreg)
	sort.Strings(stale)
	if len(unreg) > 0 {
		t.Errorf("发现未登记的「写了但不跑」判据 %d 个 (生产零引用 / 仅测试引用):\n  %s\n"+
			"处置: 接线到生产入口(如 preflightDeny), 或在 judgementReachExempt 登记定位与理由。",
			len(unreg), strings.Join(unreg, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("豁免清单腐烂 %d 项 (已可达或已删除, 应清理):\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
	t.Logf("扫描完成: 候选 %d 个, 全部已登记", len(got))
}

// TestJudgementReach_ExemptHasReason 豁免必须带定位前缀 (防「登记了就当没看见」)。
//
// 变异自检: 把任一条目的 "commit:"/"mirror:"/"dup:"/"helper:" 前缀删掉 -> 报红。
func TestJudgementReach_ExemptHasReason(t *testing.T) {
	valid := []string{"runtime:", "commit:", "mirror:", "dup:", "helper:"}
	if len(judgementReachExempt) == 0 {
		t.Fatal("豁免清单为空 —— 要么判据真的全接线了, 要么清单被清空")
	}
	for name, why := range judgementReachExempt {
		ok := false
		for _, p := range valid {
			if strings.HasPrefix(why, p) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("豁免 %s 的理由缺定位前缀 (runtime/commit/mirror/dup/helper): %q", name, why)
		}
	}
}
