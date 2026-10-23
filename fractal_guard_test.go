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
// 来源 (20261004 补留痕): 主人原话「整体有序, 局部无序」→ 实际翻译 = 整体同构 + 局部有界。
//   落地提交 df7f4a3 的提交信息只有标题, 代码头注释只写"结构美感" —— 约束的出处断链,
//   后人无法判断某条规则是主人要求还是实现者发挥。此三行即补该断链。
//
// 分形 = 自相似 + 尺度不变。本守卫把"结构美感"翻译成死程序能判的四条规则:
//
//	F1 自相似   顶层声明数有上限 (声明极多 = 上帝文件)
//	            (20261008 换度量: 原「折叠指纹长度 <= 6」在数学上恒真, 见 fractalMaxDecls)
//	F2 尺度不变 文件规模有上限, 无超大尺度
//	F3 单元有界 单文件 ≤500 行, 单函数 ≤150 行
//	F4 测试镜像 实现文件应有同名测试 (自相似在测试层的体现)
//	F5 分支密度 单函数圈复杂度 <=30 (gocyclo 口径, 见 fractal_cyclo_test.go)
//
// 判定权完全在死程序; 存量违规以"豁免清单"外置 (.forge/fractal_baseline.json)。
// 清单只能收缩不能扩张 —— 新增违规立即失败, 改造后清单可手工删条目。
// ══════════════════════════════════════════════════════════════════

