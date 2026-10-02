package main

// relation_gate_test.go — 接线哨兵 + 端到端 (20260925)
//
// 由来: 796feb2 提交信息声称"style.go 与 agent.go 均有调用", 实际调用点从未入库,
// toolstream.go 当 12 天死代码。教训: 调用点存在性只能由死程序判定。
// 本文件对 relation gate 自身施加同一判据 —— 新增 gate 必须证明"真的接上了"。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRelationGateWired 静态哨兵: 调用点 / 注册表 / 清单 / 脚本 四处必须同时存在。
func TestRelationGateWired(t *testing.T) {
	s := prodGoSources(t)
	if !strings.Contains(s, `case "relation":`) {
		t.Fatal(`全包缺 case "relation": —— relation gate 未接线`)
	}
	if !strings.Contains(s, `selfHostedGate("relation_gate"`) {
		t.Fatal(`未调用 selfHostedGate("relation_gate", ...) —— 分派缺失`)
	}
	foundReg, foundList := false, false
	for _, g := range gateNames() {
		if g == "relation" {
			foundReg = true
		}
	}
	for _, g := range 铸剑炉_GATES {
		if g == "relation" {
			foundList = true
		}
	}
	if !foundReg {
		t.Fatal("gateRegistry 缺 relation 注册")
	}
	if !foundList {
		t.Fatal("铸剑炉_GATES 缺 relation (超时/展示配置将落默认)")
	}
	if _, ok := 铸剑炉_COMPILERS["relation"]; !ok {
		t.Fatal("铸剑炉_COMPILERS 缺 relation (无超时配置)")
	}
	script := filepath.Join(ForgeToolsDir, "relation_gate.py")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("relation_gate.py 不存在: %v", err)
	}
}

// TestRelationGateScriptE2E 端到端: 脚本必须真能跑出合法 JSON 判定。
func TestRelationGateScriptE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 relation gate 脚本端到端测试")
	}
	script := filepath.Join(ForgeToolsDir, "relation_gate.py")
	if _, err := os.Stat(script); err != nil {
		t.Skip("脚本不存在")
	}
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python 不在 PATH")
	}
	cases := []struct {
		in        string
		wantOK    bool
		wantInOut string
	}{
		{`{"type":"symbol","name":"newCmd"}`, true, "wired"},
		{`{"type":"symbol","name":"no_such_symbol_xyz"}`, false, "符号不存在"},
		{`{"type":"scan"}`, true, "缺口"},
	}
	for _, c := range cases {
		// 判定为"拦截"时脚本以退出码 1 结束 —— 这是契约的一部分, 不是错误
		cmd := exec.Command("python", script, c.in)
		// 判据断言中文 verdict, 必须显式强制子进程 UTF-8 输出:
		// Windows 下 python 默认按 ANSI(GBK) 写 stdout, 剥离 PYTHONUTF8 的环境
		// (计划任务 / 裸 go test) 会读到 GBK 字节 -> 断言假红 (20261002 实测复现)。
		cmd.Env = pythonUTF8Env()
		out, _ := cmd.CombinedOutput()
		if !json.Valid(out) {
			t.Fatalf("输出非合法 JSON (%s): %s", c.in, string(out))
		}
		var d map[string]interface{}
		if err := json.Unmarshal(out, &d); err != nil {
			t.Fatal(err)
		}
		ok, _ := d["ok"].(bool)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v want %v (%v)", c.in, ok, c.wantOK, d["verdict"])
		}
		if v, _ := d["verdict"].(string); !strings.Contains(v, c.wantInOut) {
			t.Errorf("%s: verdict=%q 应含 %q", c.in, v, c.wantInOut)
		}
	}
}

// TestRelationGateProductionPath 真·接线验证: 走生产入口 forgeGate 调 relation gate。
// (脚本放错目录 / 分派缺失 / 超时配置缺失 都会在此暴露 —— 只有走生产入口才算接通)
func TestRelationGateProductionPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.WorkDir = wd
	f := NewForge(wd, cfg)
	defer f.Shutdown()
	r := f.forgeGateSkipCache(`{"type":"symbol","name":"newCmd"}`, "relation", "", true)
	if !r.OK {
		t.Fatalf("relation gate 生产路径失败: %s", r.Error)
	}
	var d1 map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &d1); err != nil {
		t.Fatalf("判定输出非法 JSON: %v / %s", err, r.Stdout)
	}
	if v, _ := d1["verdict"].(string); v != "wired" {
		t.Fatalf("newCmd 应判 wired, 实际 %q", v)
	}
	// 拦截路径: 断言一个不存在的接线关系必须判负。
	// 注意两层 ok 的语义区别 —— ForgeGateResult.OK 是"gate 执行成功",
	// 判定结果在 stdout JSON 的 ok 字段 (脚本退出码恒 0, 见脚本契约注释)。
	r2 := f.forgeGateSkipCache(`{"type":"assert","claim":"style.go 调用了 stripDynamicMemory"}`, "relation", "", true)
	if !r2.OK {
		t.Fatalf("gate 执行本身应成功: %s", r2.Error)
	}
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(r2.Stdout), &d); err != nil {
		t.Fatalf("判定输出非法 JSON: %v / %s", err, r2.Stdout)
	}
	if ok, _ := d["ok"].(bool); ok {
		t.Fatalf("假接线断言不应通过: %s", r2.Stdout)
	}
	if v, _ := d["verdict"].(string); !strings.Contains(v, "拦截") {
		t.Fatalf("应输出拦截判定: %s", r2.Stdout)
	}
}
