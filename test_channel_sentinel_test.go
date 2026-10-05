package main

// test_channel_sentinel_test.go — 测试运行通道哨兵 (20261001)
//
// 判据来自实测, 不是推断。go test 在 stdout 不是终端时对整包输出做缓冲,
// 包没结束就不落盘; 进程被杀(超时/中断) → 输出全丢, 只剩一个退出码,
// 失败原因完全不可见。同一条件下编译成测试二进制直接运行则逐条写出:
//
//	[实测] go test -short + 9s kill          -> 日志 0 字节
//	[实测] go test -c + 直接运行 + 9s kill   -> 日志 21630 字节 / 539 行
//
// 所以测试脚本必须"先 go test -c 编译, 再直接运行测试二进制"。
// 本哨兵把这条方法学钉在脚本上, 防止有人图省事改回裸 go test。

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestScriptsUseCompiledBinary(t *testing.T) {
	scripts := []string{"full_test.cmd", "quick_test.cmd"}
	for _, name := range scripts {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("读不到测试脚本 %s (cwd 应为仓库根): %v", name, err)
			}
			s := execLines(string(b))
			if !strings.Contains(s, "go test -c") {
				t.Error("必须用 go test -c 编译测试二进制: 裸 go test 的输出在被杀时全丢")
			}
			if strings.Contains(s, "go test -count") {
				t.Error("不得直接跑 go test -count ...: 输出被缓冲, 超时/中断即全丢")
			}
			if !strings.Contains(s, "-test.timeout=") {
				t.Error("直接运行测试二进制时必须显式给 -test.timeout= (默认 10m 会误杀长通道)")
			}
			// 档位契约 (20261003): quick 必须带 -test.short, full 必须不带。
			// 缺前者 → quick 静默退化成全量档 (实测: 0 个 short SKIP / 211.7s);
			// 带后者 → 两档无差别, 分层失效。
			if name == "quick_test.cmd" && !strings.Contains(s, "-test.short") {
				t.Error("quick 档必须带 -test.short: 缺了就退化成全量档")
			}
			if name == "full_test.cmd" && strings.Contains(s, "-test.short") {
				t.Error("full 档不得带 -test.short: 否则与 quick 档无差别")
			}
		})
	}
}

// ---- 非 ASCII 守卫 (20261003) ----
//
// 实测根因: 无控制台环境下 (forge task gate / 计划任务), cmd.exe 解析批处理时会因
// chcp 之后的中文注释行而错位, 最后一行静默丢掉 -test.short 与 %*, 于是 quick 档
// 退化成全量档 (实测 179.6s, 与全量 163s 同量级) 而无人察觉。
// 人工双击有控制台, 所以该缺陷只在自动化调用时发作。
// 结论: 自动化通道脚本必须是纯 ASCII 文件。

// automationScripts 是被无控制台环境调用的测试脚本。
// 新增此类脚本必须登记到这里 —— 未登记且名字含 "test" 的脚本会被 TestAutomationScriptsRegistered 报红。
var automationScripts = []string{"full_test.cmd", "quick_test.cmd"}

// asciiViolations 返回含非 ASCII 字符的行号 (1-based)。
func asciiViolations(content string) []int {
	var bad []int
	for i, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		for _, r := range line {
			if r > 127 {
				bad = append(bad, i+1)
				break
			}
		}
	}
	return bad
}

func TestTestScriptsArePureAscii(t *testing.T) {
	for _, name := range automationScripts {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("读不到测试脚本 %s (cwd 应为仓库根): %v", name, err)
			}
			if bad := asciiViolations(string(b)); len(bad) > 0 {
				t.Errorf("含非 ASCII 行 %v: 无控制台启动时 cmd.exe 会错行解析, 静默吞掉 -test.short 与 %%*", bad)
			}
		})
	}
}

