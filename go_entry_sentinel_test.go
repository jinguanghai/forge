package main

// go_entry_sentinel_test.go — go test 入口「失败证据」判据 (T59-③, 20261016)
//
// 通道方法论 (判据来自实测, 见 test_channel_sentinel_test.go):
//
//	stdout 非终端时 go test 缓冲整包输出, 被中断(超时/关窗/Ctrl+C) → 日志 0 字节, 只剩退出码;
//	go test -c 编译成测试二进制直跑 → 逐行写出 (实测 9s kill: 0B vs 21630B / 539 行)。
//
// 为什么另立本判据 (T59-③ 实测暴露):
//
//	旧版把「必须编译后直跑」钉在一张写死清单上 (automationScripts = full/quick_test.cmd),
//	于是 upgrade.ps1 —— 含 22 处 go 命令、其中就有裸 go test —— 长期在判据之外。
//	2026-10-16 08:04 双击 upgrade.cmd 失败时, 指出红在哪的 --- FAIL 行随窗口消失,
//	事后只能靠代理守护日志与耗时反推是链路抖动 —— 属证据链拼凑, 不是日志给的。
//	教训同「判据/哨兵必须扫全包, 禁写死单文件」(本仓库已复发多次): 清单式判据漏登即盲区。
//
// 本判据改为动态扫描: 根目录全部「会真正执行 go test」的入口脚本 (.cmd/.bat/.ps1/.sh),
// 一律要求 ① go test -c 编译后直跑 ② 显式给 -test.timeout=。
//
// 剥注释 + 引号感知是必需的, 不是洁癖:
//
//	实测 upgrade.ps1 里有 7 处 "go test" 出现在注释或提示字符串里, 只有 1 处是真命令。
//	裸 strings.Contains → 那 7 处被判成裸 go test (假红); 把文件整体当字符串 → 真命令
//	反而可能躲在引号里漏检。判据必须认「命令位置」, 而不是「文本出现」。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// goEntryExts 是可能承载 go 命令的脚本入口扩展名。
var goEntryExts = []string{".cmd", ".bat", ".ps1", ".sh"}

func goEntryExtOK(name string) bool {
	low := strings.ToLower(name)
	for _, e := range goEntryExts {
		if strings.HasSuffix(low, e) {
			return true
		}
	}
	return false
}

// scriptCodeLines 返回脚本的非注释行 (REM / :: / # 前缀, 大小写不敏感)。
// 注释里提到 go test 不算入口 —— 这是「注释不是接线」的同一教训。
func scriptCodeLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		up := strings.ToUpper(t)
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "::") || up == "REM" || strings.HasPrefix(up, "REM ") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// goTestTokens 返回一行中「引号之外」出现的 go test 调用的后随 token。
// (词边界判定复用 gatesync.go 的 isWordByte —— 同包内不重复定义, 避免两处口径漂移)
// token 为空串 = go test 后没有参数 (裸调用)。
func goTestTokens(line string) []string {
	var out []string
	var quote byte
	for i := 0; i < len(line); {
		c := line[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			i++
			continue
		}
		if strings.HasPrefix(line[i:], "go") && (i == 0 || !isWordByte(line[i-1])) {
			k := i + 2
			for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
				k++
			}
			if k > i+2 && strings.HasPrefix(line[k:], "test") && (k+4 >= len(line) || !isWordByte(line[k+4])) {
				m := k + 4
				for m < len(line) && (line[m] == ' ' || line[m] == '\t') {
					m++
				}
				tok := ""
				if m < len(line) {
					end := m
					for end < len(line) && line[end] != ' ' && line[end] != '\t' {
						end++
					}
					tok = line[m:end]
				}
				out = append(out, tok)
				i = m
				continue
			}
		}
		i++
	}
	return out
}

// isGoTestEntry 判定脚本内容是否会真正执行 go test (注释与字符串里的提及不算)。
func isGoTestEntry(content string) bool {
	for _, ln := range scriptCodeLines(content) {
		if len(goTestTokens(ln)) > 0 {
			return true
		}
	}
	return false
}

