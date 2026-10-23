package main

// layer_guard_scan_test.go — 分层齐备哨兵的扫描臂 (20261016)
//
// 本文件是 layer_guard_sentinel_test.go 的工具侧: 层清单 (单一数据源) + 枚举 + 活证据检查。
// 与 wiring 那套同构 (wiring_sentinel_test.go 判据 + wiring_scan_test.go 扫描 + wiring_refs_test.go 模型),
// 拆开的理由同样是尺度约束: 单文件超 F2 上限 (500 行) 即报红。
//
// 层清单的定位: 铸剑炉的"执行载体分层"此前是隐式的 —— 每一角各自有判据
// (fractal_guard 钉结构尺度 / hourly_wiring 钉调度接线 / mutation_probe_sentinel 钉变异清单),
// 却没有任何一条断言"七层齐备"。缺口与实测数据见 layer_guard_sentinel_test.go 头注释。
// 本文件把层清单写成源码常量, 即把隐式分层变成单一数据源。
//
// 三层口径:
//
//	L1~L4  Go 测试文件的互斥完备划分 (参与守恒断言: 每个 *_test.go 唯一归属一层)
//	L5     变异鉴别力清单 (defense_system/mutation_manifest.json)
//	L6     巡检任务 (defense_system/hourly.py 的 TASKS)
//	L7     Python 自检脚本 (defense_system/*_check.py)

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// layerSpecFloor 是层声明数的水位 —— 删掉一层必须同时改本常量 (显式决策, 不是静默收缩)。
const layerSpecFloor = 7

// testLayerSpec 描述一层测试判据载体。
//
// MinCount 是成员数水位: 只升不降。确需下调 (如重组合并) 时改这一行 ——
// 那是一次显式决策, 而不是让某层静默变薄。
type testLayerSpec struct {
	Name     string // 层名 (稳定标识, 判据按它查找)
	Kind     string // go-file | mutation | hourly | py-check
	MinCount int    // 成员数水位
	Why      string // 这层防什么
}

var testLayerSpecs = []testLayerSpec{
	{"L1-unit", "go-file", 255, "常规单元测试: 纯函数/纯逻辑"},
	{"L2-sentinel", "go-file", 32, "哨兵: 钉架构约束 (layer_guard_sentinel_test.go 即其一)"},
	{"L3-e2e", "go-file", 11, "端到端: 真 gate / 真子进程"},
	{"L4-supplement", "go-file", 28, "补充覆盖: cov_/p2_/b3_ 批次"},
	{"L5-mutation", "mutation", 58, "变异鉴别力: 改坏生产代码 -> 判据必须报红"},
	{"L6-hourly", "hourly", 27, "巡检任务: 每小时调度臂"},
	{"L7-pycheck", "py-check", 13, "Python 自检脚本: 判据的 .py 包装"},
}

// layerScanRoots 是分层判据的显式扫描根清单。
// 与 fractalScanRoots 同构 (盲区必须显式声明, 不能靠"没想到"隐式产生),
// 由 TestLayerGuard_ScanRootsCoverAllTestDirs 断言穷尽性。
var layerScanRoots = []string{".", "web/gateway", "defense_system/logic_guard"}

// layerSkipDirs 是遍历时跳过的目录: 它们不是仓库本体
// (.git 版本库 / node_modules 依赖树 / _archive 归档 / .forge 运行时 / snapshots 快照库)。
var layerSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "_archive": true,
	".forge": true, ".forge-temp": true, "snapshots": true,
}

// layerClassifyTestFile 按命名把测试文件归入唯一一层 (L1~L4)。
// 非测试文件返回空串。判定顺序即优先级: sentinel > e2e > supplement > unit。
// 注意 cov_verify_smoke_test.go 同时含 cov_ 与 smoke, 按优先级归 L3-e2e (端到端优先)。
func layerClassifyTestFile(name string) string {
	l := strings.ToLower(name)
	if !strings.HasSuffix(l, "_test.go") {
		return ""
	}
	switch {
	case strings.Contains(l, "sentinel"):
		return "L2-sentinel"
	case strings.Contains(l, "e2e"), strings.Contains(l, "smoke"), strings.Contains(l, "integration"):
		return "L3-e2e"
	case strings.HasPrefix(l, "cov_"), strings.HasPrefix(l, "p2_"), strings.HasPrefix(l, "b3_"):
		return "L4-supplement"
	default:
		return "L1-unit"
	}
}

