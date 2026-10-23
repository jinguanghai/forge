package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ══════════════════════════════════════════════════════════════════
// 元判据 (T32 P0-b, 20261008) —— 判据的判据
//
// 动机: F1 曾长期恒真(阈值 6 >= 度量值域上限 6, 数学上不可能报红)而无人发现;
// F2/F3 的裕度分别是 0.8% / 0.0% —— 三条判据全贴边, 「全绿」是结构退化到边缘
// 的结果而非健康。既有用例集在结构上不可能发现这件事(没有 F1 正向样本)。
//
// 本文件把「判据是否还活着」变成死程序可判的事实:
//  ① 恒真检测: 阈值 >= 度量值域上限 → 判据永不报红(物理不可能型缺陷)
//  ② 贴边检测: 实测极值与阈值的裕度 <=10% → 必须显式登记(不是豁免, 是承认)
//  ③ 失效登记检测: 登记条目在实测中不存在(僵尸)或裕度已恢复(过期) → 报红
//
// 判定逻辑抽成纯函数 fractalVitalityProblems, 由 TestFractalVitalityRuleSelfCheck
// 用合成样本反向验证(判据自身也要有判据, 公理三)。
// ══════════════════════════════════════════════════════════════════

// fractalVitalityPinRatio 贴边判定比例: 裕度 <= Limit/该值 即视为贴边。
const fractalVitalityPinRatio = 10

// fractalVitalityPins 贴边登记表 —— 键 = "规则|极值出处"。
//
// 语义: 不是豁免(不表示允许违规), 而是「已知判据即将失效, 显式承认」。
// 只能收缩: 条目所指之处不再贴边(裕度 >10%)或已消失时必须删除, 否则元判据报红。
// 每次增删都是一次显式提交 —— 与 fractalDebtCeiling 同一模式(改大需理由)。
//
// 当前三条(20261011 复测):
//
//	F1|netroute.go              29/30  (裕度 3.3%) —— 顶层声明最多的实现文件
//	F2|hygiene_forge_test.go   496/500 (裕度 0.8%)
//	F3|readline_session.go:processBatch  143/150 (裕度 4.7%)
//
// 20261011: 原 "F3|llm_stream.go:doStream" 已删除 —— 故障兜底改造把 doStream 拆成
// doStream(薄包装)+doStreamTo, 顺带把预检/指纹/debug 抽成 preflightStream,
// 裕度回到 30% 以上 (真修, 不是登记成台账)。
// 同批暴露: selfHostedSelf 148 行本是 F3 次极值, 被 doStream 的 150 遮蔽;
// 前者修好后它成为极值。既有事实(非本次引入), 登记为贴边, 待后续拆分。
//
// 20261011 第二轮(拆 selfHostedSelf): 按阶段拆成 selfApplyAction (改源码+备份) /
// selfCompileSelf (编译+失败回滚, 落 forge_self_build.go) / 部署收尾三段,
// selfHostedSelf 148 → 76 行 (裕度 49%), 本条目删除。
// 极值随之换人: config.go:LoadConfig 143 行成为 F3 新极值 (裕度 4.7%),
// 登记为贴边待拆 —— 与上一轮同型: 极值被遮蔽, 修好一个才露出下一个。
//
// 20261018 T61: config.go:LoadConfig 按装载阶段拆成六段 (146 → 19 行, 裕度回到 87%),
// 本条目删除; 极值再次换人 —— readline_session.go:processBatch 143 行成为 F3 新极值
// (裕度 4.7%), 登记为贴边。同型第三次: 修好一个才露出下一个。
//
// 20261018 T61 第 2 条: readline_session.go:processBatch 按逻辑单元拆成九段
// (processBatch 13 行 + 9 个辅助函数, 原 143 行, 裕度回到 91%), 本条目删除; 极值第四次换人 ——
// llm_sanitize.go:mergeToolCalls 135 行成为 F3 新极值 (裕度 10.0%), 登记为贴边。
//
// 20261018 T61 收口 (第 6 条 factor + 三处连锁):
//
//	① nosword_lexer.go:factor 36 → 按「标识符/括号/数字」拆成 identFactor/callFactor/
//	   parenFactor/roundCall + 两个域校验单元 (本体 CC 3), F5 债务清零 —— 基线删该条。
//	② 极值连锁换人三次, 每次都是「修好一个才露出下一个」的同型:
//	   nswCtxReject 30 (拆成三族闸门, 本体 CC 3) → forge_lang_test 的 AST 哨兵 29
//	   (拆出 flangTableKeys/flangCollectReturns/flangCheckLangLit) →
//	   llm_sanitize.go:mergeToolCalls 28/F3 135 行 (拆成 byID/byIndex 两路 + 累加器类型)。
//	③ 本轮落点: F3 极值降为 cache_stats.go:cacheStatsSummary 134 行 (裕度 10.7%, 不贴边),
//	   故 "F3|llm_sanitize.go:mergeToolCalls" 删除; F5 极值降为
//	   agent_stream_loop.go:processStream 27 (裕度 3/30 = 10.0%, 贴边) → 登记为 F5 pin。
//	   极值已从 36 降到 27 —— 继续追拆属无限套娃, 到此收口。
var fractalVitalityPins = []string{
	"F1|netroute.go",
	"F2|hygiene_forge_test.go",
	"F5|agent_stream_loop.go:processStream",
}

