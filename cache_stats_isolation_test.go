package main

// cache_stats_isolation_test.go — 缓存统计隔离哨兵 (20260923)
//
// 问题 (实测): gate_audit.jsonl 有 auditFilePath 的回退隔离出口, cache_stats.jsonl
// 却没有 —— 测试直接调 parseSSE/doStream (llm_http_test.go) 时, 内部 recordCacheStat
// 走默认相对路径 "cache_stats.jsonl", 直接写进仓库根。实测污染 1666 行假模型记录
// (test-model 1645 / m 14 / shape-model 7, 首条 2026-09-10, 末条 2026-09-23),
// 而 cacheHitRate/cacheHealth 只取"最近 n 条"算命中率 —— 污染行一旦落在窗口内,
// 状态栏命中率与 /cache 结论当场失真。
//
// 已有 setTempCacheStat 只覆盖显式调用它的测试, 覆盖不到 parseSSE/doStream 内部路径。
// 本文件把"测试进程的缓存统计写入必须落在隔离路径"固化为死程序判定。
//
// 隔离设置见 audit_isolation_test.go 的 TestMain (FORGE_CACHE_STATS_PATH)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 哨兵 a: cacheStatPathForWrite 的优先级语义 (隔离生效的前提)。
func TestCacheStatIsolation_PathPriority(t *testing.T) {
	iso := os.Getenv("FORGE_CACHE_STATS_PATH")
	if iso == "" {
		t.Fatal("TestMain 未设置 FORGE_CACHE_STATS_PATH, 缓存统计隔离失效")
	}
	// 默认名 (未显式设置工作目录) 必须回退隔离路径
	old := cacheStatPath
	cacheStatPath = defaultCacheStatName
	if got := cacheStatPathForWrite(); got != iso {
		cacheStatPath = old
		t.Fatalf("默认路径应回退隔离文件, 实际 %s", got)
	}
	// 显式设置的有效路径必须优先 (否则会破坏生产写入与 setTempCacheStat)
	valid := filepath.Join("C:", "tmp", "somewhere", defaultCacheStatName)
	cacheStatPath = valid
	got := cacheStatPathForWrite()
	cacheStatPath = old
	if got != valid {
		t.Fatalf("显式路径应优先, 实际 %s", got)
	}
}

// 哨兵 b: 端到端 —— 测试进程的写入不得落到仓库根的生产文件。
func TestCacheStatIsolation_WriteDoesNotReachRepoRoot(t *testing.T) {
	iso := os.Getenv("FORGE_CACHE_STATS_PATH")
	if iso == "" {
		t.Skip("无隔离路径, 跳过 (由哨兵 a 报错)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	prod := filepath.Join(cwd, defaultCacheStatName)

	// 唯一特征串: 只可能由本次写入产生, 故并发的外部会话写入不会干扰判据。
	const marker = "isolation-probe-model-9f3a1c"
	old := cacheStatPath
	cacheStatPath = defaultCacheStatName // 模拟测试进程的默认状态
	recordCacheStat(marker, 111, 222, "", false)
	cacheStatPath = old

	if b, err := os.ReadFile(prod); err == nil && strings.Contains(string(b), marker) {
		t.Fatalf("缓存统计写入落到了生产文件 %s —— 隔离失效 (测试正在污染 cache_stats.jsonl)", prod)
	}
	if b, err := os.ReadFile(iso); err != nil || !strings.Contains(string(b), marker) {
		t.Fatalf("隔离文件 %s 未收到写入 (err=%v) —— 埋点被误关", iso, err)
	}
}
