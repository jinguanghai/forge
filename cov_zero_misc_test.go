package main

// cov_zero_misc_test.go — 零覆盖小函数补测 (20260920)
//
// 覆盖 goalStatusLabel / setCacheStatPath / toolCodeStreamer.flush /
// enableWindowsUTF8 / checkPeakHour 五个此前零覆盖的函数。
// (runSelfReplace 的用例已迁往 selfreplace_isolated_test.go —— 那边自建隔离目录 +
//  子进程, 快照基线不再受测试二进制所在目录的外部噪声影响。)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 目标状态标签: 四态各有独立文案, 未知状态原样透传 (不吞不编)。
func TestGoalStatusLabel_AllStates(t *testing.T) {
	seen := map[string]string{}
	for _, st := range []string{GoalActive, GoalPaused, GoalCompleted, GoalBlocked} {
		label := goalStatusLabel(st)
		if label == "" {
			t.Fatalf("状态 %q 的标签不得为空", st)
		}
		if label == st {
			t.Fatalf("已知状态 %q 应有人读文案, 实际原样返回", st)
		}
		if prev, dup := seen[label]; dup {
			t.Fatalf("状态 %q 与 %q 标签重复: %q", st, prev, label)
		}
		seen[label] = st
	}
	const unknown = "weird_state"
	if got := goalStatusLabel(unknown); got != unknown {
		t.Fatalf("未知状态应原样透传, 实际 %q", got)
	}
}

// setCacheStatPath 必须落在给定工作目录下 (缓存统计是每工作目录隔离的)。
func TestSetCacheStatPath_JoinsWorkDir(t *testing.T) {
	old := cacheStatPath
	t.Cleanup(func() { cacheStatPath = old })

	dir := t.TempDir()
	setCacheStatPath(dir)
	if cacheStatPath != filepath.Join(dir, "cache_stats.jsonl") {
		t.Fatalf("路径不符: %s", cacheStatPath)
	}
	if !strings.HasPrefix(cacheStatPath, dir) {
		t.Fatalf("缓存统计文件必须位于工作目录内: %s", cacheStatPath)
	}
}

// flush: 空缓冲静默, 非空缓冲吐出残余文本(返回给调用方写 stderr —— 显示侧统一由 agent.go 落笔)。
func TestToolCodeStreamerFlush(t *testing.T) {
	t.Run("空缓冲无输出", func(t *testing.T) {
		if out := (&toolCodeStreamer{}).flush(); out != "" {
			t.Fatalf("空缓冲不应有输出, 实际 %q", out)
		}
	})

	t.Run("非空缓冲吐出残余", func(t *testing.T) {
		st := &toolCodeStreamer{}
		st.decoded.WriteString("print(1)")
		out := st.flush()
		// 高亮只插 ANSI 转义, 不改动原始字符 → 去转义后逐字节比对
		if !strings.Contains(reAnsiStrip.ReplaceAllString(out, ""), "print(1)") {
			t.Fatalf("flush 应输出已解码代码, 实际 %q", out)
		}
		if again := st.flush(); again != "" {
			t.Fatalf("重复 flush 应无输出, 实际 %q", again)
		}
	})
}

// enableWindowsUTF8 幂等: 复调不 panic (设置代码页/虚拟终端处理)。
func TestEnableWindowsUTF8_Idempotent(t *testing.T) {
	enableWindowsUTF8()
	enableWindowsUTF8()
}

// checkPeakHour: 三种配置下都必须给出可读提示 (高峰/非高峰/自动路由)。
func TestCheckPeakHour_AlwaysReports(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
	}{
		{"无配置", nil},
		{"纯 DeepSeek", &Config{}},
		{"已配 MiniMax", &Config{MiniMaxAPIKey: "k", MiniMaxBaseURL: "u", MiniMaxModel: "m"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := sinkStderr(t)
			checkPeakHour(c.cfg)
			b, err := os.ReadFile(f.Name())
			if err != nil {
				t.Fatalf("读 stderr 失败: %v", err)
			}
			if strings.TrimSpace(string(b)) == "" {
				t.Fatalf("应给出时段提示, 实际无输出")
			}
		})
	}
}