// fractalMeasure 一条结构判据的度量描述。
type fractalMeasure struct {
	Rule      string // F1/F2/F3
	Name      string // 人类可读名
	Limit     int    // 阈值
	DomainMax int    // 度量值域上限; -1 = 无上限(可无限增长)
	DomainWhy string // 上限依据(留痕)
}

// fractalSample 一个度量样本(一处出处的实测值)。
type fractalSample struct {
	Item string // 文件, 或 文件:函数
	Val  int
}

// fractalMeasureStat 一条度量的全部实测样本。
type fractalMeasureStat struct {
	M       fractalMeasure
	Samples []fractalSample
}

// max 返回实测极值与出处。
func (st fractalMeasureStat) max() (int, string) {
	best, at := -1, ""
	for _, s := range st.Samples {
		if s.Val > best {
			best, at = s.Val, s.Item
		}
	}
	return best, at
}

// lookup 按出处查实测值。
func (st fractalMeasureStat) lookup(item string) (int, bool) {
	for _, s := range st.Samples {
		if s.Item == item {
			return s.Val, true
		}
	}
	return 0, false
}

// pinKey 拼登记键。
func pinKey(rule, item string) string { return rule + "|" + item }

// fractalMeasures 度量表 —— 与 fractalCheckFile 的判定一一对应。
// 加判据时必须在此登记, 否则它不进生命体征报告(结构性盲区)。
func fractalMeasures() []fractalMeasure {
	return []fractalMeasure{
		{"F1", "顶层声明数", fractalMaxDecls, -1, "声明数随文件增长无上限"},
		{"F2", "文件行数", fractalMaxFileLines, -1, "行数随文件增长无上限"},
		{"F3", "函数行数", fractalMaxFuncLines, -1, "行数随函数增长无上限"},
		{"F5", "函数圈复杂度", fractalMaxCyclomatic, -1, "分支数随函数增长无上限"},
	}
}

// fractalMeasureAll 扫全部扫描根, 收集每条度量的全部样本。
// 度量口径与 fractalCheckFile 同源(声明计数/文件行数/函数行数), 不重实现判定。
func fractalMeasureAll(t *testing.T) []fractalMeasureStat {
	t.Helper()
	samples := map[string][]fractalSample{}
	add := func(rule, item string, v int) {
		samples[rule] = append(samples[rule], fractalSample{item, v})
	}
	for _, root := range fractalScanRoots {
		ents, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("度量扫描根 %s 读取失败: %v", root, err)
		}
		for _, e := range ents {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
				continue
			}
			fset := token.NewFileSet()
			path := filepath.Join(root, e.Name())
			f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("度量解析 %s 失败: %v", path, err)
			}
			item := fractalItemName(root, e.Name())
			add("F1", item, fractalDeclCount(f))
			add("F2", item, fset.Position(f.End()).Line)
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				n := fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
				add("F3", item+":"+fd.Name.Name, n)
				add("F5", item+":"+fd.Name.Name, fractalFuncCyclomatic(fd))
			}
		}
	}
	var out []fractalMeasureStat
	for _, m := range fractalMeasures() {
		out = append(out, fractalMeasureStat{M: m, Samples: samples[m.Rule]})
	}
	return out
}

