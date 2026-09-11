package main

import (
	"testing"
)

// TestCacheVersion: cache 持久化带版本; 加载校验
// 注: 原 TestCandidateBaseURLs / TestFailoverEndpoints 引用的
//
//	Config.BackupBaseURLs 与 candidateBaseURLs() 已随 failover 缓存功能删除，
//	属死代码测试，20260831 剔除。仅保留有效的缓存持久化测试。
func TestCacheVersion(t *testing.T) {
	path := t.TempDir() + "/cache.gob"
	f := &Forge{cachePersistFile: path, cacheMaxSize: 0}
	f.cache = map[string]ForgeGateResult{"k1": {}}
	f.cacheKeys = []string{"k1"}
	f.saveCacheToDisk()

	f2 := &Forge{cachePersistFile: path, cacheMaxSize: 0}
	f2.loadCacheFromDisk()
	if len(f2.cache) != 1 {
		t.Fatalf("expected 1 cache entry after load, got %d", len(f2.cache))
	}
	if _, ok := f2.cache["k1"]; !ok {
		t.Fatal("k1 missing after load")
	}
}
