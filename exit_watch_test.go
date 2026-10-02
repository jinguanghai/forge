package main

// exit_watch_test.go — 非正常退出检测的判据钉。
// 纪律: 正常收尾必须完全静默(否则又是告警贬值), 异常必须报出来。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAbnormalExitNotice_SilentWhenClean(t *testing.T) {
	// 无记录 → 静默(首次启动 / 账本被归档)
	if got := abnormalExitNotice(Event{}, false); got != "" {
		t.Errorf("无记录必须静默, got=%q", got)
	}
	// 正常收尾 → 静默
	if got := abnormalExitNotice(Event{Type: EvShutdown, Ts: "2026-10-02T18:00:00+08:00"}, true); got != "" {
		t.Errorf("正常收尾必须静默, got=%q", got)
	}
	// 异常收尾 → 必须报, 且带最后事件与时间
	got := abnormalExitNotice(Event{Type: EvApproved, Ts: "2026-10-02T15:17:02+08:00"}, true)
	if !strings.Contains(got, "未正常收尾") {
		t.Errorf("异常收尾必须报出, got=%q", got)
	}
	if !strings.Contains(got, EvApproved) || !strings.Contains(got, "15:17:02") {
		t.Errorf("横幅应含最后事件与时间, got=%q", got)
	}
	// 空时间不得 panic, 也不得漏报
	if got := abnormalExitNotice(Event{Type: EvToolResult}, true); !strings.Contains(got, "时间未知") {
		t.Errorf("缺时间时应标注时间未知, got=%q", got)
	}
}

func TestLastEventOf_ReadsTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	// 灌 >8KB 填充, 让最后一条落在尾部窗口外/内的边界上, 判据必须仍取到最后一条。
	pad := strings.Repeat(`{"ts":"2026-10-02T00:00:00+08:00","type":"tool_result","detail":"pad"}`+"\n", 200)
	tail := `{"ts":"2026-10-02T15:17:02+08:00","type":"approved","detail":"自杀"}` + "\n"
	if err := os.WriteFile(path, []byte(pad+tail), 0644); err != nil {
		t.Fatal(err)
	}
	ev, ok := lastEventOf(path)
	if !ok {
		t.Fatal("应能取到尾部最后一条记录")
	}
	if ev.Type != EvApproved {
		t.Errorf("应取到 approved, got=%q", ev.Type)
	}
	if _, ok := lastEventOf(filepath.Join(dir, "nope.jsonl")); ok {
		t.Error("文件不存在时必须返回 ok=false")
	}
	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastEventOf(empty); ok {
		t.Error("空文件必须返回 ok=false (不得误报异常)")
	}
}

// TestWiring_ExitWatchIsWired 接线钉: 写了没接 = 等于没写(本项目先例)。
func TestWiring_ExitWatchIsWired(t *testing.T) {
	agentSrc, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agentSrc), "markCleanExit()") {
		t.Error("agent.Shutdown 未写 shutdown 留痕 —— 检测会永远报「上次未正常收尾」(狼来了)")
	}
	startSrc, err := os.ReadFile("main_startup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(startSrc)
	if !strings.Contains(s, "reportLastExit(cfg.WorkDir)") {
		t.Fatal("启动序未调用 reportLastExit —— 检测写了等于没写")
	}
	// 顺序钉: 必须紧跟 initEventLog, 早于本次进程写的任何事件, 否则判据自毁。
	i1 := strings.Index(s, "initEventLog(cfg.WorkDir)")
	i2 := strings.Index(s, "reportLastExit(cfg.WorkDir)")
	i3 := strings.Index(s, "reportLastUpgrade(cfg.WorkDir)")
	if i1 < 0 || i2 < 0 || !(i1 < i2) {
		t.Fatal("reportLastExit 必须在 initEventLog 之后")
	}
	if i3 > 0 && i2 > i3 {
		t.Fatal("reportLastExit 必须在任何可能写事件的动作之前")
	}
}