// fractalVitalityProblems 是元判据的判定核心(纯函数, 便于反向自检)。
func fractalVitalityProblems(st fractalMeasureStat, pins []string) []string {
	var out []string
	m := st.M
	if m.DomainMax >= 0 && m.Limit >= m.DomainMax {
		out = append(out, fmt.Sprintf(
			"%s %s: 阈值 %d >= 度量值域上限 %d —— 判据恒真, 永不报红 (依据: %s)",
			m.Rule, m.Name, m.Limit, m.DomainMax, m.DomainWhy))
	}
	maxVal, maxAt := st.max()
	if margin := m.Limit - maxVal; margin >= 0 && margin <= m.Limit/fractalVitalityPinRatio {
		if !hasPin(pins, pinKey(m.Rule, maxAt)) {
			out = append(out, fmt.Sprintf(
				"%s %s: 裕度 %d/%d (%.1f%%) <=10%% —— 贴边, 必须登记在 fractalVitalityPins: %q",
				m.Rule, m.Name, margin, m.Limit,
				float64(margin)*100/float64(m.Limit), pinKey(m.Rule, maxAt)))
		}
	}
	for _, k := range pins {
		rule, item, ok := strings.Cut(k, "|")
		if !ok || rule != m.Rule {
			continue
		}
		v, found := st.lookup(item)
		if !found {
			out = append(out, fmt.Sprintf(
				"%s: 登记条目 %q 在实测中不存在 —— 僵尸条目(拼写错误或出处已删)", m.Rule, k))
			continue
		}
		if m.Limit-v > m.Limit/fractalVitalityPinRatio {
			out = append(out, fmt.Sprintf(
				"%s: 登记条目 %q 的裕度已恢复到 %d/%d —— 请从 fractalVitalityPins 删除",
				m.Rule, k, m.Limit-v, m.Limit))
		}
	}
	return out
}

func hasPin(pins []string, key string) bool {
	for _, p := range pins {
		if p == key {
			return true
		}
	}
	return false
}

// pinMark 在生命体征报告里标出该条的状态。
func pinMark(st fractalMeasureStat, pins []string) string {
	maxVal, maxAt := st.max()
	margin := st.M.Limit - maxVal
	registered := hasPin(pins, pinKey(st.M.Rule, maxAt))
	switch {
	case margin < 0:
		return " [判据在报红]"
	case margin <= st.M.Limit/fractalVitalityPinRatio && registered:
		return " [贴边·已登记]"
	case margin <= st.M.Limit/fractalVitalityPinRatio:
		return " [贴边·未登记]"
	case registered:
		return " [登记过期]"
	}
	return ""
}

