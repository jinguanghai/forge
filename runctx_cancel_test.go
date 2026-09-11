package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 验证 Ctrl+C 中断修复: 第一次取消 runCtx 必须能真正中断正在执行的工具。
//
// 修复前: Build 内部一律用 f.ctx 派生超时 ctx, 取消 runCtx 对工具执行与重试循环完全无效
//
//	→ 工具会跑到自身超时(可达 30s), 用户体感"卡死", 只能按第二次 Ctrl+C 退出程序。
//
// 修复后: Build 走 effCtx()(优先 runCtx) → 取消立即生效。
func TestBuild_RunCtxCancel_InterruptsRunningTool(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.SetRunCtx(ctx)
	defer f.SetRunCtx(nil)

	type outcome struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan outcome, 1)
	go func() {
		t0 := time.Now()
		// 必然长于取消时机的任务: 睡 30 秒 (若中断失效, 测试会在 15s 兜底处失败)
		_, _, err := f.Build("import time\ntime.sleep(30)\nprint('SHOULD_NEVER_PRINT')", "python", "")
		done <- outcome{err, time.Since(t0)}
	}()

	time.Sleep(1500 * time.Millisecond) // 等工具真正开始跑
	cancelAt := time.Now()
	cancel()

	select {
	case o := <-done:
		after := time.Since(cancelAt)
		if after > 10*time.Second {
			t.Fatalf("取消后仍耗时 %.1fs 才返回 —— runCtx 未生效", after.Seconds())
		}
		t.Logf("OK: 取消后 %.2fs 返回 (工具总耗时 %.2fs), err=%v",
			after.Seconds(), o.elapsed.Seconds(), o.err)
	case <-time.After(15 * time.Second):
		t.Fatal("取消后 15s 仍未返回 —— runCtx 未生效")
	}
}

// 回归: 不设 runCtx 时走 f.ctx 默认路径, 正常任务照常执行
func TestBuild_NoRunCtx_DefaultPathWorks(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	out, res, err := f.Build("print('RUNCTX_DEFAULT_OK')", "python", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("默认路径失败: err=%v res=%+v", err, res)
	}
	if !strings.Contains(out, "RUNCTX_DEFAULT_OK") {
		t.Errorf("输出异常: %q", out)
	}
}

// 回归: SetRunCtx(nil) 必须恢复默认, 不能残留上一次的 runCtx
func TestBuild_SetRunCtxNil_RestoresDefault(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	f.SetRunCtx(ctx)
	f.SetRunCtx(nil)
	cancel() // 即使旧 ctx 已取消, 也不应影响后续构建

	out, res, err := f.Build("print('AFTER_RESET_OK')", "python", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("恢复默认后失败: err=%v res=%+v", err, res)
	}
	if !strings.Contains(out, "AFTER_RESET_OK") {
		t.Errorf("输出异常: %q", out)
	}
}
