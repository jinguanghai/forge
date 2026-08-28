package main

// session.go — 多会话隔离 (借鉴 DSH 会话模型)
//
// 设计: .forge/sessions/<id>/ 每个会话一个目录:
//   - session.json     元数据 (id/title/created_at/updated_at)
//   - checkpoint.json  该会话的对话历史检查点 (agent.go 共用)
//   - events.jsonl     该会话的事件证据链 (event_log.go 共用)
//
// 兼容: currentSession() 为空字符串 = legacy 模式, 沿用旧路径
// .forge/checkpoint.json 与 .forge/events.jsonl —— 旧数据不迁移也完全可用,
// 旧检查点/旧事件在 legacy 模式(不 /use 任何会话)下行为与 v3.0 完全一致。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Session 会话元数据
type Session struct {
	ID        string `json:"id"`
	Title     string `json:"title,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

var (
	sessionMu  sync.RWMutex
	curSession string // 空 = legacy 模式
)

// currentSession 当前会话 id (空 = legacy)
func currentSession() string {
	sessionMu.RLock()
	defer sessionMu.RUnlock()
	return curSession
}

// setCurrentSession 设置当前会话 id (空 = 回到 legacy 模式)
func setCurrentSession(id string) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	curSession = id
}

// sessionsDir 会话根目录
func sessionsDir(workDir string) string {
	return filepath.Join(workDir, ".forge", "sessions")
}

// sessionDir 单个会话目录
func sessionDir(workDir, id string) string {
	return filepath.Join(sessionsDir(workDir), id)
}

// ensureSessionDir 创建会话目录; legacy 模式(id 空)返回空串。
func ensureSessionDir(workDir, id string) string {
	if id == "" {
		return ""
	}
	dir := sessionDir(workDir, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ""
	}
	return dir
}

// newSessionID 生成新会话 id: s20260817_153000 (秒级, 并发下不重复)
func newSessionID() string {
	return "s" + time.Now().Format("20060102_150405")
}

// createSession 创建新会话并设为当前会话。
func createSession(workDir string) (*Session, error) {
	id := newSessionID()
	s := &Session{
		ID:        id,
		CreatedAt: time.Now().Format(time.RFC3339),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	if err := saveSessionMeta(workDir, s); err != nil {
		return nil, err
	}
	setCurrentSession(id)
	return s, nil
}

// saveSessionMeta 原子写会话元数据 (tmp+rename, 防半截)。
func saveSessionMeta(workDir string, s *Session) error {
	dir := ensureSessionDir(workDir, s.ID)
	if dir == "" {
		return fmt.Errorf("无法创建会话目录: %s", s.ID)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "session.json.tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "session.json"))
}

// touchSession 更新当前会话元数据 (updated_at / title)。失败静默, 永不阻塞主流程。
func touchSession(workDir, title string) {
	id := currentSession()
	if id == "" {
		return
	}
	dir := sessionDir(workDir, id)
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return
	}
	var s Session
	if json.Unmarshal(data, &s) != nil {
		return
	}
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	if title != "" {
		s.Title = title
	}
	_ = saveSessionMeta(workDir, &s)
}

// loadSession 读取会话元数据
func loadSession(workDir, id string) (*Session, error) {
	dir := sessionDir(workDir, id)
	data, err := os.ReadFile(filepath.Join(dir, "session.json"))
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// listSessions 列出所有会话, 按 updated_at 倒序 (最近活跃在前)。
func listSessions(workDir string) ([]Session, error) {
	dir := sessionsDir(workDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if s, err := loadSession(workDir, e.Name()); err == nil {
			out = append(out, *s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out, nil
}

// checkpointPath 当前会话的检查点路径; legacy 模式 = .forge/checkpoint.json
func checkpointPath(workDir string) string {
	if id := currentSession(); id != "" {
		return filepath.Join(sessionDir(workDir, id), "checkpoint.json")
	}
	return filepath.Join(workDir, ".forge", "checkpoint.json")
}

// checkpointSessionPath 指定会话的检查点路径 (空 id = legacy 路径)
func checkpointSessionPath(workDir, id string) string {
	if id == "" {
		return filepath.Join(workDir, ".forge", "checkpoint.json")
	}
	return filepath.Join(sessionDir(workDir, id), "checkpoint.json")
}

// stripRecallPrefix 去掉 userContent 开头的 <recalled_memory> 注入块,
// 用于从检查点历史提取"真实用户输入"作会话标题。
func stripRecallPrefix(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "<recalled_memory>") {
		if i := strings.Index(s, "</recalled_memory>"); i >= 0 {
			s = strings.TrimSpace(s[i+len("</recalled_memory>"):])
		}
	}
	return s
}

// deriveSessionTitle 从历史首条真实 user 消息提取标题 (前 24 字)。
// v3.1: 跳过固定头记忆/档案消息与更新消息 (【持久记忆锚点】/【记忆已更新】等)。
func deriveSessionTitle(history []ChatMessage) string {
	for _, m := range history {
		if m.Role != "user" {
			continue
		}
		if strings.Contains(m.Content, "<memory_context>") || strings.Contains(m.Content, "<folded_archive_index>") {
			continue
		}
		if strings.HasPrefix(m.Content, "【") {
			continue
		}
		s := stripRecallPrefix(m.Content)
		runes := []rune(s)
		if len(runes) > 24 {
			return string(runes[:24]) + "…"
		}
		return string(runes)
	}
	return ""
}