// TestAutomationScriptsRegistered 防止新增测试脚本漏登记 (清单式判据的常见漏洞)。
func TestAutomationScriptsRegistered(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("列目录失败: %v", err)
	}
	reg := map[string]bool{}
	for _, n := range automationScripts {
		reg[strings.ToLower(n)] = true
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		if !strings.HasSuffix(low, ".cmd") && !strings.HasSuffix(low, ".bat") {
			continue
		}
		if !strings.Contains(low, "test") || reg[low] {
			continue
		}
		t.Errorf("脚本 %s 名字含 test 但未登记到 automationScripts: 若它会被无控制台调用, 必须登记并保持纯 ASCII", e.Name())
	}
}

// TestAsciiViolationsSelfCheck 是判据自身的判据 (变异自检):
// 若 asciiViolations 恒返回空, 上面两个用例就永远绿 —— 那是假绿, 比没有判据更危险。
func TestAsciiViolationsSelfCheck(t *testing.T) {
	if got := asciiViolations("REM ok\ncd /d %~dp0\n"); len(got) != 0 {
		t.Errorf("纯 ASCII 样本不应报违规, 却得到 %v", got)
	}
	if got := asciiViolations("REM ok\nREM \u4e2d\u6587\u6ce8\u91ca\nREM tail\n"); len(got) != 1 || got[0] != 2 {
		t.Errorf("含中文样本应报第 2 行, 却得到 %v", got)
	}
	if got := asciiViolations("REM a\r\nREM b \u2014 dash\r\nREM c\r\n"); len(got) != 1 || got[0] != 2 {
		t.Errorf("em dash + CRLF 样本应报第 2 行, 却得到 %v", got)
	}
}

// execLines 剥掉注释行, 只留可执行行。
//
// 为什么需要 (20261003 实测): full_test.cmd 的注释里提到 "-test.short" 这个字面,
// 让「full 档不得带 -test.short」这条断言假红 —— 注释里的【提及】被当成了【使用】。
// 同一根因也让旧断言的 go test -c / -test.timeout= 恒真 (注释里就有这些字样),
// 判据等于不存在。凡「脚本是否用了某参数」的判定, 必须先剥注释。
func execLines(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(strings.ToUpper(t), "REM") {
			continue
		}
		b.WriteString(t)
		b.WriteString("\n")
	}
	return b.String()
}

// TestExecLinesSelfCheck 是剥注释判据自身的判据: 若 execLines 恒返回原串,
// 上面那条「full 档不得带 -test.short」就会一直假红, 反之若剥得过头又会恒绿。
func TestExecLinesSelfCheck(t *testing.T) {
	if got := execLines("REM mentions -test.short\n"); strings.Contains(got, "-test.short") {
		t.Errorf("仅注释里提到 -test.short, 剥离后不应命中, 却得到 %q", got)
	}
	if got := execLines("x -test.short\n"); !strings.Contains(got, "-test.short") {
		t.Errorf("可执行行里的 -test.short 必须保留, 却得到 %q", got)
	}
	if got := execLines("@echo off\r\nrem lower-case comment -test.short\r\nchcp 65001 >nul\r\n"); strings.Contains(got, "-test.short") {
		t.Errorf("小写 rem + CRLF 也必须剥离, 却得到 %q", got)
	}
	if got := execLines("REM a\n\nREM b\n"); strings.TrimSpace(got) != "" {
		t.Errorf("全注释文件剥离后应为空, 却得到 %q", got)
	}
}

// ---- 分层覆盖哨兵 (20261003) ----
//
// 档位契约(quick 必须带 -test.short)已由 TestTestScriptsUseCompiledBinary 钉住,
// 但那只管「开关打开了」, 不管「开关后面有没有东西」。
//
// 实测教训: quick 档曾因 cmd 无控制台错行解析静默退化成全量档
// (SKIP 0 个 / 179.6s, 与全量 163s 同量级) 而无人察觉。
// 同构风险: 若有人把 testing.Short() 判定删光, 开关照旧传、脚本照旧合规, 分层一样失效。
// 所以必须有「判定存在性」判据 —— 调用点数 + 覆盖文件数。
//
// 阈值不是指标崇拜, 是「删光后无人察觉」的下限: 低于阈值即报红, 要求重新登记。

