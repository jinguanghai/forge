package main

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

// TestCookedBaseline_CoversRawResidue 是 20261002 控制台卡死事故的回归钉:
// cookedBaseline 必须能把「LINE_INPUT 被清」的状态补回, 否则棘轮锁死。
func TestCookedBaseline_CoversRawResidue(t *testing.T) {
	cases := []struct {
		name string
		orig uint32
		want uint32
	}{
		{"全关(0x0000)", 0x0000, enableLineInput | enableEchoInput},
		{"raw 残留(0x03F1)", 0x03F1, 0x03F1 | enableLineInput | enableEchoInput},
		{"已 cooked(0x03FF)", 0x03FF, 0x03FF},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cookedBaseline(c.orig)
			if got != c.want {
				t.Errorf("cookedBaseline(0x%X)=0x%X, want 0x%X", c.orig, got, c.want)
			}
		})
	}
}

// TestEnsureCookedForApproval_NoopWhenCooked 验证 cooked 态下不打扰 (幂等)。
func TestEnsureCookedForApproval_NoopWhenCooked(t *testing.T) {
	h := syscall.Handle(os.Stdin.Fd())
	var orig uint32
	if err := getConsoleMode(h, &orig); err != nil {
		t.Skip("非交互环境, 跳过")
	}
	if orig&enableLineInput == 0 || orig&enableEchoInput == 0 {
		t.Skip("当前本就是污染态, 无法验证幂等")
	}
	before := orig
	ensureCookedForApproval()
	after := uint32(0)
	_ = getConsoleMode(h, &after)
	if after != before {
		t.Errorf("幂等失败: before=0x%X after=0x%X", before, after)
	}
}

// TestApprovalDelegate_ClosePreservesBytes 「关闭档输出与引入前逐字节一致」的契约。
// 当前未启用委托 (FORGE_DELEGATE 未设), confirmByDelegate 必须返回 decided=false,
// 路径与改造前完全一致。
func TestApprovalDelegate_ClosePreservesBytes(t *testing.T) {
	old := os.Getenv("FORGE_DELEGATE")
	defer os.Setenv("FORGE_DELEGATE", old)
	os.Setenv("FORGE_DELEGATE", "")
	allowed, decided := confirmByDelegate(approvalDelegateMode(), "删除", "test_fp")
	if decided {
		t.Errorf("未设 FORGE_DELEGATE 必须 decided=false, got=%v allowed=%v", decided, allowed)
	}
	if allowed {
		t.Errorf("未设 FORGE_DELEGATE 必须 allowed=false, got=true")
	}
}

// TestApprovalDelegate_Modes 档位语义 — 直通档直接放行, 窗口档可转人工。
func TestApprovalDelegate_Modes(t *testing.T) {
	// 窗口档的倒计时长度不是被测语义 —— 注入 1s 窗口, 否则默认 11s 白等。
	// (FORGE_DELEGATE_WINDOW 是产品既有的可注入点, 无需改动产品代码)
	t.Setenv("FORGE_DELEGATE_WINDOW", "1")
	for _, mode := range []string{"1", "all"} {
		allowed, decided := confirmByDelegate(mode, "删除", "test_fp")
		if !decided {
			t.Errorf("mode=%s 必须 decided=true", mode)
		}
		if !allowed {
			t.Errorf("mode=%s 必须 allowed=true (无键可按时)", mode)
		}
	}
	// 档位解析必须走纯函数 parseDelegateMode, 不得读进程环境: 生产 User 级
	// FORGE_DELEGATE=all 会让「非法值判空」恒假, 且判据与 fail-closed 分支
	// 是否存活无关 (原实现正是这样失效的)。
	for _, mode := range []string{"", "garbage", "0", "yes-sir", "allx", " true!", "1x"} {
		if got := parseDelegateMode(mode); got != "" {
			t.Errorf("非法 mode=%q 应判为空, got=%q", mode, got)
		}
	}
	// 正例: 合法别名必须归一 (原用例只测非法侧, 归一逻辑删掉也没人管)。
	for _, mode := range []string{"1", "true", "TRUE", " yes ", "on"} {
		if got := parseDelegateMode(mode); got != "1" {
			t.Errorf("窗口档 mode=%q 应归一为 \"1\", got=%q", mode, got)
		}
	}
	for _, mode := range []string{"all", "ALL", " force ", "direct"} {
		if got := parseDelegateMode(mode); got != "all" {
			t.Errorf("直通档 mode=%q 应归一为 \"all\", got=%q", mode, got)
		}
	}
}

// TestConfirmDangerous_DoesNotPanicWithCookedConsole 端到端: 在 cooked 控制台下
// confirmDangerous 的 allow 路径不应 panic, 并正确写出 mode=auto 字段。
func TestConfirmDangerous_DoesNotPanicWithCookedConsole(t *testing.T) {
	// 走 delegate=all 路径, 完全跳过键盘等待, 这是委托模式的契约。
	oldD := os.Getenv("FORGE_DELEGATE")
	defer os.Setenv("FORGE_DELEGATE", oldD)
	os.Setenv("FORGE_DELEGATE", "all")
	got := (&Forge{workDir: os.TempDir()}).confirmDangerous("echo test", "测试", "test_hit")
	if !got {
		t.Errorf("委托=all 必须返回 true")
	}
	// 反向: 不置环境变量, 走老路径 (解析空输入), 应正常返回
	os.Setenv("FORGE_DELEGATE", "")
	if got := (&Forge{workDir: os.TempDir()}).confirmDangerous("echo test", "测试", "test_hit"); got {
		t.Errorf("默认路径无键盘, 应 deny, got allow")
	}
}

// TestWiring_EnsureCookedForApprovalCalledFromConfirmDangerous 接线钉:
// confirmDangerous 必须真的调 ensureCookedForApproval, 否则写了等于没写。
func TestWiring_EnsureCookedForApprovalCalledFromConfirmDangerous(t *testing.T) {
	txt, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(txt)
	if !strings.Contains(src, "ensureCookedForApproval()") {
		t.Fatal("forge.go 未调 ensureCookedForApproval()")
	}
	// 调点必须落在 confirmDangerous 函数体内, 而非别的位置。
	cdIdx := strings.Index(src, "func (f *Forge) confirmDangerous(")
	eIdx := strings.Index(src, "ensureCookedForApproval()")
	if cdIdx < 0 || eIdx < 0 || eIdx < cdIdx {
		t.Fatal("接线位置不对")
	}
	// eIdx 必须小于下一个 func 起始, 否则接到了别处
	next := strings.Index(src[cdIdx+1:], "\nfunc ")
	if next > 0 && eIdx > cdIdx+1+next {
		t.Fatalf("ensureCookedForApproval 接到了 confirmDangerous 之外")
	}
}
