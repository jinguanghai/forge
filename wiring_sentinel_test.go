package main

// wiring_sentinel_test.go — 接线哨兵 (死程序判定)
//
// 事故背景(20260910): checkDangerousTarget 函数正确、guard_target_test.go
// 7正7反全绿, 但生产代码从未调用它 → 第二道防线(受保护目标×破坏谓词)
// 形同虚设。"测试全绿"给了假的安心。
// 教训: 验收必须查调用点, 不能只查函数行为。本文件把该判定固化为死程序。

import (
	"os"
	"strings"
	"testing"
)

// wiringRequirements 关键功能函数 → 必须被调用的生产文件
var wiringRequirements = []struct {
	fn     string
	inFile string
}{
	{"checkDangerousTarget", "forge.go"}, // 第二道防线: 受保护目标×破坏谓词
	{"goalAnchorRef", "agent.go"},        // 大工具输出落盘引用
	{"storeToolOutputRef", "tool_ref.go"},
	{"pruneToolOutput", "agent_pure.go"},
}

func TestWiringSentinels(t *testing.T) {
	for _, req := range wiringRequirements {
		src, err := os.ReadFile(req.inFile)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", req.inFile, err)
		}
		if !strings.Contains(string(src), req.fn+"(") {
			t.Errorf("未接线: %s 中未见 %s() 调用 — 功能写了但没接上", req.inFile, req.fn)
		}
	}
}

// TestWiringCtrlCExitSavesCache 闲时 Ctrl+C 必须走 Shutdown 保存缓存。
func TestWiringCtrlCExitSavesCache(t *testing.T) {
	src, err := os.ReadFile("main_startup.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	idx := strings.Index(s, "!agentBusy.Load() && sig == os.Interrupt")
	if idx < 0 {
		t.Fatal("未找到闲时退出分支")
	}
	tail := s[idx:]
	end := strings.Index(tail, "os.Exit(0)")
	if end < 0 {
		t.Fatal("未找到 os.Exit(0)")
	}
	if !strings.Contains(tail[:end], "agent.Shutdown()") {
		t.Error("闲时 Ctrl+C 退出未调用 agent.Shutdown() — os.Exit 会跳过 defer, 缓存不落盘")
	}
}

// TestWiringNoByteTruncationOnOutput 工具输出展示不得按字节截断(中文/emoji 会切碎)。
func TestWiringNoByteTruncationOnOutput(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "ol = ol[:120]") {
		t.Error("仍按字节截断工具输出行 — 应改 []rune 截断")
	}
}