const (
	fractalMaxFileLines = 500
	fractalMaxFuncLines = 150
	// fractalMaxDecls 是 F1 的度量上限: 顶层声明数(不折叠)。
	//
	// 为什么换度量 (20261008, T32 P0-a): 旧度量 fractalComplexShapeLen=6 判的是
	// 「折叠指纹长度」, 而指纹字母表只有 {T,V,C,F,M,?} 六种且相邻去重 ——
	// 长度在数学上不可能超过 6。阈值 6 等于「永不报红」, 判据已死
	// (实测根目录 max=6 恰好贴阈值)。判据的度量必须能无限增长, 阈值才可能被突破。
	//
	// 阈值 30 的来由(结构意图, 不是贴极值): F2 限 500 行, Go 代码平均 ~15 行/顶层声明,
	// 30 声明 ≈ 450 行 —— 两个尺度上的约束同量级, 互为印证(自相似)。
	fractalMaxDecls     = 30
	fractalBaselinePath = ".forge/fractal_baseline.json"
	// fractalDebtCeiling 是豁免清单条数的历史最高水位 —— 只能改小, 不能改大。
	//
	// 缺口(20260927): 原守卫只拦「实际违规不在基线里」(added), 却不拦「基线被加条目」——
	// 往清单里塞一条, 新增违规就静默洗白, 守卫从约束退化成台账。
	// 改大这一行 = 一次显式决策(必须同时改测试), 而不是静默膨胀。
	// 20260927 定案 37 条 → 同日清 F3(config.LoadConfig / forge.selfHostedSelf /
	// main_interactive.runInteractive)后收紧至 33 条 → 补 main_interactive_test.go
	// 消掉一条 F4 后收紧至 32 条: 水位必须跟随真实债务下降,
	// 否则水位会变成"允许反弹的空间"。
	// 同日拆解 agent.go (1028 行 → 157 行, shape 7 → 3) 后收紧至 30 条 ——
	// 拆出的 agent_memory/history/trim/audit/stream_loop 五个文件此后永久受
	// F1/F2/F4 约束 (再涨回 >500 行或 shape >6 立即失败), 这才是收缩的实质收益。
	// 同日拆解 llm.go (1124 行 → 266 行, shape 20 → 5) 后收紧至 27 条 ——
	// 拆出 llm_types/error/stream/sanitize 四个文件, 并补齐 5 个同名测试文件 (F4)。
	// 20260927 F4 收尾: 15 条 F4(cache_stats/ctrlclose/gatesync/guard/main/
	// main_commands/main_startup/main_utils/session_stats/stats/style/term/tool_ref/
	// tui_style/ux) 全部补齐同名测试文件后收紧至 2 条 —— 只剩两条结构性不可解项:
	//   F2|forge.go            (self gate 的 srcPath 写死该文件, 拆它等于拆自己的手术刀)
	//   F2|readline_windows.go (需真拆分 + 显式 //go:build windows + VPS 侧 Linux 构建实测)
	// 同日拆解 readline_windows.go (1056 行 → 375 行, shape 23 → 4) 后收紧至 1 条 ——
	// 纯拆分 + 重排不新建顶层声明即不触碰 _windows 隐式 build constraint, 拆出的
	// readline_editor_windows.go / readline_session_windows.go 各配同名 _windows_test.go,
	// 此后永久受 F1/F2/F4 约束。仅剩唯一一条结构性不可解项:
	//   F2|forge.go (self gate 的 srcPath 写死该文件, 拆它等于拆自己的手术刀)
	// 同日拆解 forge.go (3224 行 → 371 行, shape 53 → 5) 后收紧至 0 条 ——
	// 前置改造 locateSelfSource(按内容搜索全包)解除了"self gate 只能改 forge.go"
	// 的硬绑定, 否则拆它就是拆自己的手术刀。拆出 11 个文件各配同名测试(F4)。
	// 至此 F1/F2/F3/F4 四项债务全部清零, 豁免清单为空。
	//
	// 缺口(20260927): 上面这次"清零"只覆盖实现文件 —— fractalScan 对 *_test.go 直接
	// continue, 测试文件从不进入 F1/F2/F3 检查, 于是 4 条测试文件违规长期隐形
	// (F1|wiring_sentinel_test.go shape 8; F2|llm_http_test.go 988 / self_deploy_test.go 629 /
	// cov_b3_branches_test.go 582), 而注释却宣称"四项债务全部清零"。
	// 已去掉豁免并拆解这 4 个文件, 今后测试文件与实现文件同受 F1/F2/F3 约束。
	// 现值 2 的具体身份 (20261004 补留痕, 防"水位从哪来"断链):
	//   F4|approval_delegate.go + F4|console_guard.go —— 二者同包, 由 cookguard_sentinel_test.go
	//   集中覆盖 (拆成两个同名 _test 会出现符号重定义)。属结构性不可解项, 非"来不及修"。
	// 20261008 T32 P0-d 扫描范围扩大: web/gateway 与 defense_system/logic_guard 从
	// 隐式盲区纳入扫描根 —— 暴露 2 条存量债务(F1/F2|web/gateway/main.go, 独立 module
	// 长期无人扫), 水位 2 → 4 是一次显式决策(已报主人)。同批把 logic_guard/main.go 的
	// F3(main 225 行)拆解 + 补 main_test.go 消 F4: 盲区纳入后暴露的债务里, 能当场修的
	// 不入清单 —— 清单只留「需要独立工程才能修」的项, 且只能收缩。
	fractalDebtCeiling = 2 // 20261017 T35 P1 (主人批准「B5: 分形守卫加 F5」): 新增 F5 圈复杂度维,
	//                     上线即暴露 8 条存量债务, 当场拆掉 2 条(prodSymbolRefs 33 /
	//                     TestMemoryGuard_WiredInBuild 32 —— 都是判据自身的辅助函数, 拆分支不动断言),
	//                     余 6 条入基线, 水位 2 → 8 是一次显式决策(新维引入新债务, 不是给旧违规洗白)。
	//                     6 条身份: F5|nosword_cand.go:nswIsCandidateMode 50 / readline_session.go:processBatch 38 /
	//                     nosword_lexer.go:factor 36 / nosword_cand.go:nswSniffCands 34 /
	//                     config.go:LoadConfig 33 / main_interactive.go:runInteractive 33 ——
	//                     全部是「需独立工程 + 交叉用例」的重构, 登记为 TODO T61 待拆;
	//                     拆完必须同批下调本水位(只降不升)。
	//                     20261018 T61 第 2 条: readline_session.go:processBatch 38 → 按逻辑单元拆成
	//                     九段 (processBatch 13 行), 同批删基线 + 水位 7 → 6。
	//                     20261018 T61 第 3 条: main_interactive.go:runInteractive 33 → 拆成
	//                     主循环 + lineAssembler/readInputLine/handleLocalInput/buildPrompt/
	//                     printFoldHint/printSessionSummary (runInteractive 24 行, 圈复杂度 7),
	//                     同批补 13 条单元用例 (lineAssembler.feed 逐分支钉住等价性),
	//                     删基线 + 水位 6 → 5。余 3 条待拆。
	//                     20261018 T61 第 1 条: config.go:LoadConfig 33 → 按装载阶段拆成
	//                     loadDotEnvIfPresent/applyAPIKey/applyBaseURL/applyModelRouting/
	//                     applyRuntimeEnv/clampConfig 六段 (LoadConfig 19 行, 圈复杂度 5),
	//                     同批删基线条目 + 水位 8 → 7。余 5 条待拆。
	//                     20261018 T61 第 2 条: readline_session.go:processBatch 38 → 按「逻辑单元」
	//                     拆成 stepBatch/stepNewline/stepTailNewline/stepEscape/stepByte/
	//                     finishBatch/consumeTrailing/consumeCRLFContinuation (processBatch 13 行),
	//                     同批删基线条目 + 水位 7 → 6。余 4 条待拆。
	//                     20261010 T36 债务收缩: web/gateway/main.go 拆分(744 行/33 声明 → 237 行/6 声明)
	//                     消掉 F1/F2 两条存量债务, 水位随真实债务下调 4→2(只降不升); 余 2 条为结构性不可解项
	//                     (F4 approval_delegate/console_guard: 同包集中覆盖, 拆开即符号重定义)
	//                     20261018 T61 第 1+4 条 (同文件同批): nswIsCandidateMode 50 → 9 与
	//                     nswSniffCands 34 → 8 —— 字符白名单改查表 (switch 每 case 计 1, 拆了等于没拆)、
	//                     闸门按「两路径共用 / 嗅探专属」分组、候选切分抽纯函数 nswBuildCand
	//                     (rune 偏移首次可单测)。等价性判据 = 65 条输入 + 12 段文本行为快照逐字节一致
	//                     (nosword_cand_golden_test.go) + 既有 TestNSW* 全绿。删基线 + 水位 5 → 3。
	//                     余 1 条 (nosword_lexer.go:factor 36, 递归下降文法入口, 需交叉用例)。
	fractalDumpBegin = "<<<FRACTAL_JSON_BEGIN>>>"
	fractalDumpEnd   = "<<<FRACTAL_JSON_END>>>"
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

// fractalDeclCount 统计顶层声明数(不折叠)。
//
// 旧实现是 fractalShape 折叠指纹(相邻同类合并成一个字母)。折叠丢信息:
// func a/b/c 三个声明折叠成 "F"(长度 1) —— 而上帝文件的征兆恰是「声明多」,
// 折叠把「多」抹平了。不折叠的计数单调、可加, 拆文件必然下降。
// import 是样板, 不计入结构复杂度。
func fractalDeclCount(f *ast.File) int {
	n := 0
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			continue
		}
		n++
	}
	return n
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
	if n := fractalDeclCount(f); n > fractalMaxDecls {
		vs = append(vs, fractalViolation{"F1", name, n, fractalMaxDecls})
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
		if c := fractalFuncCyclomatic(fd); c > fractalMaxCyclomatic {
			vs = append(vs, fractalViolation{"F5", name + ":" + fd.Name.Name, c, fractalMaxCyclomatic})
		}
	}
	return vs
}

