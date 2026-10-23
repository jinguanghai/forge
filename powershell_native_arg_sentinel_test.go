package main

// powershell_native_arg_sentinel_test.go — 「PowerShell 传给外部程序的参数不得裸写 -x.y」的判据 (20261016)
//
// 为什么需要: 20261016 09:03 upgrade.ps1 按新通道方法论改成「先 go test -c 编译,
// 再直跑测试二进制」, 改完当天两次运行 (09:03 / 12:03) 全在 [1/6] 中止, 症状是
// 「go test 失败」但日志里一行 "--- FAIL" 都没有 —— 真凶是脚本第 122 行:
//
//	& $TBIN -test.v -test.timeout=1800s
//
// Windows PowerShell 5.1 把这种裸写的带点参数在 '.' 处切开, 子进程收到的 argv 是
// ['-test', '.v', '-test', '.timeout=1800s'], go 测试程序当即报
// "flag provided but not defined: -test" 并退出 → 退出码非 0 → 判「测试失败」。
// 旧写法 (裸 go test) 不经过 PS 参数绑定, 所以同一句改写前是好的 (08:32 日志
// "ok forge 205.221s")。cmd 批处理不受影响, 故只有 upgrade 入口中招。
//
// 已有的 upgrade_log_sentinel_test.go / go_test_entrance_sentinel_test.go 都是
// 文本包含式检查, 抓不到「PowerShell 运行时会怎么切参数」—— 那是运行期行为。
// 本判据把该行为编码成静态规则: 剥掉字符串与注释后, 代码骨架里任何裸写的
// "-名字.后缀" 都报红 (数组展开 @('...') 与逐个加引号是合规写法, 均在引号内)。
//
// 判据自身也要有判据 (公理三): TestPs1DottedFlagsSelfCheck 用变异样本钉住
// ps1DottedFlags 有区分度, 防止它恒返回空 (假绿比没有判据更危险)。

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// ps1NativeArgExts 是受本判据约束的脚本扩展名 —— 只有 PowerShell 有该切分行为,
// cmd/sh 的裸参数原样传给子进程, 不在此列 (误纳入会假红)。
var ps1NativeArgExts = []string{".ps1"}

// ps1NativeArgAnchor 是必须落在扫描范围内且合规的脚本 (下限锚点, 防扫描范围被改小)。
var ps1NativeArgAnchor = []string{"upgrade.ps1"}

// ps1DottedFlagRe 匹配代码骨架里裸写的 "-名字.后缀" 参数 token。
// 前置要求是行首或分隔符 (空白 / & | ; ( ,), 避免把 $a-b.c 这类表达式误判成参数。
var ps1DottedFlagRe = regexp.MustCompile(`(^|[\s&|;,(])(-[A-Za-z][A-Za-z0-9_]*\.[A-Za-z0-9_.=\-]+)`)

// ps1StripStringsAndComments 剥掉一行 .ps1 里的字符串字面量与行内注释, 只留代码骨架。
// 单引号内 ” 是转义的单引号; 双引号内反引号转义下一个字符。
// 边界: 不做 here-string (@' '@) 与多行字符串的跨行跟踪 —— 本仓 .ps1 无此写法,
// 若将来出现需补 (跨行状态机), 否则行内剥不干净会假红。
func ps1StripStringsAndComments(line string) string {
	var b strings.Builder
	rs := []rune(line)
	var quote rune
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch quote {
		case '\'':
			if c == '\'' {
				if i+1 < len(rs) && rs[i+1] == '\'' {
					i++
					continue
				}
				quote = 0
			}
			continue
		case '"':
			if c == '`' {
				i++
				continue
			}
			if c == '"' {
				if i+1 < len(rs) && rs[i+1] == '"' {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '#':
			return b.String() // 行内注释: 其后内容不参与判定
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// ps1DottedFlags 返回裸写带点参数的命中行描述 (无命中即合规)。
func ps1DottedFlags(content string) []string {
	var bad []string
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	for i, line := range strings.Split(norm, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		for _, m := range ps1DottedFlagRe.FindAllStringSubmatch(ps1StripStringsAndComments(line), -1) {
			bad = append(bad, fmt.Sprintf("第 %d 行: %s  << 裸参数 %q >>", i+1, t, m[2]))
		}
	}
	return bad
}

// TestPs1NativeArgsNotDottedBare 扫根目录全部 .ps1, 禁裸写带点参数。
func TestPs1NativeArgsNotDottedBare(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("列根目录失败: %v", err)
	}
	seen := map[string]bool{}
	scanned, badTotal := 0, 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		matched := false
		for _, ext := range ps1NativeArgExts {
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
		if bad := ps1DottedFlags(string(b)); len(bad) > 0 {
			badTotal += len(bad)
			t.Errorf("%s 裸写带点参数 —— Windows PowerShell 5.1 会在 '.' 处把它切开:\n    %s\n  → 实测: `& $bin -test.v` 到达子进程的 argv 是 ['-test', '.v'],\n    程序报 \"flag provided but not defined\" 后立即退出。\n    改法: $flags = @('-test.v','-test.timeout=1800s'); & $bin @flags",
				e.Name(), strings.Join(bad, "\n    "))
		}
	}
	if scanned == 0 {
		t.Fatal("根目录一个 .ps1 都没扫到 —— 判据范围失效 (扫描被改坏)")
	}
	for _, name := range ps1NativeArgAnchor {
		if !seen[name] {
			t.Errorf("锚点脚本 %s 不在扫描结果里 —— 扫描范围被改小", name)
		}
	}
	t.Logf("扫描 %d 个 .ps1, 裸写带点参数 %d 处 (锚点 %d 个全在)", scanned, badTotal, len(ps1NativeArgAnchor))
}

// TestPs1DottedFlagsSelfCheck 是判据自身的判据 (变异自检): 正向/反向样本各半,
// 若 ps1DottedFlags 恒返回空或恒非空, 这里立即报红。
func TestPs1DottedFlagsSelfCheck(t *testing.T) {
	cases := []struct {
		name, src string
		wantBad   bool
	}{
		{"事故原形 (裸参)", "& $TBIN -test.v -test.timeout=1800s 2>&1 | ForEach-Object {\n", true},
		{"数组展开 (合规改法)", "$flags = @('-test.v', '-test.timeout=1800s')\n& $TBIN @flags\n", false},
		{"逐个加引号 (合规)", "& $TBIN \"-test.v\" \"-test.timeout=1800s\"\n", false},
		{"整行注释提及", "# 裸写 -test.v 会被切开\n", false},
		{"错误消息提及", "NG \"别用 -test.v 裸写\"\n", false},
		{"日志标题提及", "Write-VLog \"go test -c\" $cout\n", false},
		{"cmdlet 开关 (无点, 不误伤)", "Remove-Item -Force -ErrorAction SilentlyContinue $f\n", false},
		{"cmdlet 带值参数", "Select-Object -Skip 10 -First 1\n", false},
		{"对象属性 (无前导短横)", "$_.Exception.Message\n", false},
		{"文件路径字面量", "Write-Host \"日志: upgrade_verify_1.log\"\n", false},
		{"算术区间", "foreach ($i in 1..10) { $x += $i }\n", false},
		{"行尾注释里提及", "$x = 1  # 别写 -test.v\n", false},
	}
	for _, c := range cases {
		got := len(ps1DottedFlags(c.src)) > 0
		if got != c.wantBad {
			t.Errorf("[%s] 期望违规=%v 实得=%v (源码 %q)", c.name, c.wantBad, got, c.src)
		}
	}
}
