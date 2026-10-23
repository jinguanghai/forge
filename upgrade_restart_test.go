package main

import (
	"strings"
	"testing"
)

// upgrade_restart_test.go —— 重启脚本的跨平台契约 (T22 第 2 层)。
//
// 为什么 sh 版能在本机(Windows)被测: 脚本正文由纯函数 buildRestartScriptSH 生成,
// 不含任何平台 API。本机无 WSL, 这是 Linux 侧重启语义唯一的死程序判据 ——
// 否则「Linux 上升级后程序不回来」这类故障要等真机才暴露。
//
// 语义逐条对齐 PowerShell 版(upgrade_test.go 的 TestBuildRestartScript 是它的对偶):
// 排空等待 → 超时强杀 → 备份失败拒绝替换 → 冒烟失败回滚 → 重建基线 → 拉起新进程。

func TestBuildRestartScriptSH_Contract(t *testing.T) {
	s := buildRestartScriptSH("/home/jin/forge", 12345, "20261008_150000")
	for _, want := range []string{
		"wd='/home/jin/forge'",                 // 工作目录注入
		"kill -0 12345",                        // 轮询主进程存活
		"deadline=$(( $(date +%s) + 30 ))",     // 排空等待上限 30s
		"kill -9 12345",                        // 超时才强杀(兜底)
		`cp "$exe" "$exe.bak_20261008_150000"`, // 备份在替换之前
		"refuse to replace",                    // 备份失败 = 无回滚能力 → 拒绝替换
		`mv "$exe.new" "$exe"`,                 // 就位
		`"$exe" --version`,                     // 冒烟: 编译过 != 能跑
		"rolling back",                         // 冒烟失败回滚
		`mv "$exe.bak_20261008_150000" "$exe"`,
		"python3",             // 重建防御基线(探测 python3)
		"nohup",               // 脱离终端拉起新进程
		"upgrade_restart.log", // 失败留痕: 升级失败必须可见
	} {
		if !strings.Contains(s, want) {
			t.Errorf("sh 脚本缺少: %s", want)
		}
	}
	// 排空式: 强杀必须出现在等待逻辑之后(顺序错了就退化成「先杀再等」)
	if strings.Index(s, "deadline=") > strings.Index(s, "kill -9") {
		t.Error("脚本顺序错误: 强杀应在排空等待之后")
	}
	// 致命步骤必须 exit 1(而非静默继续)
	if !strings.Contains(s, "exit 1") {
		t.Error("致命失败路径缺少 exit 1")
	}
}

// TestBuildRestartScriptSH_NoPlaceholderLeft 占位符必须全部被替换 ——
// 漏一个不会报错, 只会让脚本在真机上带着 @@WD@@ 去 cd(静默失效)。
func TestBuildRestartScriptSH_NoPlaceholderLeft(t *testing.T) {
	s := buildRestartScriptSH("/tmp/wd", 7, "TS")
	if strings.Contains(s, "@@") {
		t.Errorf("模板占位符未全部替换: %q", s[strings.Index(s, "@@"):min(len(s), strings.Index(s, "@@")+40)])
	}
}

// TestBuildRestartScriptSH_LFOnly sh 脚本必须 LF 行尾(\r 会被当成命令名的一部分)。
func TestBuildRestartScriptSH_LFOnly(t *testing.T) {
	if s := buildRestartScriptSH("/tmp/wd", 7, "TS"); strings.Contains(s, "\r") {
		t.Error("sh 脚本含 CR —— sh 会报 not found")
	}
}

// TestBuildRestartScriptSH_PureASCII 正文纯 ASCII: 与 PowerShell 版同纪律 ——
// 目标机 locale 不确定时, 非 ASCII 日志会乱码且无法定位故障。
func TestBuildRestartScriptSH_PureASCII(t *testing.T) {
	for i, r := range buildRestartScriptSH("/tmp/wd", 7, "TS") {
		if r > 127 {
			t.Fatalf("sh 脚本第 %d 个字符非 ASCII: %q", i, r)
		}
	}
}

// TestRenderRestartTemplate_AllPlaceholders 渲染器必须处理全部四个占位符 ——
// 漏一个的表现是脚本里留着 @@XXX@@ 静默跑(上面那条判据的反向保证)。
func TestRenderRestartTemplate_AllPlaceholders(t *testing.T) {
	tpl := "wd=@@WD@@ pid=@@PID@@ ts=@@TS@@ exe=@@EXE@@"
	got := renderRestartTemplate(tpl, "/w", 42, "T1")
	want := "wd=/w pid=42 ts=T1 exe=" + mainExeName()
	if got != want {
		t.Errorf("renderRestartTemplate = %q, 期望 %q", got, want)
	}
}
