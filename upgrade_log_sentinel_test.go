package main

// upgrade_log_sentinel_test.go — 升级脚本"验证输出落盘"的接线判据 (20261016)
//
// 为什么需要: 20261016 08:04 双击 upgrade.cmd 报「go test 失败 / 自动验证未通过」,
// 而控制台上真正指出哪个用例红的 "--- FAIL: TestXxx" 行随窗口关闭一起消失,
// 事后只剩三行 FAIL, 无从分辨是网络抖动还是真红 —— 判据的输出没落盘 = 失败不可追溯。
// 现 upgrade.ps1 把 go vet/test/build 三段输出同时写 .forge/logs/upgrade_verify_*.log,
// 且 go test 段走流式落盘 (StreamWriter + AutoFlush) —— 光"落盘"不够: 整块缓冲的写法
// 在进程被中断时照样落个空文件, 等于没落 (20261016 二次修复)。
//
// 判据自身也要有判据 (公理三): 落盘逻辑被悄悄摘掉、或脚本的轮转份数与
// .forge 清单的 patterns.keep 漂移, 都会在这里报红 —— 否则"日志"只是脚本里的愿望。

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	// upgradeScriptFile 是判据的对照对象 (缺失即 fail-closed)。
	upgradeScriptFile = "upgrade.ps1"
	// upgradeLogGlob 必须与 manifest subdirs[logs].patterns[0].glob 一致。
	upgradeLogGlob = "upgrade_verify_*.log"
	// upgradeLogKeep 是轮转份数上限: 脚本 -Skip N 与清单 keep 必须同数 ——
	// 单一数字两个落点, 改一处忘另一处会让 .forge 哨兵常驻报红 (狼来了)。
	upgradeLogKeep = 10
)

// upgradeScriptText 读取升级脚本, 读不到即 fail-closed (判据无对照对象时不得静默通过)。
func upgradeScriptText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(upgradeScriptFile)
	if err != nil {
		t.Fatalf("读取 %s 失败, 落盘判据失去对照对象 (fail-closed): %v", upgradeScriptFile, err)
	}
	return string(b)
}

// TestUpgradeScriptLogsVerifyOutput 哨兵: 三段验证输出必须落盘, 且失败时能摘出用例名。
func TestUpgradeScriptLogsVerifyOutput(t *testing.T) {
	src := upgradeScriptText(t)
	must := []struct{ frag, why string }{
		{`.forge\logs`, "验证日志目录 (须与 manifest entries[logs] 一致)"},
		{"upgrade_verify_$vstamp.log", "带时间戳的日志文件名"},
		{"function Write-VLog", "落盘函数"},
		{"--- FAIL", "失败用例摘取模式"},
		{"Select-Object -Skip " + strconv.Itoa(upgradeLogKeep), "轮转份数"},
	}
	for _, c := range must {
		if !strings.Contains(src, c.frag) {
			t.Errorf("%s 缺少 %q —— %s; 落盘判据被摘掉后失败将重新不可追溯",
				upgradeScriptFile, c.frag, c.why)
		}
	}
	// 三段输出各自落盘: 只落一段 = 另一段失败仍不可复盘。
	// go vet / go build 输出短, 用 Write-VLog 整块写;
	// go test 是全量长跑 (200s+), 必须流式落盘 —— 整块写(Out-String)在进程被
	// 中断时变量随进程消失, 落盘等于落了个空文件 (20261016 补)。
	for _, step := range []string{"go vet", "go build"} {
		if !strings.Contains(src, `Write-VLog "`+step+`"`) {
			t.Errorf("%s 未把 %s 的输出落盘 (Write-VLog %q 缺失)", upgradeScriptFile, step, step)
		}
	}
	for _, c := range []struct{ frag, why string }{
		{`Open-VLogStream "go test"`, "go test 段流式落盘未开启 (长跑被中断即落空文件)"},
		{"Write-VLogStream $line", "逐行写入缺失 (整块缓冲 = 中断丢光)"},
		{"Close-VLogStream", "流式落盘未收尾"},
		{"AutoFlush = $true", "未开 AutoFlush: 输出攒在进程内, 中断照样丢"},
	} {
		if !strings.Contains(src, c.frag) {
			t.Errorf("%s 缺少 %q —— %s", upgradeScriptFile, c.frag, c.why)
		}
	}
}

// TestUpgradeLogKeepInSync 哨兵: 脚本轮转份数 == 清单 keep == 常量 (三处同数)。
func TestUpgradeLogKeepInSync(t *testing.T) {
	src := upgradeScriptText(t)
	re := regexp.MustCompile(`Select-Object -Skip (\d+)`)
	mt := re.FindStringSubmatch(src)
	if mt == nil {
		t.Fatalf("%s 找不到 'Select-Object -Skip N' —— 轮转被摘掉, 日志将无上限增长", upgradeScriptFile)
	}
	n, err := strconv.Atoi(mt[1])
	if err != nil {
		t.Fatalf("解析轮转份数失败: %v", err)
	}
	if n != upgradeLogKeep {
		t.Errorf("脚本轮转 %d 份 != 常量 upgradeLogKeep %d (两处必须同改)", n, upgradeLogKeep)
	}

	m := loadForgeManifest(t)
	got := -1
	glob := ""
	for _, sd := range m.Subdirs {
		if sd.Path != "logs" {
			continue
		}
		for _, p := range sd.Patterns {
			if p.Glob == upgradeLogGlob {
				got, glob = p.Keep, p.Glob
			}
		}
	}
	if glob == "" {
		t.Fatalf("manifest 无 subdirs[logs].patterns[%s] —— 日志目录内部重回盲区", upgradeLogGlob)
	}
	if got != upgradeLogKeep {
		t.Errorf("清单 keep %d != 常量 %d —— 脚本轮转与 .forge 哨兵上限漂移 (脚本留 %d 份而清单说 %d)",
			got, upgradeLogKeep, n, got)
	}
	// 根层须有 logs 条目, 否则目录本身未登记 -> .forge 哨兵报红。
	found := false
	for _, e := range m.Entries {
		if e.Name == "logs" {
			found = true
		}
	}
	if !found {
		t.Errorf("manifest entries 无 logs 条目 —— .forge/logs 会成为未登记项")
	}
}
