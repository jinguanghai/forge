package main

// test_channel_sentinel_test.go — 测试运行通道哨兵 (20261001)
//
// 判据来自实测, 不是推断。go test 在 stdout 不是终端时对整包输出做缓冲,
// 包没结束就不落盘; 进程被杀(超时/中断) → 输出全丢, 只剩一个退出码,
// 失败原因完全不可见。同一条件下编译成测试二进制直接运行则逐条写出:
//
//	[实测] go test -short + 9s kill          -> 日志 0 字节
//	[实测] go test -c + 直接运行 + 9s kill   -> 日志 21630 字节 / 539 行
//
// 所以测试脚本必须"先 go test -c 编译, 再直接运行测试二进制"。
// 本哨兵把这条方法学钉在脚本上, 防止有人图省事改回裸 go test。

import (
	"os"
	"strings"
	"testing"
)

func TestTestScriptsUseCompiledBinary(t *testing.T) {
	scripts := []string{"full_test.cmd", "quick_test.cmd"}
	for _, name := range scripts {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatalf("读不到测试脚本 %s (cwd 应为仓库根): %v", name, err)
			}
			s := string(b)
			if !strings.Contains(s, "go test -c") {
				t.Error("必须用 go test -c 编译测试二进制: 裸 go test 的输出在被杀时全丢")
			}
			if strings.Contains(s, "go test -count") {
				t.Error("不得直接跑 go test -count ...: 输出被缓冲, 超时/中断即全丢")
			}
			if !strings.Contains(s, "-test.timeout=") {
				t.Error("直接运行测试二进制时必须显式给 -test.timeout= (默认 10m 会误杀长通道)")
			}
		})
	}
}
