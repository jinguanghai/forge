// main_startup_test.go: 启动流程的行为契约。
//
// runSelfReplace / installSignals 无法在测试进程内真实执行(前者要求二进制名为
// forge_new, 后者会注册真实信号处理器并 os.Exit), 改为"能测的行为测透 +
// 不可测的部分用源码级结构断言钉住接线"。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"forge.exe"}, args...)
	t.Cleanup(func() { os.Args = old })
}

func withEnvRestore(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestMainStartup_ParseFlags(t *testing.T) {
	withEnvRestore(t, "FORGE_SHOW_REASONING")

	// 无参数: 默认显示推理
	withArgs(t)
	show, rest := parseFlags()
	if !show {
		t.Error("默认应显示推理链")
	}
	if len(rest) != 0 {
		t.Errorf("无位置参数时 rest 应为空: %v", rest)
	}
	if os.Getenv("FORGE_SHOW_REASONING") != "true" {
		t.Error("应导出 FORGE_SHOW_REASONING=true")
	}

	// --no-reasoning 关闭
	withArgs(t, "--no-reasoning")
	show, _ = parseFlags()
	if show {
		t.Error("--no-reasoning 应关闭推理链")
	}
	if os.Getenv("FORGE_SHOW_REASONING") != "false" {
		t.Error("应导出 FORGE_SHOW_REASONING=false")
	}

	// -r 与 --reasoning 等价
	withArgs(t, "-r")
	if show, _ := parseFlags(); !show {
		t.Error("-r 应开启推理链")
	}
	withArgs(t, "--reasoning")
	if show, _ := parseFlags(); !show {
		t.Error("--reasoning 应开启推理链")
	}

	// 位置参数与 flag 分离
	withArgs(t, "--no-reasoning", "列出当前目录")
	_, rest = parseFlags()
	if len(rest) != 1 || rest[0] != "列出当前目录" {
		t.Errorf("位置参数 = %v", rest)
	}

	// 首个位置参数之后的内容原样保留(即使长得像 flag)
	withArgs(t, "查询", "-r")
	_, rest = parseFlags()
	if len(rest) != 2 || rest[0] != "查询" || rest[1] != "-r" {
		t.Errorf("flag 之后的参数必须原样保留: %v", rest)
	}
}

func TestMainStartup_SetupHistoryFile(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{WorkDir: dir}
	got := setupHistoryFile(cfg)
	want := filepath.Join(dir, ".forge", "history")
	if got != want {
		t.Errorf("历史文件路径 = %q 期望 %q", got, want)
	}
	info, err := os.Stat(filepath.Join(dir, ".forge"))
	if err != nil {
		t.Fatalf(".forge 目录未创建: %v", err)
	}
	if !info.IsDir() {
		t.Error(".forge 应为目录")
	}
	// 重复调用必须幂等(MkdirAll 语义)
	if again := setupHistoryFile(cfg); again != want {
		t.Errorf("重复调用结果不一致: %q", again)
	}
}

func TestMainStartup_AppendSelfReplaceEvent(t *testing.T) {
	dir := t.TempDir()
	appendSelfReplaceEvent(dir, map[string]string{"result": "ok", "backup": "forge.exe.bak_x"})

	path := filepath.Join(dir, ".forge", "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("未写出事件日志: %v", err)
	}
	var ev Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &ev); err != nil {
		t.Fatalf("事件不是合法 JSON: %v", err)
	}
	if ev.Type != EvSelfRestart {
		t.Errorf("事件类型 = %q 期望 %q", ev.Type, EvSelfRestart)
	}
	if ev.Ts == "" {
		t.Error("事件缺时间戳")
	}
	m, ok := ev.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data 类型异常: %T", ev.Data)
	}
	if m["result"] != "ok" {
		t.Errorf("Data 内容不符: %v", m)
	}

	// 追加语义: 第二次调用不得覆盖第一次
	appendSelfReplaceEvent(dir, map[string]string{"result": "failed"})
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data2)), "\n")
	if len(lines) != 2 {
		t.Fatalf("应为追加 2 行, 实际 %d", len(lines))
	}
}

func TestMainStartup_InstallSignalsWiring(t *testing.T) {
	// 不真注册信号处理器(会改变测试进程行为并可能 os.Exit), 改源码级结构断言。
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(wd, "main_startup.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"func installSignals(",
		"agent.CancelCurrent()", // 忙碌时首次 Ctrl+C 取消当前任务
		"agent.Shutdown()",      // 闲时退出必须显式存缓存(否则下次冷启动全量 miss)
	} {
		if !strings.Contains(s, want) {
			t.Errorf("installSignals 缺关键逻辑 %q", want)
		}
	}
	mainSrc, err := os.ReadFile(filepath.Join(wd, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSrc), "installSignals(") {
		t.Error("main.go 未调用 installSignals —— 信号处理器未接线")
	}
}

func TestMainStartup_RunSelfReplaceWiring(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	mainSrc, err := os.ReadFile(filepath.Join(wd, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSrc), "runSelfReplace()") {
		t.Error("main.go 未调用 runSelfReplace —— 自替换永不生效")
	}
	src, err := os.ReadFile(filepath.Join(wd, "main_startup.go"))
	if err != nil {
		t.Fatal(err)
	}
	// 就位必须用 rename, 不得先删后改(删成功+改名失败 = forge.exe 消失)
	s := string(src)
	if !strings.Contains(s, `os.Rename(newExe, oldExe)`) {
		t.Error("自替换就位未使用 os.Rename —— 先删后改会留下 forge.exe 消失窗口")
	}
	if strings.Contains(s, "os.Remove(oldExe)") {
		t.Error("自替换不得先删旧 exe")
	}
}