// goTestEntryProblem 判定一个 go test 入口是否走「编译后直跑」—— 纯函数, 空串 = 合格。
// 非入口 (无 go test 调用) 同样返回空串: 本判据只管入口, 不管无关脚本。
func goTestEntryProblem(name, content string) string {
	var calls []string
	lines := scriptCodeLines(content)
	for _, ln := range lines {
		calls = append(calls, goTestTokens(ln)...)
	}
	if len(calls) == 0 {
		return ""
	}
	for _, tok := range calls {
		if !strings.HasPrefix(tok, "-c") {
			return fmt.Sprintf("%s: 存在裸 go test (后随 %q): 必须 go test -c 编译后直跑, 否则被中断时日志全丢", name, tok)
		}
	}
	code := strings.Join(lines, "\n")
	if !strings.Contains(code, "-test.timeout=") {
		return fmt.Sprintf("%s: 缺少 -test.timeout=: 直跑测试二进制时默认 10m 会误杀长通道", name)
	}
	return ""
}

// scanGoTestEntrypoints 扫目录 (只一层, 不递归) 里会真正执行 go test 的入口脚本。
func scanGoTestEntrypoints(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !goEntryExtOK(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if !isGoTestEntry(string(b)) {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// TestGoTestEntrypointsUseCompiledBinary 是 T59-③ 的判据本体。
func TestGoTestEntrypointsUseCompiledBinary(t *testing.T) {
	scripts, err := scanGoTestEntrypoints(".")
	if err != nil {
		t.Fatalf("扫根目录失败 (cwd 应为仓库根): %v", err)
	}
	// 扫描器自检: 两个测试通道本体必须被识别 —— 扫描器坏掉会让本判据恒绿 (假绿比没有更危险)。
	for _, must := range []string{"full_test.cmd", "quick_test.cmd"} {
		found := false
		for _, s := range scripts {
			if s == must {
				found = true
			}
		}
		if !found {
			t.Fatalf("扫描器未识别 %s (结果 %v): 判据失效, 不是产品缺陷", must, scripts)
		}
	}
	if len(scripts) < 3 {
		t.Fatalf("只扫到 %d 个 go test 入口 %v: 数量明显偏少, 先查扫描器", len(scripts), scripts)
	}
	for _, name := range scripts {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读不到入口 %s: %v", name, err)
		}
		if prob := goTestEntryProblem(name, string(b)); prob != "" {
			t.Error(prob)
		}
	}
	t.Logf("go test 入口 %d 个全部走「编译后直跑」: %v", len(scripts), scripts)
}

// TestGoTestEntryProblemSelfCheck 是判据自身的判据 (变异自检):
// 若 goTestEntryProblem 恒返回空串, 上面那条就永远绿 —— 那是假绿, 比没有判据更危险。
// 反向向量 (注释/字符串/非入口) 同样必需: 只验报红方向会漏掉"剥得过狠"的假红。
func TestGoTestEntryProblemSelfCheck(t *testing.T) {
	const okBody = "go test -c -o bin .\nbin -test.v -test.timeout=600s\n"
	cases := []struct {
		name    string
		content string
		wantRed bool
	}{
		{"bare.cmd", "go test .\n", true},
		{"bare-noarg.cmd", "go test\n", true},
		{"ok.cmd", okBody, false},
		{"comment.ps1", "# go test .\n" + okBody, false},
		{"rem.cmd", "REM go test .\n" + okBody, false},
		{"quoted.ps1", "if ($rc -ne 0) { NG \"go test 失败\" }\n" + okBody, false},
		{"notentry.md", "no go here\n", false},
		{"notimeout.sh", "go test -c -o b .\n", true},
		{"gopher.cmd", "gopher test .\n" + okBody, false},
	}
	for _, c := range cases {
		prob := goTestEntryProblem(c.name, c.content)
		if c.wantRed && prob == "" {
			t.Errorf("%s: 应报红却判绿 —— 判据无鉴别力", c.name)
		}
		if !c.wantRed && prob != "" {
			t.Errorf("%s: 应判绿却报红: %s", c.name, prob)
		}
	}
	// 扫描器的入口判定也必须双向: 合规入口认得出, 纯注释不认。
	if !isGoTestEntry(okBody) {
		t.Error("扫描器未把合规入口识别为 go test 入口")
	}
	if isGoTestEntry("# go test .\n") {
		t.Error("扫描器把纯注释当成了入口 (剥注释失效)")
	}
	if isGoTestEntry("go build .\n") {
		t.Error("扫描器把 go build 当成了 go test 入口")
	}
}
