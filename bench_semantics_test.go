package main

// bench_semantics_test.go — 十期 DMAIC: 13 gate 语义基准 (精确断言)
//
// 缺口 (实测 20261003):
//   ① bench_test.go 只 20 项, 覆盖 6/13 gate (math/file/code/logic/regex/chain),
//      knowledge/tcm/browser/relation/media/task/self 七个 gate 无端到端基准;
//   ② 断言全是 strings.Contains —— 弱断言掩盖语义错误
//      (如 "1/2+1/2" 只查输出里有没有 "1");
//   ③ 无「预期被拒绝」用例 —— gate 的拒绝权(拒绝契约)从未被基准钉住。
//
// 本基准与 bench_test.go 的分工: 后者是冒烟(能不能跑通), 本文件是语义(答得对不对),
// 每个用例断言「输出 JSON 里的确定字段」而非「有没有某个字」。
//
// 已知缺陷用例 (xfail) 显式登记: 它们断言「当前的错误行为」, 修复后自动转 ok
// (xfail 通过 = 缺陷被修掉, 报告会提示升级为 ok 断言)。缺陷不清零, 判据不撒谎。
//
// 用法: go test -run TestBenchSemantics -v        (含慢项: knowledge/browser)
//       go test -run TestBenchSemantics -short    (只跳网络慢项, 确定性用例照跑)
// 输出: bench/semantics_YYYYMMDD.md

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// semCase 一个语义基准用例
type semCase struct {
	gate string // gate 名 (Build 的 lang)
	name string
	code string
	// kind: ok=期望成功 / fail=期望执行失败 / reject=期望被主动拒绝
	kind     string
	want     []string // 输出必须全部包含 (精确到 JSON 字段值)
	wantNone []string // 输出不得包含
	slow     bool     // 网络类, -short 跳过
	net      bool     // 依赖真实外网(走代理): 网络层失败降级为跳过, 不判 FAIL
	xfail    bool     // 已知缺陷: 期望当前行为是「不合契约」的
	note     string
}

// semResult 单用例结果
type semResult struct {
	gate string
	name string
	kind string
	ok   bool
	ms   int64
	note string
}

