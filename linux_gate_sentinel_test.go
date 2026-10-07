package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLinuxTestBinaryCompiles 钉住 T22 第 1 层「Linux 门敞开」这个事实。
//
// 背景: 2026-10-07 实测 Linux 交叉编译, 生产二进制一次通过, 但测试二进制挂在
// 两个无 build tag 的测试文件上 —— cookguard_sentinel_test.go (consoleProcsOK 等
// 5 个 Windows 符号) 与 status_bar_reserve_test.go (consoleProcsOK)。
// 二者都已按平台拆分 (加 tag / 拆文件), 修法本身没有任何判据保护:
// 下一个人把 syscall.Handle / consoleProcsOK 写进无 tag 文件, 门又悄悄关上,
// 而 Windows 侧全量测试依旧全绿 (它们根本看不见 Linux)。
//
// 判据 = 死程序: GOOS=linux GOARCH=amd64 go test -c 必须 exit 0。
// 与「声称 Linux 可编译」相比, 这是唯一不可自欺的形态。
//
// 成本: 约 2~5s (构建缓存命中后更快)。quick 档 (-short) 跳过, full 档执行。
// 关闸: FORGE_LINUX_SENTINEL=0。
func TestLinuxTestBinaryCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("quick 档跳过 Linux 交叉编译 (~5s), full 档执行")
	}
	if os.Getenv("FORGE_LINUX_SENTINEL") == "0" {
		t.Skip("FORGE_LINUX_SENTINEL=0")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH, 无法交叉编译: %v", err)
	}
	out := filepath.Join(t.TempDir(), "forge_linux_test_probe")
	cmd := exec.Command("go", "test", "-c", "-o", out, ".")
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("GOOS=linux go test -c 失败 —— Linux 门已关 (多半是无 tag 文件引用了 Windows 专属符号; 修法: 加 //go:build windows 或拆到 _windows_test.go):\n%v\n%s", err, b)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("交叉编译产物缺失或为空: err=%v", err)
	}
	// ELF 魔数校验: 产物必须是 Linux 可执行文件, 不是被静默替换成 Windows PE。
	f, err := os.Open(out)
	if err != nil {
		t.Fatalf("读取产物失败: %v", err)
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		t.Fatalf("读取魔数失败: %v", err)
	}
	if string(magic) != "\x7fELF" {
		t.Fatalf("产物魔数 %q 不是 ELF —— 交叉编译未真正生效", magic)
	}
	t.Logf("Linux 测试二进制 OK: %d 字节, 魔数 ELF", st.Size())
}
