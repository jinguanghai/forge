package main

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Linux 行编辑平台层的结构哨兵 (T22 第 3 层)。
//
// 为什么需要: 第 3 层的产出(termios raw 模式 + ANSI 相对重绘 + 终端恢复兜底)
// 本机无法端到端验证(无 WSL/docker)。可判定的部分必须交给死程序 ——
// 「文件在不在 / tag 对不对 / 兜底链完不完整 / 有没有引第三方」四类结构事实,
// 一旦被后人改掉, Windows 侧全量测试依旧全绿(它们看不见 Linux)。
//
// 运行期行为由 readline_render_test.go 的 ANSI 模拟器覆盖(跨平台可跑);
// 交叉编译由 linux_gate_sentinel_test.go 覆盖。

func TestLinuxReadlinePlatformFilesExist(t *testing.T) {
	want := []string{
		"term_linux.go",            // termios/ioctl 基础 + cooked 自检
		"readline_linux.go",        // raw 模式 + readLine 主循环
		"readline_editor_linux.go", // ANSI 相对重绘平台层
	}
	for _, name := range want {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("缺 Linux 平台文件 %s: %v", name, err)
			continue
		}
		if !strings.HasPrefix(string(b), "//go:build linux") {
			t.Errorf("%s 首行必须是 //go:build linux (否则与 Windows 版重复定义)", name)
		}
	}
}

func TestLinuxReadlineTerminalRestoreChain(t *testing.T) {
	// 终端恢复三重兜底 —— 对应 Windows 20261002「控制台卡死」事故的 Linux 版:
	//   ① defer 恢复  ② 信号兜底  ③ 审批前自检补回 cooked
	b, err := os.ReadFile("readline_linux.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"restoreTermios(fd, old)", "signal.Notify", "defer func()"} {
		if !strings.Contains(s, want) {
			t.Errorf("readline_linux.go 缺终端恢复兜底: %q", want)
		}
	}
	g, err := os.ReadFile("term_linux.go")
	if err != nil {
		t.Fatal(err)
	}
	gs := string(g)
	if !strings.Contains(gs, "func ensureCookedForApproval()") {
		t.Error("term_linux.go 必须实现 ensureCookedForApproval")
	}
	if !strings.Contains(gs, "syscall.ICANON") || !strings.Contains(gs, "syscall.ECHO") {
		t.Error("ensureCookedForApproval 必须补回 ICANON|ECHO (raw 残留的棘轮解药)")
	}
}

func TestReadlineLogicLayerIsPortable(t *testing.T) {
	// 逻辑层必须双平台共享: 一旦有人给它加 tag, Linux 侧就只剩平台层壳子,
	// 而且那些逻辑再也不能在本机被测试(判据静默失效)。
	for _, name := range []string{"readline_editor.go", "readline_session.go", "readline_common.go", "readline_render.go"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("读 %s: %v", name, err)
			continue
		}
		if strings.Contains(string(b), "//go:build") {
			t.Errorf("%s 带 build tag —— 行编辑逻辑层必须双平台共享", name)
		}
	}
}

func TestZeroThirdPartyDependency(t *testing.T) {
	// 「零第三方依赖」是硬约束: 补平行编辑器本需 readline/liner 库(T22 第 3 层原判),
	// 这里自实现(标准库 syscall 的 termios/ioctl) —— 由判据钉住, 防后人顺手引库。
	files := []string{
		"term_linux.go", "readline_linux.go", "readline_editor_linux.go",
		"readline_render.go", "readline_common.go", "readline_editor.go",
		"readline_session.go", "readline_windows.go",
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Errorf("读 %s: %v", name, err)
			continue
		}
		_ = b
		// 用 AST 解析 import 声明, 而不是文本扫描 —— 注释里提到 "golang.org/x/sys"
		// 是解释「为何不用它」, 不是依赖。文本扫描会把说明文字判成依赖(误报),
		// 误报的判据最终会被关掉, 等于没有判据。
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("解析 %s 的 import: %v", name, err)
			continue
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.Contains(p, ".") { // 标准库 import 路径不含点
				t.Errorf("%s 引入第三方依赖 %q —— 违反零依赖硬约束", name, p)
			}
		}
	}
	gm, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gm), "require") {
		t.Error("go.mod 出现 require —— 铸剑炉零第三方依赖硬约束被破")
	}
}
