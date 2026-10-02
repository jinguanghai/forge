package main

// cov_gaps5_more_test.go — 覆盖率缺口补测 第五批 (跨平台, 20261001)
//
// 覆盖 /看图 命令的四条早退路径 (无参数 / 仅标志 / 目录不存在 / 目录无图)
// 与信号处理器安装。全部不触发 LLM 网络调用。

import (
	"strings"
	"testing"
)

func TestCovGap5_CmdVision_EarlyExits(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false

	// 1) 无参数 → 用法提示
	out := captureStdout(t, func() { cmdVision(agent, cfg, nil, hist, &sr, "/看图") })
	if !strings.Contains(out, "用法") {
		t.Fatalf("无参数应给用法: %q", out)
	}

	// 2) 只有 -d 标志 → 剥掉标志后仍为空, 同样给用法
	out = captureStdout(t, func() { cmdVision(agent, cfg, []string{"/看图", "-d"}, hist, &sr, "/看图") })
	if !strings.Contains(out, "用法") {
		t.Fatalf("仅标志应给用法: %q", out)
	}

	// 3) 目录不存在 → 报错早退 (不得进入 LLM 调用)
	out = captureStdout(t, func() { cmdVision(agent, cfg, []string{"/看图", "no_such_dir_zzz"}, hist, &sr, "/看图") })
	if !strings.Contains(out, "识图失败") {
		t.Fatalf("目录不存在应报错: %q", out)
	}

	// 4) 目录存在但无图片 → 提示早退
	empty := t.TempDir()
	out = captureStdout(t, func() { cmdVision(agent, cfg, []string{"/看图", empty}, hist, &sr, "/看图") })
	if !strings.Contains(out, "未发现图片") {
		t.Fatalf("空目录应提示未发现图片: %q", out)
	}
}

// installSignals: 幂等安装不 panic (重复调用不得泄漏 panic 或改坏全局)。
func TestCovGap5_InstallSignals_Idempotent(t *testing.T) {
	agent, _, _ := newHandleCmdAgent(t)
	installSignals(agent)
	installSignals(agent)
	installSignals(nil) // 空 agent: 处理路径有 nil 判定, 安装本身不得 panic
}