// shortScanSkipDirs 不参与统计的目录段名。
// snapshots 是历史源码快照(内含旧版 *_test.go): 扫进去会让计数虚高 ——
// 判据被历史副本喂饱, 活代码里的判定删光也测不出来。
var shortScanSkipDirs = map[string]bool{
	".git": true, "_archive": true, ".forge": true, ".forge-temp": true,
	"node_modules": true, "snapshots": true, "vendor": true,
}

// countShortCalls 统计源码中 testing.Short() 的【调用点】数。
//
// 剥行注释 / 块注释 / 字符串字面量: 注释里提到它、字符串里写着它, 都不算调用。
// 实测教训: 注释里的【提及】被当成【使用】, 判据恒真 (execLines 那条同源)。
func countShortCalls(src string) int {
	const marker = "testing.Short()"
	rs := []rune(src)
	var b strings.Builder
	inLine, inBlock, inStr, inRaw := false, false, false, false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		var next rune
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				b.WriteRune(c)
			}
		case inBlock:
			if c == '*' && next == '/' {
				inBlock = false
				i++
			}
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case inRaw:
			if c == '`' {
				inRaw = false
			}
		default:
			switch {
			case c == '/' && next == '/':
				inLine = true
				i++
			case c == '/' && next == '*':
				inBlock = true
				i++
			case c == '"':
				inStr = true
			case c == '`':
				inRaw = true
			default:
				b.WriteRune(c)
			}
		}
	}
	return strings.Count(b.String(), marker)
}

// shouldSkipDir 判定目录是否不参与统计。
//
// 抽成纯函数是为了可单测: 判据必须能问「snapshots 这个目录到底跳没跳」——
// 只看「跳过的总数」测不出单个目录的隔离失效
// (实测 M8: 删掉 snapshots 跳过, 总量仍大于快照数, 判据假绿)。
func shouldSkipDir(path, name string) bool {
	return path != "." && shortScanSkipDirs[name]
}

// shortCallStats 返回 (调用点数, 覆盖文件数, 扫描的 .go 文件数, 被跳过目录的 .go 文件数)。
//
// 第 4 个值不是装饰: 它是「范围污染」的判据。skip 表一旦被改窄(或删掉 snapshots),
// 历史快照会被计入, 计数虚高 —— 而主判据照样绿(实测变异 M8: 去掉 snapshots 跳过,
// 覆盖判据仍 PASS)。计数虚高会让「活代码里判定被删光」这件事测不出来。
func shortCallStats(t *testing.T) (calls, files, scanned, skippedGo int) {
	t.Helper()
	werr := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限/断链目录跳过, 不中断整轮扫描
		}
		if d.IsDir() {
			if shouldSkipDir(path, d.Name()) {
				skippedGo += countGoFiles(path)
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		scanned++
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if n := countShortCalls(string(b)); n > 0 {
			calls += n
			files++
		}
		return nil
	})
	if werr != nil {
		t.Fatalf("扫描失败: %v", werr)
	}
	return calls, files, scanned, skippedGo
}

// countGoFiles 数目录(含子目录)下的 .go 文件数。
func countGoFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			n++
		}
		return nil
	})
	return n
}

// TestShortTierCoverage 分层判定的存在性判据。
func TestShortTierCoverage(t *testing.T) {
	// 阈值: 实测 31 调用点 / 23 文件 / 扫描 200+ 个 .go (20261003, 已排除 snapshots)。
	// 留约 1/3 余量 —— 正常重构可删减, 但删掉三分之一就该有人解释为什么。
	const minCalls, minFiles, minScanned = 20, 15, 100
	calls, files, scanned, _ := shortCallStats(t)
	if scanned < minScanned {
		t.Fatalf("只扫到 %d 个 .go (<%d): 扫描范围出错, 判据无效 (宁可报红不可假绿)", scanned, minScanned)
	}
	if calls < minCalls {
		t.Errorf("testing.Short() 调用点仅 %d (<%d): 分层判定被删光 = quick 档静默退化成全量档", calls, minCalls)
	}
	if files < minFiles {
		t.Errorf("testing.Short() 只覆盖 %d 个文件 (<%d): 判定集中一处易被整段删除", files, minFiles)
	}
	t.Logf("分层覆盖实测: %d 调用点 / %d 文件 / 扫描 %d 个 .go", calls, files, scanned)
}