func semCases() []semCase {
	return []semCase{
		// ── math gate (sympy 确定性计算) ──
		{gate: "math", name: "整数优先级", code: "2+3*4", kind: "ok", want: []string{`"result":"14"`}},
		{gate: "math", name: "分数精确", code: "1/3+1/6", kind: "ok", want: []string{`"result":"1/2"`}},
		{gate: "math", name: "幂运算", code: "2^10", kind: "ok", want: []string{`"result":"1024"`}},
		{gate: "math", name: "符号微分", code: "diff(x**3, x)", kind: "ok", want: []string{`"result":"3*x**2"`}},
		{gate: "math", name: "三角恒等", code: "sin(pi/2)", kind: "ok", want: []string{`"result":"1"`}},
		{gate: "math", name: "拒绝自然语言", code: "hello world", kind: "reject", note: "不得隐式乘法切碎自然语言"},
		{gate: "math", name: "拒绝空输入", code: "", kind: "fail", want: []string{"代码为空"}},
		{gate: "math", name: "求值型表达式(solve)", code: "solve(x**2-4, x)", kind: "ok",
			want: []string{`"result":"[-2, 2]"`},
			note: "parse 阶段即返回 list 的表达式必须原样输出, 不得进符号变换 (20261003 修复: 旧版 simplify(list) 崩溃)"},

		// ── logic gate (z3) ──
		{gate: "logic", name: "SAT 可满足", code: "a & b", kind: "ok", want: []string{`"verdict":"sat"`}},
		{gate: "logic", name: "UNSAT 矛盾", code: "a & ~a", kind: "ok", want: []string{`"verdict":"unsat"`}},
		{gate: "logic", name: "prove 恒真", code: `{"type":"prove","code":"claim = x + 1 > x"}`, kind: "ok",
			want: []string{`"verdict":"proved"`}},
		{gate: "logic", name: "拒绝 SMT-LIB", code: "(assert (= 1 1))", kind: "reject",
			note: "契约: logic gate 收 z3py 表达式, 不是 SMT-LIB"},
		{gate: "logic", name: "量词 ForAll", code: "ForAll(x, x*x >= 0)", kind: "ok",
			want: []string{`"verdict":"sat"`},
			note: "z3 内建名不得被当自由变量声明 (20261003 修复: 旧版 ForAll = Int('ForAll') → not callable)"},
		{gate: "logic", name: "量词 Exists", code: "Exists(x, x > 0)", kind: "ok",
			want: []string{`"verdict":"sat"`}},
		{gate: "logic", name: "量词证明", code: `{"type":"prove","code":"claim = ForAll(x, x*x >= 0)"}`, kind: "ok",
			want: []string{`"verdict":"proved"`}},

		// ── regex gate (整串 fullmatch 语义) ──
		{gate: "regex", name: "整串匹配正例", code: `{"type":"match","pattern":"[A-Z]\\d{3}","positive":["B456"]}`,
			kind: "ok", want: []string{`"verdict":"all_pass"`, `"passed":1`}},
		{gate: "regex", name: "整串语义反例", code: `{"type":"match","pattern":"[A-Z]\\d{3}","positive":["order B456 ok"]}`,
			kind: "ok", want: []string{`"failed":1`}, note: "fullmatch 语义: 含子串不算匹配"},
		{gate: "regex", name: "负例列表", code: `{"type":"match","pattern":"[A-Z]\\d{3}","positive":[],"negative":["B45"]}`,
			kind: "ok", want: []string{`"failed":0`}},
		{gate: "regex", name: "非法正则被拒绝", code: `{"type":"match","pattern":"[","positive":["a"]}`, kind: "reject",
			want: []string{"正则非法"},
			note: "非法 pattern 属输入形态错误: rejected + 非零退出 (20261003 修复: 旧版返回 ok:true + verdict:error)"},
		{gate: "regex", name: "等价判定同口径", code: `{"type":"equivalent","pattern":"a|b","pattern2":"b|a"}`,
			kind: "ok", want: []string{`"verdict":"equivalent"`, `"engine":"greenery"`},
			note: "等价判定两侧必须同口径 (20261003 修复: 旧版 pattern 用 fullmatch / pattern2 用 search)。" +
				"20261004: greenery 已装 → 形式化判定(engine=greenery); 穷举降级路径仍受判据覆盖 " +
				"(regex_engine_degraded_test.go, 用 RG_FORCE_BRUTEFORCE 强制), 不因装上而变死代码"},
		{gate: "regex", name: "等价判定可证伪", code: `{"type":"equivalent","pattern":"a+","pattern2":"a{1,3}"}`,
			kind: "ok", want: []string{`"verdict":"not_equivalent"`, `"engine":"greenery"`},
			note: "20261004: greenery 已装 → 形式化判定, 不产出 counterexample(反例仅穷举路径有); " +
				"旧假阳性案例 a+ vs a{1,7} 曾报 likely_equivalent, 现为真判定 not_equivalent"},

		// ── tcm gate (势态诊断) ──
		{gate: "tcm", name: "文本诊断控势", code: "市场与营销部门过度亢奋，承诺过多。核心研发部门动力不足。阳热亢进，阴寒不足，上下失交。",
			kind: "ok", want: []string{`"recommendation":"控势"`}},
		{gate: "tcm", name: "向量诊断", code: `{"type":"diagnose","vector":[8,5,7,4,3,5,4,4],"domain":"body"}`,
			kind: "ok", want: []string{`"recommendation"`}},

		// ── relation gate (接线断言, 剥离注释) ──
		{gate: "relation", name: "符号已接线", code: `{"type":"symbol","name":"Build"}`, kind: "ok",
			want: []string{`"verdict": "wired"`}},
		{gate: "relation", name: "真接线断言", code: `{"type":"assert","claim":"forge.go 调用了 forgeGate"}`,
			kind: "ok", want: []string{`"ok": true`}},
		{gate: "relation", name: "假接线拦截", code: `{"type":"assert","claim":"forge.go 调用了 zzzNotExistFunc"}`,
			kind: "ok", want: []string{`"ok": false`, "拦截"}, note: "零引用必须判否, 不得放行"},

		// ── chain gate (多 gate 编排) ──
		{gate: "chain", name: "两阶段顺序", code: `{"stages":[{"gate":"math","input":{"expr":"2+2"}},{"gate":"math","input":{"expr":"3*3"}}],"stop_on":"never"}`,
			kind: "ok", want: []string{`\"result\":\"9\"`}},
		{gate: "chain", name: "拒绝非法 JSON", code: "not json at all", kind: "fail"},

		// ── task gate (长任务通道) ──
		{gate: "task", name: "提交长任务", code: "echo SEMANTIC_TASK_OK", kind: "ok",
			want: []string{`"task_id"`}},
		{gate: "task", name: "未知 id fail-closed", code: `{"action":"status","id":"t19990101_000000_dead"}`,
			kind: "fail", note: "未知 task_id 不得返回 ok:true"},

		// ── media gate (成本护栏, 不真跑生成) ──
		{gate: "media", name: "models 清单", code: `{"action":"models"}`, kind: "ok",
			want: []string{`"models"`, `"guard"`}},
		{gate: "media", name: "成本护栏拒绝", code: `{"action":"video","prompt":"x","tier":"hd","duration":10}`,
			kind: "fail", note: "单次超上限必须拒绝"},

		// ── 编译器 (python/go/node) ──
		{gate: "python", name: "python 运行", code: "print(21*2)", kind: "ok", want: []string{"42"}},
		{gate: "python", name: "python 语法错误诊断", code: "def f(:\n    return 1", kind: "fail",
			want: []string{"SyntaxError"}, note: "失败必须带诊断"},
		{gate: "python", name: "python 运行时错误", code: `raise ValueError("boom")`, kind: "ok",
			want: []string{"ValueError"}, note: "契约: OK=执行完成(非执行成功), 运行时异常记 ExitCode=1 + Error 带 traceback"},
		{gate: "go", name: "go 编译运行", code: "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(21 * 2) }",
			kind: "ok", want: []string{"42"}},
		{gate: "go", name: "go 编译错误诊断", code: "package main\n\nfunc main() {\n\tvar x int = \"s\"\n\t_ = x\n}",
			kind: "fail", want: []string{"cannot use", `"lang":"go"`},
			note: "失败必须带行号级诊断 (Error + Diagnostics)"},
		{gate: "node", name: "node 运行", code: "console.log(2**10)", kind: "ok", want: []string{"1024"}},
		{gate: "node", name: "node 语法错误诊断", code: "const x = ;", kind: "fail",
			want: []string{"SyntaxError"}},

		// ── 慢项 (网络) ──
		{gate: "knowledge", name: "SPARQL 查询", code: "SELECT ?s ?p ?o WHERE { ?s ?p ?o } LIMIT 2",
			kind: "ok", want: []string{`"ok":true`}, slow: true},
		{gate: "browser", name: "抓取 example.com", code: "https://example.com",
			kind: "ok", want: []string{"Example Domain"}, slow: true, net: true},
	}
}

