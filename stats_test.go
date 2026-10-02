// stats_test.go: 会话统计聚合的行为契约。
//
// 重点: cacheRate 无样本时必须 ok=false —— 用 0 冒充"0% 命中"会误导状态栏。
package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStats_Accumulate(t *testing.T) {
	s := &SessionStats{StartTime: time.Now()}
	s.addTurn(1500 * time.Millisecond)
	s.addTurn(500 * time.Millisecond)
	s.setModel("deepseek-flash")
	s.addToken(100)
	s.addToken(250)
	s.addToolOK(30 * time.Millisecond)
	s.addToolFail(20 * time.Millisecond)
	s.addCache(90, 10)

	if s.Turns != 2 {
		t.Errorf("Turns = %d 期望 2", s.Turns)
	}
	if s.TotalMs != 2000 {
		t.Errorf("TotalMs = %d 期望 2000", s.TotalMs)
	}
	if s.TotalTokens != 350 {
		t.Errorf("TotalTokens = %d 期望 350", s.TotalTokens)
	}
	if s.ToolOK != 1 || s.ToolFail != 1 {
		t.Errorf("工具成败 = %d/%d 期望 1/1", s.ToolOK, s.ToolFail)
	}
	if s.TotalToolMs != 50 {
		t.Errorf("TotalToolMs = %d 期望 50 (成功与失败都要计入)", s.TotalToolMs)
	}
	if s.LastModel != "deepseek-flash" {
		t.Errorf("LastModel = %q", s.LastModel)
	}
	if s.CacheHit != 90 || s.CacheMiss != 10 {
		t.Errorf("缓存累计 = %d/%d 期望 90/10", s.CacheHit, s.CacheMiss)
	}
}

func TestStats_StringFormats(t *testing.T) {
	s := &SessionStats{StartTime: time.Now()}
	s.addTurn(1200 * time.Millisecond)
	s.setModel("deepseek-flash")
	s.addToken(42)
	s.addToolOK(time.Millisecond)
	s.addToolFail(time.Millisecond)

	en := s.String()
	for _, want := range []string{"turns:1", "tokens:42", "tools:1✓/1✗", "total:1.2s", "model:deepseek-flash"} {
		if !strings.Contains(en, want) {
			t.Errorf("String() 缺 %q: %q", want, en)
		}
	}
	zh := s.StringZh()
	for _, want := range []string{"轮次:1", "令牌:42", "工具:1✓/1✗", "计算耗时:1.2s"} {
		if !strings.Contains(zh, want) {
			t.Errorf("StringZh() 缺 %q: %q", want, zh)
		}
	}
}

func TestStats_CacheRate(t *testing.T) {
	s := &SessionStats{}
	if rate, ok := s.cacheRate(); ok || rate != 0 {
		t.Errorf("无样本应 ok=false, 实际 rate=%v ok=%v", rate, ok)
	}
	s.addCache(90, 10)
	rate, ok := s.cacheRate()
	if !ok {
		t.Fatal("有样本应 ok=true")
	}
	if rate != 90 {
		t.Errorf("rate = %v 期望 90", rate)
	}
	s.addCache(100, 0)
	rate, ok = s.cacheRate()
	if !ok || rate != 95 {
		t.Errorf("累计 rate = %v 期望 95", rate)
	}
}

func TestStats_ZeroTokenStillNoSample(t *testing.T) {
	s := &SessionStats{}
	s.addCache(0, 0)
	if _, ok := s.cacheRate(); ok {
		t.Error("零 token 累加后仍应 ok=false")
	}
}

func TestStats_ConcurrentSafe(t *testing.T) {
	s := &SessionStats{StartTime: time.Now()}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.addTurn(time.Millisecond)
			s.addToken(1)
			s.addToolOK(time.Millisecond)
			s.addToolFail(time.Millisecond)
			s.addCache(1, 1)
			_ = s.String()
			_ = s.StringZh()
			_, _ = s.cacheRate()
		}()
	}
	wg.Wait()
	if s.Turns != 20 || s.TotalTokens != 20 {
		t.Errorf("并发累加丢失: turns=%d tokens=%d 期望 20/20", s.Turns, s.TotalTokens)
	}
}