// TestCountShortCallsSelfCheck 是判据自身的判据 (变异自检), 两个方向都钉:
// 恒返回 0 → 上面的覆盖判据永远假红; 恒返回 >0 (不剥注释) → 判据恒真。
func TestCountShortCallsSelfCheck(t *testing.T) {
	real := "func TestX(t *testing.T) {\n\tif testing.Short() {\n\t\tt.Skip()\n\t}\n}\n"
	if got := countShortCalls(real); got != 1 {
		t.Errorf("真实调用应计 1, 得 %d", got)
	}
	two := "if testing.Short() {\n}\nif testing.Short() {\n}\n"
	if got := countShortCalls(two); got != 2 {
		t.Errorf("两处调用应计 2, 得 %d", got)
	}
	lineCmt := "// 这里提到 testing.Short() 但只是注释\nfunc TestY(t *testing.T) {}\n"
	if got := countShortCalls(lineCmt); got != 0 {
		t.Errorf("行注释里的提及不得计数, 得 %d", got)
	}
	blockCmt := "/*\ntesting.Short()\n*/\n"
	if got := countShortCalls(blockCmt); got != 0 {
		t.Errorf("块注释不得计数, 得 %d", got)
	}
	strLit := "var s = \"testing.Short()\"\n"
	if got := countShortCalls(strLit); got != 0 {
		t.Errorf("字符串字面量不得计数, 得 %d", got)
	}
	rawLit := "var s = `testing.Short()`\n"
	if got := countShortCalls(rawLit); got != 0 {
		t.Errorf("反引号字面量不得计数, 得 %d", got)
	}
	mixed := "// testing.Short()\nif testing.Short() { } // testing.Short()\nvar x = \"testing.Short()\"\n"
	if got := countShortCalls(mixed); got != 1 {
		t.Errorf("混合样本应计 1 (只算真调用), 得 %d", got)
	}
}

// TestShortScanExcludesSnapshots 是「扫描范围」的判据 (变异 M8 暴露的缺口):
// 主判据只数「扫到了多少」, 数不出「该跳的有没有跳」。
//
// 第一版判据用「跳过的总数 >= 快照数」, M8 实测假绿 —— 其他跳过目录的 .go
// 把缺口补上了。教训: 聚合量测不出单点失效, 判据必须精确到被保护的对象本身。
//
// 两道: ①目录级 (snapshots 必须被 shouldSkipDir 判跳)
//
//	②守恒 (实扫 + 跳过 == 全仓 .go 总数, 防「跳了但没计数」)
func TestShortScanExcludesSnapshots(t *testing.T) {
	snapDir := filepath.Join("defense_system", "snapshots")
	if _, err := os.Stat(snapDir); err != nil {
		t.Skip("无历史快照目录, 本判据不适用")
	}
	want := countGoFiles(snapDir)
	if want == 0 {
		t.Skip("快照目录下无 .go, 本判据不适用")
	}
	if !shouldSkipDir(snapDir, "snapshots") {
		t.Errorf("快照目录(含 %d 个 .go)未被排除: 计数会被历史副本污染, 活代码删光也测不出", want)
	}
	_, _, scanned, skipped := shortCallStats(t)
	total := countAllGoFiles()
	if scanned+skipped != total {
		t.Errorf("守恒失败: 实扫 %d + 跳过 %d != 全仓 %d (有目录被遍历却未计数)", scanned, skipped, total)
	}
	t.Logf("范围隔离: 实扫 %d + 跳过 %d = %d (快照目录 %d 个 .go)", scanned, skipped, total, want)
}

// countAllGoFiles 数全仓 .go 文件数, 不做任何目录排除 (守恒判据的基准)。
func countAllGoFiles() int {
	n := 0
	_ = filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			n++
		}
		return nil
	})
	return n
}