// layerScanGoFiles 遍历扫描根, 返回 层名 -> 文件路径列表 (相对仓库根, 已排序)。
// 只读一层目录: 测试文件都在包的根层, 不进子目录。
func layerScanGoFiles(roots []string) (map[string][]string, error) {
	out := map[string][]string{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("列扫描根 %s 失败: %w", root, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			layer := layerClassifyTestFile(e.Name())
			if layer == "" {
				continue
			}
			p := e.Name()
			if root != "." {
				p = filepath.ToSlash(filepath.Join(root, e.Name()))
			}
			out[layer] = append(out[layer], p)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out, nil
}

// layerAnyFuncRe 认任意顶层函数定义 (含 helper) —— 文件级"非空壳"判据。
// layerTestFuncRe 认顶层测试/基准函数 —— 层级"有活判据"判据。
// 两者都用 (?m)^func 锚定行首: 注释行以 // 开头, 不会命中
// (避免把"注释里提到 func Test"当成活证据 —— 那正是"文本不是类型"的典型误判)。
var (
	layerAnyFuncRe  = regexp.MustCompile(`(?m)^func `)
	layerTestFuncRe = regexp.MustCompile(`(?m)^func (Test|Benchmark)\w*\(`)
)

// layerContentProblem 判定单个 Go 测试文件内容是否为空壳 —— 纯函数, 返回空串 = 合格。
//
// 为什么抽成纯函数 (20261016 变异实测 V6/V7): 把下方 layerGoProblems 的两个检查分支
// 分别架空 (改成 `_ = b` / `if false`), 八条判据全绿 —— 「判据失效但全绿」的经典形态,
// 与「F1 折叠指纹恒真数月」同型。只有把判定抽出来喂畸形数据, 才能证明判据还活着。
func layerContentProblem(path, content string) string {
	if !layerAnyFuncRe.MatchString(content) {
		return path + ": 无任何顶层函数定义 —— 空壳文件 (只有文件名, 没有判据)"
	}
	return ""
}

// layerHelperPileProblem 判定一层是否退化成 helper 堆 —— 纯函数, 同样便于自证。
// total = 层内文件数, withTest = 层内含 Test/Benchmark 的文件数。
// total==0 时不报 (空层归水位判据 J2 管, 此处不重复 —— 两处口径必然漂移)。
func layerHelperPileProblem(layer string, total, withTest int) string {
	if total > 0 && withTest == 0 {
		return fmt.Sprintf("%s: %d 个文件里零个 func Test/Benchmark —— 整层退化成 helper 堆",
			layer, total)
	}
	return ""
}

// layerGoProblems 检查一个 Go 测试层的活证据, 分两级 (判定逻辑全在纯函数里):
//
//	文件级: 每个文件必须含至少一个顶层函数 (真空壳文件报红)
//	层  级: 该层至少有一个 func Test/Benchmark (整层退化成 helper 堆报红)
//
// 为什么不要求"每个文件都有 Test" (20261016 实测误报): Go 允许测试文件只放 helper。
// wiring_refs_test.go / wiring_scan_test.go 是 wiring_sentinel_test.go 的拆分产物
// (原文件曾超 F1/F2 上限), 二者只有类型/方法/扫描函数, 测试函数全在原文件里。
// 按文件要求"必有 Test"会误伤这类合法拆分 —— 判据的可用性被自己的粗粒度毁掉,
// 逼人去改结构而不是改行为。层是否空壳由层级判据负责。
func layerGoProblems(layer string, files []string) []string {
	var bad []string
	withTest := 0
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			bad = append(bad, p+": 读不到: "+err.Error())
			continue
		}
		if prob := layerContentProblem(p, string(b)); prob != "" {
			bad = append(bad, prob)
		}
		if layerTestFuncRe.Match(b) {
			withTest++
		}
	}
	if prob := layerHelperPileProblem(layer, len(files), withTest); prob != "" {
		bad = append(bad, prob)
	}
	return bad
}