// fractalItemName 给违规条目的出处加扫描根前缀。
// 根目录不加前缀(保持基线里既有 key 不变); 子目录必须加 —— 否则两个包里的
// main.go 会撞同一个 key("F2|main.go"), 基线条目互相冒充。
func fractalItemName(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return filepath.ToSlash(filepath.Join(dir, name))
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
		// 测试文件同样受 F1/F2/F3 约束 —— 此前这里直接 continue, 使 4 条测试文件违规
		// 长期隐形, 而下方注释宣称"四项债务全部清零"(声称范围 ≠ 实际范围)。
		if strings.HasSuffix(name, "_test.go") {
			testBase[fractalBase(name)] = true
		} else {
			implBase[fractalBase(name)] = true
		}

		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, 0, fmt.Errorf("解析 %s 失败: %w", name, err)
		}
		nFiles++
		vs = append(vs, fractalCheckFile(fset, fractalItemName(dir, name), f)...)
	}

	for b := range implBase {
		if !testBase[b] {
			vs = append(vs, fractalViolation{"F4", fractalItemName(dir, b+".go"), 0, 1})
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

// fractalScanRoots 是分形判据的显式扫描根清单 (T32 P0-d, 20261008)。
//
// 为什么显式: 原实现只 os.ReadDir(".") 一层(非递归), web/gateway 与
// defense_system/logic_guard 从未被扫过 —— 盲区是隐式的, 无人知道。
// 实测盲区内的超标: web/gateway/main.go 743 行; logic_guard main() 225 行。
// 与「容器内部是盲区」(.forge/forge-tools 19 项躺数月而四套守卫全绿) 同型。
//
// TestFractalScanCoverage 断言: 仓库中每个含 .go 的目录都必须出现在本清单
// (或显式跳过清单)里 —— 新增子目录包不可能再静默成为盲区。
var fractalScanRoots = []string{".", "web/gateway", "defense_system/logic_guard"}

// fractalScanAll 扫全部扫描根。F4(测试镜像) 按根内配对 —— 子目录是独立包。
func fractalScanAll() ([]fractalViolation, int, error) {
	var all []fractalViolation
	total := 0
	for _, root := range fractalScanRoots {
		vs, n, err := fractalScan(root)
		if err != nil {
			return nil, 0, fmt.Errorf("扫描根 %s: %w", root, err)
		}
		all = append(all, vs...)
		total += n
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Rule != all[j].Rule {
			return all[i].Rule < all[j].Rule
		}
		return all[i].Item < all[j].Item
	})
	return all, total, nil
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
	vs, nFiles, err := fractalScanAll()
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
	// 债务水位: 基线条数不得突破历史最高水位。只靠 added 检测堵不住
	// 「往基线里塞条目」这条洗白路径(塞进去后新违规就在 allowed 里了)。
	if len(bl.Violations) > fractalDebtCeiling {
		t.Errorf("❌ 豁免清单膨胀: %d 条 > 水位 %d —— 新增违规不得靠加基线洗白; "+
			"确需提高水位必须同时改 fractalDebtCeiling(显式决策)",
			len(bl.Violations), fractalDebtCeiling)
	}
	// FORGE_FRACTAL_DUMP=1: 输出当前全部违规的结构化清单, 供
	// defense_system/fractal_check.py --shrink 重算基线(单向收缩)。
	// 判定权仍在死程序(此处), 脚本只负责搬运与护栏。
	if os.Getenv("FORGE_FRACTAL_DUMP") == "1" {
		fractalDump(t, vs)
	}
	t.Logf("分形守卫: 扫描根 %v | .go 文件 %d 个(含测试) | 基线允许 %d 条 | 实际违规 %d 条 | 水位 %d",
		fractalScanRoots, nFiles, len(bl.Violations), len(vs), fractalDebtCeiling)
}

// TestFractalDeclCountRule 自检1: 顶层声明计数(不折叠)是否正确。
//
// 旧用例集测的是折叠指纹, 其中 `func a/b/c → "F"` 把「相邻同类合并」的语义
// 钉成了契约 —— 三个声明折叠成一个字母, 信息在度量层就丢了。T32 P0-c 改为
// 直接测计数, 并补 F1 正向用例: 此前 F1 没有任何正向样本, 「F1 恒真」这件事
// 在结构上不可能被现有用例集发现。
func TestFractalDeclCountRule(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"package p\n\nimport \"fmt\"\n\nvar a = 1\n\nfunc f() { fmt.Println(a) }\n", 2}, // import 不计
		{"package p\n\ntype T struct{}\n\nfunc (t T) M() {}\n\nfunc (t T) N() {}\n", 3},
		{"package p\n\nconst C = 1\n\ntype T int\n\nfunc (t T) M() {}\n\nfunc (t T) N() {}\n", 4},
		// 不折叠: 旧 shape 把这三个声明折叠成 "F"(长度 1), 计数保留 3
		{"package p\n\nfunc a() {}\n\nfunc b() {}\n\nfunc c() {}\n", 3},
		{"package p\n\nvar (\n\ta = 1\n\tb = 2\n)\n\nfunc f() {}\n", 2}, // 一个 var 块 = 1 个声明
	}
	for i, c := range cases {
		f, err := parser.ParseFile(token.NewFileSet(), fmt.Sprintf("case%d.go", i), c.src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("用例 %d 解析失败: %v", i, err)
		}
		if got := fractalDeclCount(f); got != c.want {
			t.Errorf("用例 %d: decls=%d, want %d", i, got, c.want)
		}
	}

	// F1 正向用例 + 边界: 阈值+1 必须触发; 恰好等于阈值不得触发。
	for _, tc := range []struct {
		n       int
		wantHit bool
	}{{fractalMaxDecls, false}, {fractalMaxDecls + 1, true}} {
		var sb strings.Builder
		sb.WriteString("package p\n")
		for i := 0; i < tc.n; i++ {
			fmt.Fprintf(&sb, "\nfunc f%d() {}\n", i)
		}
		f, err := parser.ParseFile(token.NewFileSet(), "many.go", sb.String(), parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("合成 %d 声明文件解析失败: %v", tc.n, err)
		}
		hit := false
		for _, v := range fractalCheckFile(token.NewFileSet(), "many.go", f) {
			if v.Rule == "F1" {
				hit = true
			}
		}
		if hit != tc.wantHit {
			t.Errorf("合成 %d 个顶层声明: F1 触发=%v, want %v (阈值 %d)", tc.n, hit, tc.wantHit, fractalMaxDecls)
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

// TestFractalThresholdRule 自检3: 三条阈值判定真的能抓到 —— 走真实判定路径。
//
// 这是变异自检的载体: 删掉/改坏任一条判定分支(或让它永不触发), 本用例报红。
// 20261008 T32 扩展: 原用例只构造 F2/F3 的正向样本(620 行文件), 没有 F1 ——
// 「F1 恒真」这件事在结构上不可能被现有用例集发现。
func TestFractalThresholdRule(t *testing.T) {
	// 合成文件同时触发三条: F1(顶层声明 >30) / F2(文件 >500 行) / F3(函数 >150 行)
	var sb strings.Builder
	sb.WriteString("package p\n")
	for i := 0; i < fractalMaxDecls+1; i++ {
		fmt.Fprintf(&sb, "\nfunc f%d() {}\n", i)
	}
	sb.WriteString("\nfunc big() {\n")
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
	for _, rule := range []string{"F1", "F2", "F3"} {
		if !got[rule] {
			t.Errorf("合成文件未触发 %s —— 该判定分支失效(变异自检)", rule)
		}
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
