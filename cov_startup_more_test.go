package main

// cov_startup_more_test.go — 启动期工具函数补测 (flag 解析/自替换事件/历史文件)
// 边界: 不触发 --help/--version 分支 (其内部 os.Exit 会终止测试进程)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCovStartup_ParseFlags(t *testing.T) {
	orig := os.Args
	t.Cleanup(func() { os.Args = orig })
	t.Setenv("FORGE_SHOW_REASONING", "")

	cases := []struct {
		args []string
		want bool
		n    int
	}{
		{[]string{"forge.exe"}, true, 0},
		{[]string{"forge.exe", "--reasoning"}, true, 0},
		{[]string{"forge.exe", "-r"}, true, 0},
		{[]string{"forge.exe", "--no-reasoning"}, false, 0},
		{[]string{"forge.exe", "-nr"}, false, 0},
		{[]string{"forge.exe", "列出文件"}, true, 1},
		{[]string{"forge.exe", "列出文件", "-r"}, true, 2},
	}
	for i, c := range cases {
		os.Args = c.args
		got, nonFlag := parseFlags()
		if got != c.want {
			t.Errorf("用例%d: showReasoning=%v, want %v", i, got, c.want)
		}
		if len(nonFlag) != c.n {
			t.Errorf("用例%d: 非 flag 参数 %v, want 数量 %d", i, nonFlag, c.n)
		}
	}
}

func TestCovStartup_AppendSelfReplaceEvent(t *testing.T) {
	wd := t.TempDir()
	appendSelfReplaceEvent(wd, map[string]string{"stage": "cov"})
	p := filepath.Join(wd, ".forge", "events.jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("events.jsonl 未写入: %v", err)
	}
	var ev Event
	if err := json.Unmarshal(b[:len(b)-1], &ev); err != nil {
		t.Fatalf("事件 JSON 非法: %v (%s)", err, string(b))
	}
	if ev.Type != EvSelfRestart || ev.Detail != "startup-self-replace" {
		t.Errorf("事件字段不符: %+v", ev)
	}
	appendSelfReplaceEvent(wd, map[string]string{"stage": "cov2"})
	b2, _ := os.ReadFile(p)
	if len(b2) <= len(b) {
		t.Error("追加写入未生效")
	}
	// 不可写路径: 必须静默失败不 panic
	appendSelfReplaceEvent(filepath.Join(wd, "nope\x00bad"), map[string]string{"a": "b"})
}

func TestCovStartup_SetupHistoryFile(t *testing.T) {
	wd := t.TempDir()
	cfg := &Config{WorkDir: wd}
	p := setupHistoryFile(cfg)
	if p != filepath.Join(wd, ".forge", "history") {
		t.Errorf("历史文件路径不符: %s", p)
	}
	if _, err := os.Stat(filepath.Join(wd, ".forge")); err != nil {
		t.Errorf(".forge 目录未创建: %v", err)
	}
}