// layerMutationTarget 是 mutation_manifest.json 的一条条目 (字段与探针消费的一致)。
type layerMutationTarget struct {
	ID       string `json:"id"`
	Why      string `json:"why"`
	File     string `json:"file"`
	Sentinel string `json:"sentinel"`
	Old      string `json:"old"`
	New      string `json:"new"`
}

// problem 判定本条是否字段齐全 —— 纯函数, 便于喂空结构体自证 (见 J5-c)。
func (tg layerMutationTarget) problem(i int) string {
	if tg.ID == "" || tg.Why == "" || tg.File == "" || tg.Sentinel == "" || tg.Old == "" || tg.New == "" {
		return fmt.Sprintf("mutation targets[%d] 字段缺失 (id=%q): 探针会 ERR 或静默少跑", i, tg.ID)
	}
	return ""
}

// layerMutationProblems 检查变异层的活证据: 条目字段齐全 + id 唯一。
// 水位与覆盖面归 mutation_probe_sentinel_test.go 管, 此处不重复 (两处口径必然漂移)。
func layerMutationProblems() (int, []string) {
	b, err := os.ReadFile("defense_system/mutation_manifest.json")
	if err != nil {
		return 0, []string{"读不到 mutation_manifest.json: " + err.Error()}
	}
	var m struct {
		Targets []layerMutationTarget `json:"targets"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return 0, []string{"mutation_manifest.json 解析失败: " + err.Error()}
	}
	var bad []string
	seen := map[string]bool{}
	for i, tg := range m.Targets {
		if prob := tg.problem(i); prob != "" {
			bad = append(bad, prob)
		}
		if seen[tg.ID] {
			bad = append(bad, "mutation id 重复: "+tg.ID)
		}
		seen[tg.ID] = true
	}
	return len(m.Targets), bad
}

// layerHourlyTaskRe 抓 hourly.py 里 TASKS 的条目 ('name', [PY, ..., 'x.py', ...]);
// layerPyNameRe 抓条目体里的 .py 文件名。
var (
	layerHourlyTaskRe = regexp.MustCompile(`\(\s*'(\w+)'\s*,\s*\[([^\]]*)\]`)
	layerPyNameRe     = regexp.MustCompile(`'([\w.\-]+\.py)'`)
)

// layerPyScriptsInTaskBody 从 TASKS 条目体里提取 .py 文件名 —— 纯函数, 便于自证。
//
// 必须用 FindAllStringSubmatch 取捕获组: FindAllString 返回的是完整匹配
// (含两侧引号), 拼出的路径 defense_system/'x.py' 恒不存在 —— 实测第一次跑
// 就报了 27 条假红, 判据的 bug 伪装成产品缺陷。自证样本见 J5-c。
func layerPyScriptsInTaskBody(body string) []string {
	var out []string
	for _, sm := range layerPyNameRe.FindAllStringSubmatch(body, -1) {
		out = append(out, sm[1])
	}
	return out
}

// layerHourlyProblems 检查巡检层的活证据: 任务名唯一 + 引用的脚本真实存在。
func layerHourlyProblems() (int, []string) {
	b, err := os.ReadFile("defense_system/hourly.py")
	if err != nil {
		return 0, []string{"读不到 hourly.py: " + err.Error()}
	}
	src := string(b)
	i := strings.Index(src, "TASKS = [")
	if i < 0 {
		return 0, []string{"hourly.py 里找不到 TASKS 列表 —— 巡检臂被摘除"}
	}
	seg := src[i:]
	if j := strings.Index(seg, "\n]"); j >= 0 {
		seg = seg[:j]
	}
	var bad []string
	seen := map[string]bool{}
	n := 0
	for _, m := range layerHourlyTaskRe.FindAllStringSubmatch(seg, -1) {
		name := m[1]
		if seen[name] {
			bad = append(bad, "巡检任务名重复: "+name)
		}
		seen[name] = true
		n++
		scripts := layerPyScriptsInTaskBody(m[2])
		if len(scripts) == 0 {
			bad = append(bad, "巡检任务 "+name+" 未引用任何 .py 脚本")
			continue
		}
		for _, s := range scripts {
			if _, err := os.Stat(filepath.Join("defense_system", s)); err != nil {
				bad = append(bad, "巡检任务 "+name+" 的脚本不存在: defense_system/"+s)
			}
		}
	}
	return n, bad
}

// layerPyEntryProblem 判定一个 Python 自检脚本是否有可执行入口 —— 纯函数, 便于自证。
func layerPyEntryProblem(path, content string) string {
	if !strings.Contains(content, "if __name__") && !strings.Contains(content, "def main(") {
		return path + ": 既无 __main__ 入口也无 def main( —— 空壳脚本"
	}
	return ""
}

// layerPyCheckProblems 检查 Python 自检层: 每个 *_check.py 存在且有可执行入口。
func layerPyCheckProblems() (int, []string) {
	entries, err := os.ReadDir("defense_system")
	if err != nil {
		return 0, []string{"列 defense_system 失败: " + err.Error()}
	}
	var bad []string
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_check.py") {
			continue
		}
		p := filepath.ToSlash(filepath.Join("defense_system", e.Name()))
		n++
		b, err := os.ReadFile(p)
		if err != nil {
			bad = append(bad, p+": 读不到: "+err.Error())
			continue
		}
		if prob := layerPyEntryProblem(p, string(b)); prob != "" {
			bad = append(bad, prob)
		}
	}
	return n, bad
}

// layerTestDirsUnderRoot 独立枚举全仓含 *_test.go 的目录 (不用扫描根清单),
// 供 J3 守恒与 J7 穷尽性使用 —— 扫描根漏目录时这两条判据会报红。
// 深度上限 3: 测试文件都在包的根层, 更深的遍历只增加成本不增加信息。
func layerTestDirsUnderRoot() []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > 3 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		has := false
		for _, e := range entries {
			if e.IsDir() {
				if layerSkipDirs[e.Name()] {
					continue
				}
				child := e.Name()
				if dir != "." {
					child = filepath.ToSlash(filepath.Join(dir, e.Name()))
				}
				walk(child, depth+1)
				continue
			}
			if strings.HasSuffix(e.Name(), "_test.go") {
				has = true
			}
		}
		if has {
			out = append(out, dir)
		}
	}
	walk(".", 0)
	sort.Strings(out)
	return out
}

// layerObservedCounts 汇总七层的实际成员数, 并收集各层的活证据问题 (J4)。
func layerObservedCounts() (map[string]int, []string, error) {
	counts := map[string]int{}
	var bad []string

	goFiles, err := layerScanGoFiles(layerScanRoots)
	if err != nil {
		return nil, nil, err
	}
	for _, spec := range testLayerSpecs {
		switch spec.Kind {
		case "go-file":
			files := goFiles[spec.Name]
			counts[spec.Name] = len(files)
			bad = append(bad, layerGoProblems(spec.Name, files)...)
		case "mutation":
			n, probs := layerMutationProblems()
			counts[spec.Name], bad = n, append(bad, probs...)
		case "hourly":
			n, probs := layerHourlyProblems()
			counts[spec.Name], bad = n, append(bad, probs...)
		case "py-check":
			n, probs := layerPyCheckProblems()
			counts[spec.Name], bad = n, append(bad, probs...)
		default:
			bad = append(bad, "未知层载体 kind: "+spec.Kind)
		}
	}
	return counts, bad, nil
}
