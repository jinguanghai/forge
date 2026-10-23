package main

// go_test_entrance_sentinel_test.go — 「凡跑 go test 的入口都必须先编译再直跑」的判据 (T57, 20261016)
//
// 为什么需要: full_test.cmd / quick_test.cmd / test.sh 早已改成
// 「go test -c 编译 → 直跑测试二进制」, 并由 TestTestScriptsUseCompiledBinary 钉住。
// 但那条判据的清单写死了两个文件名, upgrade.ps1 从未纳入 ——
// 于是同一条教训在升级入口原样复发: 20261016 08:04 双击 upgrade.cmd 报 go test 失败,
// 控制台只留下三行 FAIL, 真正红的是哪个用例随窗口消失, 事后无从分辨。
//
// 本判据不枚举清单, 而是扫根目录全部脚本 (.cmd/.bat/.ps1/.sh):
// 任何一处出现裸 "go test" 即报红。清单会漏, 扫全目录不会。
// 实测依据 (20261001, 见 full_test.cmd 注释): 9s kill 下
//
//	裸 go test        -> 日志 0 字节 (整包缓冲, 包没结束什么都不落)
//	go test -c + 直跑 -> 日志 21630 字节 / 539 行

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// goTestEntranceExts 是会被当作「测试入口」的脚本扩展名。
var goTestEntranceExts = []string{".cmd", ".bat", ".ps1", ".sh"}

// goTestEntranceAnchor 是必须落在扫描范围内且合规的入口 (下限锚点)。
// 穷尽性由「扫根目录全部脚本」保证, 这几条只防「扫描范围被悄悄改小」——
// 清单式判据的漏洞是漏登记, 锚点式判据的漏洞是范围缩小, 两者互为补丁。
var goTestEntranceAnchor = []string{"quick_test.cmd", "full_test.cmd", "test.sh", "upgrade.ps1"}

// scriptCommentLine 判定整行注释 (cmd: REM / @REM / ::, ps1·sh: #)。
func scriptCommentLine(t string) bool {
	up := strings.ToUpper(t)
	if up == "REM" || strings.HasPrefix(up, "REM ") || strings.HasPrefix(up, "REM\t") {
		return true
	}
	if strings.HasPrefix(up, "@REM") {
		return true
	}
	return strings.HasPrefix(t, "::") || strings.HasPrefix(t, "#")
}

// goTestCallRe 只认「命令位置」的 go test —— 行首, 或 & | ; ( 之后。
//
// 为什么不是「行内任意位置」(20261016 实测假红): 剥注释只解决了一半。
// 脚本里大量【字符串字面量】会提到 go test:
//
//	C "  -- go test --"            <- 显示文本
//	Write-VLog "go test -c" $cout  <- 日志标题
//	NG "go test 失败"              <- 错误消息
//
// 行内匹配把这三种"提及"判成"使用", 判据一上线就假红, 还逼着人去改文案
// 而不是改行为 —— 判据的可用性被自己的粗粒度毁掉。真正的调用只有两种写法:
// 行首 "go test ..." 或 "& go test ..." (PowerShell 调用外部命令)。
// 已知边界: "cmd /c \"go test .\"" 这种把命令包在引号里的写法本仓不存在,
// 若将来出现需补判据 (引号内内容不参与本判据)。
var goTestCallRe = regexp.MustCompile(`(?:^|[&|;(])\s*go\s+test`)

// bareGoTestCalls 返回裸 "go test" (未跟 -c) 的命中行描述。
// 注意 "-count=1" 不得被当成 "-c": 故只认 "-c" 后接空白或行尾。
func bareGoTestCalls(content string) []string {
	var bad []string
	for i, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || scriptCommentLine(t) {
			continue
		}
		for _, m := range goTestCallRe.FindAllStringIndex(line, -1) {
			rest := strings.TrimLeft(line[m[1]:], " \t")
			if rest == "-c" || strings.HasPrefix(rest, "-c ") || strings.HasPrefix(rest, "-c\t") {
				continue // go test -c ... : 合规
			}
			bad = append(bad, fmt.Sprintf("第 %d 行: %s", i+1, t))
		}
	}
	return bad
}

// TestGoTestEntrancesUseCompiledBinary 扫根目录全部脚本入口, 禁裸 go test。
func TestGoTestEntrancesUseCompiledBinary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("列根目录失败: %v", err)
	}
	seen := map[string]bool{}
	scanned := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		matched := false
		for _, ext := range goTestEntranceExts {
			if strings.HasSuffix(low, ext) {
				matched = true
			}
		}
		if !matched {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Errorf("读 %s 失败: %v", e.Name(), err)
			continue
		}
		scanned++
		seen[e.Name()] = true
		if bad := bareGoTestCalls(string(b)); len(bad) > 0 {
			t.Errorf("%s 用了裸 go test (被中断时输出全丢, 失败不可追溯):\n    %s\n  → 先 \"go test -c -o <bin>\" 编译, 再直跑该二进制",
				e.Name(), strings.Join(bad, "\n    "))
		}
	}
	if scanned == 0 {
		t.Fatal("根目录一个脚本入口都没扫到 —— 判据范围失效 (扫描被改坏)")
	}
	for _, name := range goTestEntranceAnchor {
		if !seen[name] {
			t.Errorf("锚点入口 %s 不在扫描结果里 —— 扫描范围被改小", name)
		}
	}
	t.Logf("扫描 %d 个脚本入口, 裸 go test 0 处 (锚点 %d 个全在)", scanned, len(goTestEntranceAnchor))
}

// TestBareGoTestCallsSelfCheck 是判据自身的判据 (变异自检):
// 若 bareGoTestCalls 恒返回空, 上面的用例永远绿 —— 那是假绿, 比没有判据更危险。
func TestBareGoTestCallsSelfCheck(t *testing.T) {
	cases := []struct {
		name, src string
		wantBad   bool
	}{
		{"裸 go test", "$out = & go test . 2>&1\n", true},
		{"裸 go test -count (不得误判为 -c)", "go test -count=1 .\n", true},
		{"裸 go test -short", "go test -short\n", true},
		{"编译后直跑", "go test -c -o \"x.exe\" .\n\"x.exe\" -test.v\n", false},
		{"行尾只有 -c", "go test -c\n", false},
		{"cmd 注释提及", "REM 别用裸 go test, 先 go test -c\n", false},
		{"显示文本提及 (命令位置判据)", "C \"  -- go test --\"\n", false},
		{"错误消息提及", "NG \"go test 失败\"\n", false},
		{"日志标题提及", "Write-VLog \"go test -c\" $cout\n", false},
		{"bat @REM 提及", "@REM go test 会缓冲\n", false},
		{"ps1 注释提及", "# 裸 go test 会缓冲\n", false},
		{"sh 注释提及", "# bare go test buffers\n", false},
		{"cmd :: 注释", ":: go test buffers\n", false},
		{"go build 不误伤", "go build -o forge.exe .\n", false},
		{"无 go test", "go vet .\n", false},
	}
	for _, c := range cases {
		got := len(bareGoTestCalls(c.src)) > 0
		if got != c.wantBad {
			t.Errorf("[%s] 期望违规=%v 实得=%v (源码 %q)", c.name, c.wantBad, got, c.src)
		}
	}
}
