package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 验证状态栏缓存命中率文本: 无数据/历史数据/会话统计三分支。
func TestStatusBarCacheText_NoData(t *testing.T) {
	old := cacheStatPath
	cacheStatPath = filepath.Join(t.TempDir(), "cache_stats.jsonl")
	defer func() { cacheStatPath = old }()

	got := statusBarCacheText(nil, 20)
	if !strings.Contains(got, "缓存命中") || !strings.Contains(got, "--") {
		t.Fatalf("无数据应显示 '缓存命中 --', 实际: %q", got)
	}
	if !strings.Contains(got, "\x1b[2m") { // dim
		t.Fatalf("无数据分支应使用 dim 色, 实际: %q", got)
	}
}

func TestStatusBarCacheText_HistoryData(t *testing.T) {
	old := cacheStatPath
	cacheStatPath = filepath.Join(t.TempDir(), "cache_stats.jsonl")
	defer func() { cacheStatPath = old }()

	var sb strings.Builder
	for i := 0; i < 20; i++ {
		cs := CacheStat{Time: time.Now().Format(time.RFC3339), Hit: 75, Miss: 25}
		b, _ := json.Marshal(cs)
		sb.Write(b)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(cacheStatPath, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	got := statusBarCacheText(nil, 20)
	if !strings.Contains(got, "75.0%") {
		t.Fatalf("历史数据应显示 75.0%%, 实际: %q", got)
	}
	if !strings.Contains(got, "近20条") {
		t.Fatalf("历史数据应标注 近20条, 实际: %q", got)
	}
}

func TestStatusBarCacheText_Session(t *testing.T) {
	old := cacheStatPath
	cacheStatPath = filepath.Join(t.TempDir(), "cache_stats.jsonl") // 无历史
	defer func() { cacheStatPath = old }()

	agent := &AgentRunner{stats: &SessionStats{CacheHit: 80, CacheMiss: 20}}
	got := statusBarCacheText(agent, 20)
	if !strings.Contains(got, "80.0%") || !strings.Contains(got, "会话") {
		t.Fatalf("会话命中率应显示 80.0%% 会话, 实际: %q", got)
	}
}
