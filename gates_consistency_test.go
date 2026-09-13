package main

// gates_consistency_test.go — gate 清单/表/展示层一致性 + 判定函数覆盖 (20260913)
//
// 背景: gate 清单原有多份独立副本 (gatesync.go currentGates / memory_health.go
// 体检局部 cur / forge.go 铸剑炉_COMPILERS 的 key / main_commands.go /tools 展示名)。
// 已统一为唯一源 铸剑炉_GATES; 本文件把"表与清单不得脱节"固化为死程序判定。
// 另补齐若干 0% 覆盖的确定性判定函数 (compilerTimeout / shEchoStaticPattern /
// looksLikeValidGoTopLevel / 两个时段入口包装)。

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// 哨兵: 铸剑炉_COMPILERS 的 key 集合必须与唯一源 铸剑炉_GATES 完全一致。
// 脱节后果: 新 gate 无超时/执行命令配置 → 静默用默认 30s; 或表里留幽灵 gate。
func TestGatesList_MatchesCompilerTable(t *testing.T) {
	list := map[string]bool{}
	for _, g := range 铸剑炉_GATES {
		if list[g] {
			t.Fatalf("铸剑炉_GATES 有重复项: %s", g)
		}
		list[g] = true
		if _, ok := 铸剑炉_COMPILERS[g]; !ok {
			t.Errorf("清单里的 %s 在 铸剑炉_COMPILERS 表中缺失 (无超时/执行配置)", g)
		}
	}
	for k := range 铸剑炉_COMPILERS {
		if !list[k] {
			t.Errorf("表里有 %s 但不在 铸剑炉_GATES 清单中 (幽灵 gate)", k)
		}
	}
	if len(铸剑炉_GATES) != 12 {
		t.Errorf("当前应为 12 面 gate, 实际 %d", len(铸剑炉_GATES))
	}
}

// 哨兵: /tools 展示层与清单同集合 (展示名可带别名, 如 sh/bash)。
func TestGateDisplayNames_MatchGatesList(t *testing.T) {
	shown := map[string]bool{}
	for _, d := range gateDisplayNames {
		base := strings.Split(d, "/")[0]
		shown[base] = true
	}
	for _, g := range 铸剑炉_GATES {
		if !shown[g] {
			t.Errorf("/tools 展示缺 gate: %s", g)
		}
	}
	for b := range shown {
		found := false
		for _, g := range 铸剑炉_GATES {
			if g == b {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/tools 展示了不存在的 gate: %s", b)
		}
	}
	if len(gateDisplayNames) != len(铸剑炉_GATES) {
		t.Errorf("展示项数 %d 与清单 %d 不一致", len(gateDisplayNames), len(铸剑炉_GATES))
	}
}

func TestCompilerTimeout(t *testing.T) {
	for _, g := range 铸剑炉_GATES {
		want := 30 * time.Second
		if c, ok := 铸剑炉_COMPILERS[g]; ok && c.ExecTimeout > 0 {
			want = c.ExecTimeout
		}
		if got := compilerTimeout(g); got != want {
			t.Errorf("compilerTimeout(%s) = %v, want %v", g, got, want)
		}
		if compilerTimeout(g) <= 0 {
			t.Errorf("compilerTimeout(%s) 必须为正", g)
		}
	}
	if got := compilerTimeout("no_such_gate"); got != 30*time.Second {
		t.Errorf("未知语言应回退 30s, 实际 %v", got)
	}
}

func TestExeSuffix(t *testing.T) {
	got := exeSuffix()
	if got != ".exe" && got != "" {
		t.Fatalf("exeSuffix 只能是 .exe 或空串, 实际 %q", got)
	}
	if runtime.GOOS == "windows" && got != ".exe" {
		t.Fatalf("Windows 上应为 .exe, 实际 %q", got)
	}
}

func TestShEchoStaticPattern(t *testing.T) {
	pos := []struct{ in, want string }{
		{"echo hello", "hello\n"},
		{"ECHO Hi", "Hi\n"},
		{"echo 你好世界", "你好世界\n"},
		{"echo a b c", "a b c\n"},
		{"  echo padded  ", "padded\n"},
	}
	for _, c := range pos {
		got, ok := shEchoStaticPattern(c.in)
		if !ok || got != c.want {
			t.Errorf("shEchoStaticPattern(%q) = (%q,%v), want (%q,true)", c.in, got, ok, c.want)
		}
	}
	neg := []string{
		"echo %PATH%", "echo a&b", "echo a|b", "echo a>b", "echo a<b",
		"echo a^b", `echo "quoted"`, "echo a!b",
		"echo line1\necho line2", "echo /?", "echo", "printf hi", "ls",
		"", "   ",
	}
	for _, c := range neg {
		if got, ok := shEchoStaticPattern(c); ok {
			t.Errorf("shEchoStaticPattern(%q) 应拒绝, 实际 (%q,true)", c, got)
		}
	}
}

func TestLooksLikeValidGoTopLevel(t *testing.T) {
	pos := []string{
		"func main() {}",
		"type T struct{}",
		"var x = 1",
		"const A = 1",
		`import "fmt"`,
		"package main",
		"// 注释开头",
		"/* 块注释 */",
		"  func f() {}",
	}
	for _, c := range pos {
		if !looksLikeValidGoTopLevel(c) {
			t.Errorf("looksLikeValidGoTopLevel(%q) 应为 true", c)
		}
	}
	neg := []string{"", "   ", "fmt.Println(1)", "x := 1", "for i := 0; i < 3; i++ {}"}
	for _, c := range neg {
		if looksLikeValidGoTopLevel(c) {
			t.Errorf("looksLikeValidGoTopLevel(%q) 应为 false", c)
		}
	}
}

// 入口包装必须与纯函数同判据 —— 包装写错时纯函数测试仍全绿, 生产行为却错。
func TestPeakHourEntry_MatchesPureFunc(t *testing.T) {
	if isPeakHour() != isPeakHourAt(time.Now()) {
		if isPeakHour() != isPeakHourAt(time.Now()) { // 容忍跨小时边界
			t.Fatal("isPeakHour() 与 isPeakHourAt(now) 不一致 (入口包装写错?)")
		}
	}
}

func TestMiniMaxWindowEntry_MatchesPureFunc(t *testing.T) {
	if minimaxWindow() != isMiniMaxWindow(time.Now()) {
		if minimaxWindow() != isMiniMaxWindow(time.Now()) {
			t.Fatal("minimaxWindow() 与 isMiniMaxWindow(now) 不一致 (入口包装写错?)")
		}
	}
}
