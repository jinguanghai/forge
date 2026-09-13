package main

// stats_coverage_test.go — SessionStats 覆盖补强 (B2)
//
// 原状: stats.go 8 个方法零测试引用 (addTurn/setModel/addToken/addToolOK/
// addToolFail/addCache/cacheRate/StringZh)。会话统计是状态栏与 /stats 的数据源,
// 累加错误会静默污染展示, 故补齐累加语义 + 并发安全两类用例。

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionStats_Accumulate(t *testing.T) {
	var s SessionStats
	s.StartTime = time.Now()
	s.addTurn(1500 * time.Millisecond)
	s.addTurn(500 * time.Millisecond)
	s.setModel("deepseek-flash")
	s.addToken(100)
	s.addToken(250)
	s.addToolOK(30 * time.Millisecond)
	s.addToolFail(20 * time.Millisecond)
	s.addCache(9, 1)

	if s.Turns != 2 {
		t.Fatalf("Turns 应累加为 2, 实际 %d", s.Turns)
	}
	if s.TotalMs != 2000 {
		t.Fatalf("TotalMs 应为 2000, 实际 %d", s.TotalMs)
	}
	if s.TotalTokens != 350 {
		t.Fatalf("TotalTokens 应为 350, 实际 %d", s.TotalTokens)
	}
	if s.ToolOK != 1 || s.ToolFail != 1 {
		t.Fatalf("工具计数应 1/1, 实际 %d/%d", s.ToolOK, s.ToolFail)
	}
	if s.TotalToolMs != 50 {
		t.Fatalf("TotalToolMs 应为 50 (成功与失败都计入), 实际 %d", s.TotalToolMs)
	}
	if s.LastModel != "deepseek-flash" {
		t.Fatalf("LastModel 应为 deepseek-flash, 实际 %s", s.LastModel)
	}
	rate, ok := s.cacheRate()
	if !ok || rate != 90 {
		t.Fatalf("cacheRate 应为 90, 实际 %v ok=%v", rate, ok)
	}
	if zh := s.StringZh(); !strings.Contains(zh, "轮次:2") || !strings.Contains(zh, "工具:1✓/1✗") {
		t.Fatalf("StringZh 内容不符: %s", zh)
	}
	if en := s.String(); !strings.Contains(en, "turns:2") || !strings.Contains(en, "tools:1✓/1✗") {
		t.Fatalf("String 内容不符: %s", en)
	}
}

func TestSessionStats_CacheRateNoSample(t *testing.T) {
	var s SessionStats
	if _, ok := s.cacheRate(); ok {
		t.Fatal("无缓存样本时应返回 ok=false")
	}
}

func TestSessionStats_ConcurrentSafe(t *testing.T) {
	var s SessionStats
	s.StartTime = time.Now()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.addTurn(time.Millisecond)
			s.addToken(2)
			s.addToolOK(time.Millisecond)
			s.addToolFail(time.Millisecond)
			s.addCache(1, 1)
			s.setModel("m")
			_ = s.String()
			_ = s.StringZh()
			_, _ = s.cacheRate()
		}()
	}
	wg.Wait()
	if s.Turns != n || s.TotalTokens != 2*n {
		t.Fatalf("并发累加丢数: Turns=%d (期望 %d), Tokens=%d (期望 %d)", s.Turns, n, s.TotalTokens, 2*n)
	}
	if s.ToolOK != n || s.ToolFail != n {
		t.Fatalf("并发工具计数不符: %d/%d (期望 %d/%d)", s.ToolOK, s.ToolFail, n, n)
	}
	if s.CacheHit != n || s.CacheMiss != n {
		t.Fatalf("并发缓存计数不符: %d/%d (期望 %d/%d)", s.CacheHit, s.CacheMiss, n, n)
	}
}
