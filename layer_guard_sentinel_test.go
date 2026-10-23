package main

// layer_guard_sentinel_test.go — 测试分层齐备哨兵 (20261016)
//
// 缺口 (20261016 实测): 铸剑炉的"分层测试"有两套含义 ——
//
//	① 判据维度分层 (logic / adversarial / structural / drift / runtime):
//	   显式且已程序化 —— 定义在 defense_system/judgement_semantics.json,
//	   落地在 judgement_ledger.json (147 条 + 55 元判据)。
//	② 执行载体分层 (单元 / 哨兵 / 端到端 / 补充覆盖 / 变异 / 巡检 / Python 自检):
//	   隐式存在。每一角各自有判据, 却没有任何一条断言"七层齐备"。
//
// ② 的缺口是实打实的: 把 31 个哨兵测试整体删掉, fractal 的 F4 只问
// "实现文件有没有同名测试"(删测试文件不触发), hourly_wiring 只看调度清单
// (与测试文件无关), mutation_probe_sentinel 只看自己的 manifest ——
// 三处全绿, 而防御纵深已少一整层。按公理三 (架构即测试), ② 当时只是"愿望"。
//
// 本哨兵把 ② 显式化: 层清单写死在源码 (layer_guard_scan_test.go, 单一数据源) + 七条判据。
//
//	J1 齐备   layerSpecFloor=7: 层声明数不得低于水位 (删一层 = 显式决策)
//	J2 水位   每层成员数 >= MinCount (棘轮, 只升不降)
//	J3 守恒   每个 *_test.go 唯一归属某一层; 目录枚举数 == 分类汇总数
//	J4 活证据 每层不能只有"文件存在": Go 文件须含顶层函数且层内须有 Test/Benchmark,
//	          变异条目字段齐全且 id 不重复, 巡检脚本须真实存在, Python 自检须有入口
//	J5 元判据 分类函数不可恒真/恒假 (每层至少一个正例样本); 水位不可 <= 0
//	J6 自指   本文件自身必须落在 L2-sentinel 层 (哨兵也是层成员, 不搞例外)
//	J7 无盲区 全仓含 *_test.go 的目录必须都在 layerScanRoots 里
//
// 实测数据 (20261016, 排除 .git/_archive/.forge/.forge-temp/snapshots/node_modules):
//
//	L1-unit 255  L2-sentinel 32  L3-e2e 11  L4-supplement 28
//	L5-mutation 58  L6-hourly 27  L7-pycheck 13
//	Go 测试文件合计 326, 分布在 3 个目录 (., web/gateway, defense_system/logic_guard)
//
// 已知边界: L1~L4 是 Go 测试文件的互斥完备划分 (参与 J3 守恒);
// L5~L7 是其它载体的判据层, 各自独立枚举, 与 L1~L4 可重叠, 不参与守恒。
// 判据自身的判据: 见 defense_system/mutation_manifest.json 的 layer_* 条目
// (把本文件的层清单/水位/分类逻辑改瞎时, 这些判据必须报红)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// J1 齐备: 层声明数不得低于水位, 层名唯一, 描述字段齐全。
func TestLayerGuard_SpecsComplete(t *testing.T) {
	if len(testLayerSpecs) < layerSpecFloor {
		t.Errorf("层声明数 %d < 水位 %d: 删层必须同时改 layerSpecFloor (显式决策, 不是静默收缩)",
			len(testLayerSpecs), layerSpecFloor)
	}
	seen := map[string]bool{}
	for _, s := range testLayerSpecs {
		if seen[s.Name] {
			t.Errorf("层名重复: %s", s.Name)
		}
		seen[s.Name] = true
		if s.Kind == "" || s.Why == "" {
			t.Errorf("层 %s 缺 kind/why 字段 —— 层清单退化为装饰品", s.Name)
		}
	}
}

// J2 水位: 每层成员数不得低于 MinCount。
func TestLayerGuard_CountsAtFloor(t *testing.T) {
	counts, _, err := layerObservedCounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range testLayerSpecs {
		if got := counts[spec.Name]; got < spec.MinCount {
			t.Errorf("%s 成员数 %d < 水位 %d —— 该层被拆除/缩水 (%s); 若确为重组合并, 请显式下调水位并留痕",
				spec.Name, got, spec.MinCount, spec.Why)
		}
	}
}