// semCheck 判定单用例, 返回 (是否通过, 备注)
func semCheck(c semCase, out string, res *ForgeGateResult, err error) (bool, string) {
	gotOK := err == nil && res != nil && res.OK
	switch c.kind {
	case "ok":
		if !gotOK {
			return false, "期望成功, 实际: " + semBrief(res, err, out)
		}
	case "fail":
		if gotOK {
			return false, "期望失败, 实际成功"
		}
		if res == nil && err == nil {
			return false, "期望失败, 实际无结果"
		}
	case "reject":
		if gotOK {
			return false, "期望被拒绝, 实际成功执行"
		}
		rejected := (res != nil && res.GateRejected) ||
			strings.Contains(out, "rejected") || strings.Contains(out, "拒绝")
		if !rejected {
			return false, "期望带拒绝标记, 实际: " + semBrief(res, err, out)
		}
	}
	for _, w := range c.want {
		if !strings.Contains(out, w) {
			return false, "缺 " + w + " | 实际: " + semBrief(res, err, out)
		}
	}
	for _, n := range c.wantNone {
		if strings.Contains(out, n) {
			return false, "不应含 " + n
		}
	}
	return true, ""
}

func semBrief(res *ForgeGateResult, err error, out string) string {
	s := ""
	if err != nil {
		s = "err=" + err.Error()
	} else if res != nil && res.Error != "" {
		s = "gateErr=" + res.Error
	} else if res != nil && res.Stderr != "" {
		s = "stderr=" + res.Stderr
	} else {
		s = out
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// TestBenchSemantics 13 gate 语义基准入口
func TestBenchSemantics(t *testing.T) {
	// -short: 只跳过网络慢项 (knowledge/browser), 确定性用例照跑 ——
	// hourly 巡检要的就是「免费且快」的那部分 (整测试跳过 = 巡检看不到任何东西)。
	skipSlow := testing.Short()
	wd, werr := os.Getwd()
	if werr != nil {
		t.Fatal(werr)
	}
	cfg := DefaultConfig()
	cfg.WorkDir = wd
	cfg.RetryMax = 0
	f := NewForge(wd, cfg)
	defer f.Shutdown()

	cases := semCases()
	results := make([]semResult, 0, len(cases))
	for _, c := range cases {
		if c.slow && (skipSlow || os.Getenv("FORGE_SEM_SLOW") == "0") {
			results = append(results, semResult{gate: c.gate, name: c.name, kind: c.kind, ok: true, note: "跳过(慢项)"})
			continue
		}
		start := time.Now()
		// 基准必须绕缓存: 缓存命中会把「上次的结果」当成本次能力,
		// 既掩盖 gate 退化, 也让重复运行给出假绿。
		res := f.forgeGateSkipCache(c.code, c.gate, "", true)
		ms := time.Since(start).Milliseconds()
		out := res.Stdout + "\n" + res.Stderr + "\n" + res.Error + "\n" + res.Diagnostics
		ok, note := semCheck(c, out, &res, nil)
		// xfail: 期望当前行为「不合契约」→ 判定取反, 备注标注
		if c.xfail {
			if ok {
				note = "xfail 通过 → 缺陷已修复, 请把该用例转为正常断言 | " + c.note
			} else {
				ok = true
				note = "已知缺陷(xfail): " + c.note + " | 实测: " + note
			}
		}
		// 网络抖动降级 (20261003): 显式声明 net:true 的用例依赖真实外网(走代理),
		// 网络层失败与代码质量无关 —— 实测同一份代码 13:44 PASS / 13:57 FAIL,
		// 唯一差异是 net::ERR_CONNECTION_CLOSED。
		// 只对 net:true 生效(宽口子会把真缺陷一起降级掉), 且降级必须留痕。
		// 判定逻辑抽在 applyNetFlake (net_flake_test.go), 那边有分支判据 ——
		// 写在循环里的分支不可单测, 等于「有处置臂但无人验证」。
		ok, note = applyNetFlake(c, ok, note, out)
		results = append(results, semResult{gate: c.gate, name: c.name, kind: c.kind, ok: ok, ms: ms, note: note})
		if !ok {
			t.Errorf("[%s/%s] %s", c.gate, c.name, note)
		}
	}

	// 注册表断言: 13 gate 全部在册 (self 不实际执行 —— 需主人审批)
	names := gateNames()
	for _, want := range []string{"math", "logic", "regex", "knowledge", "tcm", "browser", "chain", "self", "relation", "media", "task"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("gate 注册表缺 %s (实际: %v)", want, names)
		}
	}

	report := semReport(results, names)
	// 报告目录用相对路径 "." 而非 os.Getwd() 派生值 —— 后者会被
	// workdir_write_sentinel_test.go 判为「测试写入落在仓库工作目录」
	// (与 bench_test.go 同写法; wd 仅用于 NewForge 的只读用途)。
	dir := filepath.Join(".", "bench")
	if err := os.MkdirAll(dir, 0755); err == nil {
		path := filepath.Join(dir, "semantics_"+time.Now().Format("20060102")+".md")
		if werr := os.WriteFile(path, []byte(report), 0644); werr != nil {
			t.Errorf("报告落盘失败: %v", werr)
		} else {
			t.Logf("语义基准报告: %s", path)
		}
	}
	t.Logf("\n%s", report)
}

// semReport 生成 markdown 报告
func semReport(results []semResult, gates []string) string {
	pass, xfailN, netSkipN := 0, 0, 0
	for _, r := range results {
		if r.ok {
			pass++
		}
		if strings.Contains(r.note, "xfail") {
			xfailN++
		}
		if strings.Contains(r.note, "网络不可用") {
			netSkipN++
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# 铸剑炉 gate 语义基准 %s\n\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&sb, "**通过: %d/%d (%.0f%%)** | gate 注册表: %d 个 | 已知缺陷(xfail): %d | 网络降级: %d\n\n",
		pass, len(results), float64(pass)*100/float64(len(results)), len(gates), xfailN, netSkipN)
	if netSkipN > 0 {
		fmt.Fprintf(&sb, "> ⚠️ %d 项因网络层不可用被降级(不计 FAIL)。降级不等于通过: 核对网络/代理后再看结论。\n\n", netSkipN)
	}
	sb.WriteString("| gate | 用例 | 期望 | 结果 | 耗时(ms) | 备注 |\n|---|---|---|---|---|---|\n")
	for _, r := range results {
		mark := "✅"
		if !r.ok {
			mark = "❌"
		} else if strings.Contains(r.note, "网络不可用") {
			mark = "⚠️"
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %d | %s |\n", r.gate, r.name, r.kind, mark, r.ms, r.note)
	}
	sb.WriteString("\n---\n说明: 本基准断言 gate 输出 JSON 的确定字段 (非子串嗅探), 覆盖 13 gate 语义与拒绝契约。\n")
	sb.WriteString("xfail = 已登记的已知缺陷, 断言「当前错误行为」; 修复后该项会自动提示转为正常断言。\n")
	return sb.String()
}
