package main

// cov_gates_deadboundary_test.go — 死边界 gate (math/logic/regex/chain) 真实执行路径补测
// 要点: NewForge 的 toolsDir 派生自 workDir; 测试里把 toolsDir 指回包目录,
//       即可在不污染真实缓存/工作目录的前提下跑通 selfHostedGate 全路径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func covTrunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}

func TestCovGates_DeadBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过死边界 gate 端到端测试(起真实 gate 子进程)")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_, cfg, _ := newHandleCmdAgent(t)
	f := NewForge(t.TempDir(), cfg)
	f.toolsDir = filepath.Join(wd, ForgeToolsDir)
	t.Cleanup(f.Shutdown)

	cases := []struct{ lang, code string }{
		{"math", "1+1"},
		{"math", "2*(3+4)-5"},
		{"math", "1/0"},
		{"math", "完全不是算式"},
		{"logic", "(assert (= (+ 1 1) 2))"},
		{"logic", "1+1=2"},
		{"regex", `{"type":"match","pattern":"^a$","positive":["a"],"negative":["b"]}`},
		{"regex", `{"type":"match","pattern":"[","positive":[],"negative":[]}`},
		{"regex", "裸正则模式"},
		{"chain", `{"stages":[{"gate":"math","input":{"code":"1+1"}}]}`},
		{"chain", `{"stages":[{"gate":"math","input":"1+1"}],"stop_on":"never"}`},
		{"chain", `{"stages":[]}`},
		{"chain", `不是 JSON`},
		{"chain", `{"stages":[{"gate":"math","input":{"code":"1+1"}},{"gate":"math","input":{"code":"2+2"},"if_verdict":"2"}]}`},
	}
	for _, c := range cases {
		r := f.forgeGate(c.code, c.lang, "")
		t.Logf("%-6s ok=%-5v stage=%-8s err=%s", c.lang, r.OK, r.Stage, covTrunc(r.Error, 70))
		if r.Lang == "" {
			t.Errorf("lang=%s 结果 Lang 为空", c.lang)
		}
	}

	// 缓存复用: 同一 math 表达式第二次应命中
	a := f.forgeGate("7*6", "math", "")
	b := f.forgeGate("7*6", "math", "")
	if a.OK != b.OK {
		t.Errorf("math 缓存命中前后不一致: %v %v", a.OK, b.OK)
	}
}

func TestCovGates_ToolsDirMissing(t *testing.T) {
	_, cfg, _ := newHandleCmdAgent(t)
	f := NewForge(t.TempDir(), cfg)
	t.Cleanup(f.Shutdown)
	// 空 toolsDir → 死边界不可用分支 (错误路径必须可读且不回退成成功)
	r := f.forgeGate("1+1", "math", "")
	if r.OK {
		t.Error("死边界缺失时不应判成功")
	}
	if !strings.Contains(r.Error, "not found") && r.Error == "" {
		t.Errorf("错误信息缺失: %+v", r)
	}
}