// J3 守恒: 每个 *_test.go 必须唯一归属某一层; 目录枚举数 == 分类汇总数。
func TestLayerGuard_NoOrphanTestFiles(t *testing.T) {
	goFiles, err := layerScanGoFiles(layerScanRoots)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, fs := range goFiles {
		total += len(fs)
	}
	raw := 0
	for _, d := range layerTestDirsUnderRoot() {
		entries, err := os.ReadDir(d)
		if err != nil {
			t.Fatalf("列目录 %s 失败: %v", d, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			raw++
			if layerClassifyTestFile(e.Name()) == "" {
				t.Errorf("孤儿测试文件 (不属于任何层): %s", filepath.ToSlash(filepath.Join(d, e.Name())))
			}
		}
	}
	if raw != total {
		t.Errorf("守恒失败: 目录枚举 %d 个 *_test.go, 扫描根分类汇总 %d 个 —— 扫描根漏了目录 (隐式盲区)",
			raw, total)
	}
}

// J4 活证据: 每层不能只有"文件存在"。
func TestLayerGuard_LiveEvidence(t *testing.T) {
	_, bad, err := layerObservedCounts()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}

// J5-a 元判据: 分类函数不可恒真/恒假 —— 每个 go-file 层至少一个正例样本。
func TestLayerGuard_ClassifyNotVacuous(t *testing.T) {
	cases := map[string]string{
		"anchor_fields_sentinel_test.go": "L2-sentinel",
		"layer_guard_sentinel_test.go":   "L2-sentinel",
		"vision_e2e_test.go":             "L3-e2e",
		"smoke_test.go":                  "L3-e2e",
		"forge_integration_test.go":      "L3-e2e",
		"cov_gaps_more_test.go":          "L4-supplement",
		"b3_branches_ext_test.go":        "L4-supplement",
		"p2_timeout_task_hint_test.go":   "L4-supplement",
		"agent_test.go":                  "L1-unit",
		"main.go":                        "",
		"README.md":                      "",
	}
	if len(cases) < len(testLayerSpecs) {
		t.Errorf("样本集 %d 条 < 层数 %d —— 样本被削, 本元判据可能恒真", len(cases), len(testLayerSpecs))
	}
	hit := map[string]bool{}
	for name, want := range cases {
		got := layerClassifyTestFile(name)
		if got != want {
			t.Errorf("分类 %q = %q, 期望 %q", name, got, want)
			continue
		}
		if want != "" {
			hit[want] = true
		}
	}
	for _, spec := range testLayerSpecs {
		if spec.Kind == "go-file" && !hit[spec.Name] {
			t.Errorf("分类函数对 %s 无正例样本 —— 该层的分类分支可能已失效而判据全绿", spec.Name)
		}
	}
}

// J5-b 元判据: 水位不可 <= 0 (否则 J2 恒真, 层可被整层拆除而全绿)。
func TestLayerGuard_FloorPositive(t *testing.T) {
	if layerSpecFloor <= 0 {
		t.Errorf("layerSpecFloor=%d <= 0 —— 层齐备判据恒真", layerSpecFloor)
	}
	for _, spec := range testLayerSpecs {
		if spec.MinCount <= 0 {
			t.Errorf("%s 的 MinCount=%d <= 0 —— 水位判据恒真, 该层可被整层拆除而全绿", spec.Name, spec.MinCount)
		}
	}
}

// J5-c 元判据: 活证据判定函数必须能报出畸形输入 (判据自身可被自证)。
//
// 动机 (20261016 变异实测 V6/V7): 把 layerGoProblems 的两个检查分支分别架空
// (改成 `_ = b` / `if false`), 八条判据全绿 —— 「判据失效但全绿」的经典形态,
// 与「F1 折叠指纹恒真数月」同型。修法 = 判定抽成纯函数
// (layerContentProblem / layerHelperPileProblem / layerMutationTarget.problem /
// layerPyEntryProblem / layerPyScriptsInTaskBody), 再喂畸形数据证明它们还活着。
// 本判据与「判据的判据」是同一件事: 没有它, J4 就只是一句承诺。
func TestLayerGuard_EvidenceJudgesCatchMalformed(t *testing.T) {
	// 文件级活证据: 只有注释的测试文件必须判为空壳
	if layerContentProblem("x_test.go", "package main\n\n// 只有注释, 没有任何函数\n") == "" {
		t.Error("layerContentProblem 对空壳内容返回合格 —— 文件级活证据判据已失效")
	}
	// 样本用 helper 名而非 Test 名: mutation_probe_sentinel 用 `func (Test\w+)\(` 扫哨兵文件
	// 收集「该哨兵的全部用例」, 字符串里出现 Test 名会被误当真实用例 (双向覆盖检查会报
	// 「TestX 未列入 run」)。layerContentProblem 只要求「任意顶层函数」, 语义不受影响。
	if got := layerContentProblem("ok_test.go", "package main\n\nfunc helperX() {}\n"); got != "" {
		t.Error("layerContentProblem 对正常内容误报: " + got)
	}
	// 层级活证据: 零个测试函数的层必须判为 helper 堆
	if layerHelperPileProblem("Lx", 5, 0) == "" {
		t.Error("layerHelperPileProblem 对 withTest=0 返回合格 —— 层级活证据判据已失效")
	}
	if got := layerHelperPileProblem("Lx", 5, 1); got != "" {
		t.Error("layerHelperPileProblem 对有测试函数的层误报: " + got)
	}
	if got := layerHelperPileProblem("Lx", 0, 0); got != "" {
		t.Error("layerHelperPileProblem 对空层误报 (空层归水位判据 J2 管, 此处不重复): " + got)
	}
	// 变异条目活证据: 空结构体必须判为字段缺失
	if (layerMutationTarget{}).problem(0) == "" {
		t.Error("layerMutationTarget.problem 对空条目返回合格 —— 变异层活证据判据已失效")
	}
	if got := (layerMutationTarget{ID: "a", Why: "b", File: "c", Sentinel: "d", Old: "e", New: "f"}).problem(0); got != "" {
		t.Error("layerMutationTarget.problem 对齐全条目误报: " + got)
	}
	// Python 自检活证据: 无入口脚本必须报红
	if layerPyEntryProblem("x_check.py", "import sys\n") == "" {
		t.Error("layerPyEntryProblem 对无入口脚本返回合格 —— Python 层活证据判据已失效")
	}
	// 巡检脚本名提取: 捕获组必须剥掉引号 (FindAllString 的坑, 曾造成 27 条假红)
	got := layerPyScriptsInTaskBody("PY, os.path.join(BASE, 'forge_guard.py'), 'check'")
	if len(got) != 1 || got[0] != "forge_guard.py" {
		t.Errorf("layerPyScriptsInTaskBody 提取结果 %q, 期望 [forge_guard.py] —— 引号未剥离则路径恒不存在", got)
	}
}

// J6 自指: 本文件自身必须落在 L2-sentinel 成员清单里 (哨兵不搞例外)。
func TestLayerGuard_SelfIsMember(t *testing.T) {
	goFiles, err := layerScanGoFiles(layerScanRoots)
	if err != nil {
		t.Fatal(err)
	}
	const self = "layer_guard_sentinel_test.go"
	for _, f := range goFiles["L2-sentinel"] {
		if filepath.Base(f) == self {
			return
		}
	}
	t.Errorf("本文件 %s 不在 L2-sentinel 成员清单里 —— 判据自身游离于分层之外", self)
}

// J7 无盲区: 全仓含 *_test.go 的目录必须都在 layerScanRoots 里。
func TestLayerGuard_ScanRootsCoverAllTestDirs(t *testing.T) {
	declared := map[string]bool{}
	for _, r := range layerScanRoots {
		declared[filepath.ToSlash(r)] = true
	}
	dirs := layerTestDirsUnderRoot()
	if len(dirs) == 0 {
		t.Fatal("全仓找不到任何含 *_test.go 的目录 —— 遍历逻辑失效 (判据恒真的典型形态)")
	}
	for _, d := range dirs {
		if !declared[d] {
			t.Errorf("含 *_test.go 的目录 %q 不在 layerScanRoots 里 —— 隐式盲区; 请补入扫描根", d)
		}
	}
}
