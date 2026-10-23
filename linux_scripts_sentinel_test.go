package main

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// linux_scripts_sentinel_test.go —— Linux 启动/重启/回滚/测试脚本的判据 (T22 第 2 层)。
//
// 为什么需要: .cmd/.bat 在 Linux 上一条都跑不了(命令解释器不存在), 而「复制过去能启动
// 干活」的第一步就是「怎么启动」。脚本本身是死程序, 但它们的契约(方法论/兜底链/行尾/
// 白名单登记)没有任何东西钉住 —— 退化形态都不可见: 裸 go test 会吞掉日志、CRLF 会让
// sh 报 "not found"、未登记脚本会被 hygiene 判违规。
//
// 与 Windows 侧同款判据同构: TestTestScriptsUseCompiledBinary / TestTestScriptsArePureAscii。

var linuxScripts = []string{"start.sh", "restart.sh", "rollback.sh", "test.sh"}

func TestLinuxScriptsExistWithShebang(t *testing.T) {
	for _, name := range linuxScripts {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("缺 %s: %v", name, err)
			continue
		}
		if !strings.HasPrefix(string(b), "#!/bin/sh\n") {
			t.Errorf("%s 首行必须是 #!/bin/sh (且 LF 行尾)", name)
		}
	}
}

// TestLinuxScriptsUseLF 行尾判据: Windows 上写 .sh 极易带 CRLF, 而 sh 会把 \r 当命令名
// 的一部分 → "not found" 或语法错误。这类故障在 Linux 上才暴露, 必须在源头拦住。
func TestLinuxScriptsUseLF(t *testing.T) {
	for _, name := range linuxScripts {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("读 %s: %v", name, err)
			continue
		}
		if strings.Contains(string(b), "\r\n") {
			t.Errorf("%s 含 CRLF 行尾 —— sh 会执行失败 (需 LF)", name)
		}
	}
}

// TestTestShUsesCompiledBinary 与 Windows 侧 TestTestScriptsUseCompiledBinary 同构:
// 裸 "go test" 在 stdout 非终端时整体缓冲, 被 kill 的运行拿不到任何日志(实测 9s kill
// -> 0 字节)。通道方法论必须钉死, 否则「Linux 上跑不了测试」将无声退化。
func TestTestShUsesCompiledBinary(t *testing.T) {
	b, err := os.ReadFile("test.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "go test -c -o") {
		t.Error("test.sh 必须先用 go test -c 编译测试二进制, 再直接运行它")
	}
	if !strings.Contains(s, `exec "$BIN"`) {
		t.Error("test.sh 必须 exec 编译产物而非再调 go test")
	}
	if strings.Contains(s, "go test -short") || strings.Contains(s, "go test -v ") {
		t.Error("test.sh 退化为裸 go test —— 被 kill 时会丢光日志")
	}
	for _, want := range []string{"-test.short", "-test.timeout", "full"} {
		if !strings.Contains(s, want) {
			t.Errorf("test.sh 缺 %q (快速档/全量档契约)", want)
		}
	}
}

// TestRestartShHasAliveCheckAndRollback 重启脚本的兜底链: 存活检查 → 失败自动回滚。
// 少了存活检查 = 「重启成功」只是没报错; 少了回滚 = 坏二进制留在生产位。
func TestRestartShHasAliveCheckAndRollback(t *testing.T) {
	b, err := os.ReadFile("restart.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"pgrep -f", "pkill -f", "bak_*", "cp -f \"$BAK\""} {
		if !strings.Contains(s, want) {
			t.Errorf("restart.sh 缺 %q (存活检查/进程判定/回滚)", want)
		}
	}
}