// TestFractalThresholdVitality 元判据: 每条判据都要有区分度, 贴边必须显式登记。
func TestFractalThresholdVitality(t *testing.T) {
	stats := fractalMeasureAll(t)
	if len(stats) != len(fractalMeasures()) {
		t.Errorf("度量实测 %d 条 != 度量表 %d 条 —— 有度量没扫到(扫描根或度量定义失配)",
			len(stats), len(fractalMeasures()))
	}
	var problems []string
	for _, st := range stats {
		maxVal, maxAt := st.max()
		margin := st.M.Limit - maxVal
		t.Logf("生命体征 %s: %s 极值 %d (%s) / 阈值 %d → 裕度 %d (%.1f%%)%s",
			st.M.Rule, st.M.Name, maxVal, maxAt, st.M.Limit, margin,
			float64(margin)*100/float64(st.M.Limit), pinMark(st, fractalVitalityPins))
		problems = append(problems, fractalVitalityProblems(st, fractalVitalityPins)...)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestFractalVitalityRuleSelfCheck 元判据自检: 用合成样本反向验证判定逻辑。
// 没有这层, 元判据自己恒真时也无人发现(同型缺陷)。
func TestFractalVitalityRuleSelfCheck(t *testing.T) {
	cases := []struct {
		name   string
		limit  int
		domain int
		vals   []int // 样本值
		pins   []string
		want   string // 期望命中的问题关键词; "" = 必须无问题
	}{
		{"恒真: 阈值==值域上限", 6, 6, []int{6}, nil, "恒真"},
		{"恒真: 阈值>值域上限", 9, 6, []int{3}, nil, "恒真"},
		{"贴边: 裕度 0 未登记", 500, -1, []int{500}, nil, "贴边"},
		{"贴边: 裕度恰 10% 未登记", 500, -1, []int{450}, nil, "贴边"},
		{"贴边已登记", 500, -1, []int{450}, []string{"X|synth0.go"}, ""},
		{"过期登记: 裕度已恢复", 500, -1, []int{100}, []string{"X|synth0.go"}, "已恢复"},
		{"僵尸登记: 出处不存在", 500, -1, []int{100}, []string{"X|gone.go"}, "僵尸"},
		{"健康: 有裕度且未登记", 500, -1, []int{100}, nil, ""},
		{"报红: 极值超阈值(存量违规归基线管)", 500, -1, []int{600}, nil, ""},
	}
	for _, c := range cases {
		st := fractalMeasureStat{M: fractalMeasure{Rule: "X", Name: "合成度量",
			Limit: c.limit, DomainMax: c.domain, DomainWhy: "合成"}}
		for i, v := range c.vals {
			st.Samples = append(st.Samples, fractalSample{Item: fmt.Sprintf("synth%d.go", i), Val: v})
		}
		got := strings.Join(fractalVitalityProblems(st, c.pins), " ; ")
		if c.want == "" {
			if got != "" {
				t.Errorf("%s: 期望无问题, 实得 %s", c.name, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: 期望命中 %q, 实得 %q", c.name, c.want, got)
		}
	}
}

// fractalCoverageSkip 覆盖断言的显式跳过清单(非源码容器)。
// 点/下划线前缀由代码统一跳过(工具/归档约定), 这里是额外的显式名单。
var fractalCoverageSkip = map[string]bool{
	"node_modules": true,
	"knowledge":    true, // 私有知识库(数据, 非本仓源码)
	"snapshots":    true, // defense_system 下的历史快照
}

// TestFractalScanCoverage 覆盖断言 (T32 P0-d): 仓库中每个含 .go 的目录
// 必须在 fractalScanRoots 里, 否则新增子目录包会静默成为盲区。
func TestFractalScanCoverage(t *testing.T) {
	roots := map[string]bool{}
	for _, r := range fractalScanRoots {
		roots[filepath.ToSlash(r)] = true
	}
	var found, skipped []string
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != "." {
			base := d.Name()
			if strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") || fractalCoverageSkip[base] {
				skipped = append(skipped, filepath.ToSlash(p))
				return fs.SkipDir
			}
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			return nil
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				found = append(found, filepath.ToSlash(p))
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历仓库失败: %v", err)
	}
	problems := fractalCoverageProblems(found, roots)
	for _, dir := range problems {
		t.Errorf("目录 %s 含 .go 但不在 fractalScanRoots —— 分形判据的盲区"+
			"(登记为扫描根, 或加入 fractalCoverageSkip 显式跳过)", dir)
	}
	// 自检: 判定本身必须能抓到未登记目录 —— 否则本判据恒绿(变异自检载体)。
	if got := fractalCoverageProblems([]string{"pkg/unregistered"}, roots); len(got) != 1 {
		t.Errorf("覆盖断言自检失败: 未登记目录未被判出, 实得 %v", got)
	}
	t.Logf("含 .go 的目录 %d 个 (%v), 未登记 %d 个; 跳过目录 %d 个",
		len(found), found, len(problems), len(skipped))
}

// fractalCoverageProblems 覆盖断言的判定核心(纯函数, 便于变异自检)。
func fractalCoverageProblems(found []string, roots map[string]bool) []string {
	var out []string
	for _, dir := range found {
		if !roots[dir] {
			out = append(out, dir)
		}
	}
	return out
}
