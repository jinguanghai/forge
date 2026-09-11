// stats.go: 轻量统计聚合

package main

import (
	"fmt"
	"sync"
	"time"
)

// ─── Session stats ─────────────────────────────────────────

type SessionStats struct {
	mu          sync.RWMutex
	StartTime   time.Time
	Turns       int
	TotalMs     int64
	TotalTokens int64
	TotalToolMs int64
	ToolOK      int
	ToolFail    int
	LastModel   string
	CacheHit    int64
	CacheMiss   int64
}

func (s *SessionStats) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	elapsed := time.Since(s.StartTime).Round(time.Second)
	return fmt.Sprintf(
		"turns:%d | tokens:%d | tools:%d✓/%d✗ | total:%v | elapsed:%v | model:%s",
		s.Turns, s.TotalTokens, s.ToolOK, s.ToolFail,
		time.Duration(s.TotalMs)*time.Millisecond, elapsed, s.LastModel,
	)
}

func (s *SessionStats) StringZh() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	elapsed := time.Since(s.StartTime).Round(time.Second)
	return fmt.Sprintf(
		"轮次:%d | 令牌:%d | 工具:%d✓/%d✗ | 计算耗时:%v | 会话时长:%v | 模型:%s",
		s.Turns, s.TotalTokens, s.ToolOK, s.ToolFail,
		time.Duration(s.TotalMs)*time.Millisecond, elapsed, s.LastModel,
	)
}

func (s *SessionStats) addTurn(d time.Duration) {
	s.mu.Lock()
	s.Turns++
	s.TotalMs += d.Milliseconds()
	s.mu.Unlock()
}

func (s *SessionStats) setModel(m string) {
	s.mu.Lock()
	s.LastModel = m
	s.mu.Unlock()
}

func (s *SessionStats) addToken(n int64) {
	s.mu.Lock()
	s.TotalTokens += n
	s.mu.Unlock()
}

func (s *SessionStats) addToolOK(d time.Duration) {
	s.mu.Lock()
	s.ToolOK++
	s.TotalToolMs += d.Milliseconds()
	s.mu.Unlock()
}

func (s *SessionStats) addToolFail(d time.Duration) {
	s.mu.Lock()
	s.ToolFail++
	s.TotalToolMs += d.Milliseconds()
	s.mu.Unlock()
}

func (s *SessionStats) addCache(hit, miss int) {
	s.mu.Lock()
	s.CacheHit += int64(hit)
	s.CacheMiss += int64(miss)
	s.mu.Unlock()
}

func (s *SessionStats) cacheRate() (rate float64, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := s.CacheHit + s.CacheMiss
	if total == 0 {
		return 0, false
	}
	return float64(s.CacheHit) * 100 / float64(total), true
}