// TestStartShChecksExecutable 启动脚本必须在启动前确认二进制可执行 —— 否则报的是
// shell 的 "not found", 主人无法分辨「没编译」与「路径错」。
func TestStartShChecksExecutable(t *testing.T) {
	b, err := os.ReadFile("start.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, "[ ! -x ") {
		t.Error("start.sh 必须先判 -x (可执行位), 否则失败信息无法区分未编译与路径错")
	}
	if !strings.Contains(s, `exec "$EXE"`) {
		t.Error("start.sh 应以 exec 交棒(不留多余 shell 进程)")
	}
}

// TestLinuxScriptsRegisteredInHygieneWhitelist 新增根目录文件必须登记白名单 ——
// 否则 hygiene 哨兵报红, 而报红信息在另一个脚本里, 两处脱节时脚本会被误删/误移。
func TestLinuxScriptsRegisteredInHygieneWhitelist(t *testing.T) {
	b, err := os.ReadFile("defense_system/hygiene_whitelist.json")
	if err != nil {
		t.Fatal(err)
	}
	var wl struct {
		AllowExact []string `json:"allow_exact"`
		AllowGlobs []string `json:"allow_globs"`
	}
	if err := json.Unmarshal(b, &wl); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, e := range wl.AllowExact {
		allowed[e] = true
	}
	for _, g := range wl.AllowGlobs {
		if g == "*.sh" {
			t.Fatal("allow_globs 不得放 *.sh —— 宽口子会让任何新脚本静默通过(同 *.ps1 的教训)")
		}
	}
	for _, name := range linuxScripts {
		if !allowed[name] {
			t.Errorf("%s 未登记 hygiene_whitelist.json 的 allow_exact", name)
		}
	}
}

// TestLineEndingsPolicyCoversLinuxArtifacts 行尾策略判据: Linux 侧产物(.sh 与 systemd
// 单元)必须在 .gitattributes 里被显式钉成 LF。core.autocrlf=true 会把检出文件转 CRLF,
// 而 sh 遇 CRLF 报 "not found"、systemd 遇 CRLF 键值解析失败 —— 两者都只在 Linux 上暴露,
// 在 Windows 开发机上看不出来(这正是需要判据而不是自觉的原因)。
func TestLineEndingsPolicyCoversLinuxArtifacts(t *testing.T) {
	b, err := os.ReadFile(".gitattributes")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, pat := range []string{"*.sh", "*.service", "*.timer"} {
		ok := false
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), pat) && strings.Contains(line, "eol=lf") {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf(".gitattributes 缺 %s 的 eol=lf 规则 (检出会变 CRLF, Linux 上才暴露)", pat)
		}
	}
}

// TestWindowsRestartCmdBackupIsDynamic —— Windows 侧对偶判据 (T33, 20261009)。
//
// 病根(实测): restart.cmd 曾硬编码 set "BAK=forge.exe.bak_20260805_204157",
// 而该文件早被清理 —— 新版 exe 起不来时回滚分支必然命中「无备份可回滚」,
// 安全网恰在最需要它的时刻失效。修法 = 回滚源动态取现存最新的备份。
// 本判据钉住修法本身: 硬编码备份名回归即报红(否则同型缺陷会以同样方式复发)。
func TestWindowsRestartCmdBackupIsDynamic(t *testing.T) {
	const name = "restart.cmd"
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读不到 %s (cwd 应为仓库根): %v", name, err)
	}
	s := string(b)
	if m := hardcodedBackupRe.FindString(s); m != "" {
		t.Errorf("%s 含硬编码备份名 %q —— 该文件可能已被清理, 回滚分支会静默失效; 改为动态解析", name, m)
	}
	for _, want := range []string{`set "BAK="`, "dir /b /o-d", "if not defined BAK"} {
		if !strings.Contains(s, want) {
			t.Errorf("%s 缺动态解析要素 %q", name, want)
		}
	}
	if !strings.Contains(s, "无备份可回滚") && !bytes.Contains(b, gbkNoBackupFallback) {
		t.Errorf("%s 缺「无备份可回滚」兜底提示 (一个备份都没有时必须可见)", name)
	}
}

// gbkNoBackupFallback = 「无备份可回滚」的 GBK 字节。
//
// 为什么硬编码字节而不是直接比中文 (T59-②, 20261017): restart.cmd 等含中文的 .cmd 已统一
// 为 GBK + chcp 936 (理由见 bat_encoding_sentinel_test.go: chcp 65001 + UTF-8 会让 cmd.exe
// 逐行读取时字节错位, 连后续 call 都被吃掉), 而 Go 源码里的中文字面量是 UTF-8 ——
// string(b) 之后中文全成乱码, strings.Contains 必然假红 (本判据实测就是这样红的)。
// 仓库禁第三方依赖 (TestGoModHasNoThirdPartyDeps), 故不做运行时转码, 改为双编码比对:
// UTF-8 字面量 + GBK 字节各试一次, 两种编码下都能工作。
var gbkNoBackupFallback = []byte{0xCE, 0xDE, 0xB1, 0xB8, 0xB7, 0xDD, 0xBF, 0xC9, 0xBB, 0xD8, 0xB9, 0xF6}

// hardcodedBackupRe 匹配形如 forge.exe.bak_20260805_204157 的硬编码备份名。
var hardcodedBackupRe = regexp.MustCompile(`forge\.exe\.bak_\d{8}_\d{6}`)

// TestHardcodedBackupReSelfCheck 判据自身也要有判据: 正则既不能漏判硬编码,
// 也不能把动态解析(%APP%.bak_*)误判成硬编码 —— 否则本哨兵要么恒绿要么恒红。
func TestHardcodedBackupReSelfCheck(t *testing.T) {
	if hardcodedBackupRe.FindString(`set "BAK=forge.exe.bak_20260805_204157"`) == "" {
		t.Error("正则漏判硬编码备份名")
	}
	if hardcodedBackupRe.FindString(`for /f %%f in ('dir /b /o-d "%APP%.bak_*"')`) != "" {
		t.Error("正则把动态解析误判为硬编码")
	}
}
