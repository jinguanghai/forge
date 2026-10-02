//go:build windows

package main

// cov_gaps5_windows_test.go — 覆盖率缺口补测 第五批 (Windows 专属, 20261001)
//
// 覆盖控制台输入探针 (stdinHasInput / probeConsoleProcs 全路径) 与 GBK 兜底解码。
// 不消费真实键盘输入、不改控制台模式 (只读探针 + 原样回设)。

import (
	"testing"
)

// stdinHasInput: 探针未就绪必须直接返回 false; 就绪后在非控制台 stdin 上也不得 panic。
func TestCovGap5_StdinHasInput_Branches(t *testing.T) {
	old := consoleProcsOK
	t.Cleanup(func() { consoleProcsOK = old })

	consoleProcsOK = false
	if stdinHasInput() {
		t.Fatal("探针未就绪时必须返回 false (否则 readLine 会误判有输入而卡住)")
	}

	consoleProcsOK = true
	_ = stdinHasInput() // 测试进程 stdin 非控制台: 必须安全返回, 不得 panic
}

// probeConsoleProcs 全路径: 逐个解析 console LazyProc; 缺失时内部 recover 降级。
// 结论 (consoleProcsOK) 必须被写下, 二次调用走缓存。
func TestCovGap5_ProbeConsoleProcs_FullPath(t *testing.T) {
	old := consoleProcsOK
	t.Cleanup(func() { consoleProcsOK = old })

	consoleProcsOK = false
	probeConsoleProcs() // 不得 panic (内部 recover)
	probeConsoleProcs() // 二次调用: 走已探测缓存
}

// GBK 兜底: 多字节残留无法按 UTF-8 解码时, 必须尝试 GBK 并保留内容, 不得丢字。
func TestCovGap5_ConsumePending_GBKFallback(t *testing.T) {
	// GBK "你" = C4 E3; 凑满 UTFMax 字节才会走 GBK 分支
	gbk := []byte{0xC4, 0xE3, 0xC4, 0xE3}
	want, ok := gbkDecode(gbk)
	if !ok || len(want) == 0 {
		t.Skip("当前系统代码页非 GBK(936), 无法构造 GBK 残留用例")
	}

	ed := newDiscardEditor()
	pending := append([]byte(nil), gbk...)
	consumePending(ed, &pending, true)
	if len(pending) != 0 {
		t.Fatalf("GBK 序列应被整体消费, 残留 %q", pending)
	}
	if string(ed.buf) != string(want) {
		t.Fatalf("GBK 解码内容不符: got %q, want %q", string(ed.buf), string(want))
	}

	// 续字节开头 (非 RuneStart): 走另一条 GBK 兜底分支
	ed2 := newDiscardEditor()
	pending2 := []byte{0xA1, 0xA1}
	if rs, ok2 := gbkDecode(pending2); ok2 && len(rs) > 0 {
		consumePending(ed2, &pending2, true)
		if len(pending2) != 0 {
			t.Fatalf("续字节开头的 GBK 序列应被消费, 残留 %q", pending2)
		}
		if string(ed2.buf) != string(rs) {
			t.Fatalf("续字节 GBK 解码不符: got %q, want %q", string(ed2.buf), string(rs))
		}
	}
}
