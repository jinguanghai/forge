// ══════════════════════════════════════════════════════════════
// agent_history.go — 会话检查点 (落盘/恢复) 与固定头不变式校验。
// ══════════════════════════════════════════════════════════════

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ─── 会话检查点 (借鉴 langgraph Checkpoint 思想): ───
// 每轮任务正常结束后把对话历史落盘。按会话隔离:
//   - 当前会话非空 → .forge/sessions/<id>/checkpoint.json
//   - legacy 模式   → .forge/checkpoint.json (与 v3.0 行为一致)
//
// 崩溃/重启后主人输入"恢复"即可续接上次会话上下文。
func (a *AgentRunner) saveCheckpoint(workDir string) error {
	if workDir == "" {
		return nil
	}
	cp := struct {
		SavedAt string        `json:"saved_at"`
		History []ChatMessage `json:"history"`
	}{SavedAt: time.Now().Format("2006-01-02 15:04:05"), History: a.history}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	path := checkpointPath(workDir)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// 回写会话元数据 (标题取首条真实用户输入)
	touchSession(workDir, deriveSessionTitle(a.history))
	return nil
}

// loadCheckpoint 读取上次会话检查点, 返回 (历史, 保存时间, 错误)
// 优先读当前会话目录; legacy 模式读旧路径。
func loadCheckpoint(workDir string) ([]ChatMessage, string, error) {
	if workDir == "" {
		return nil, "", fmt.Errorf("no workdir")
	}
	data, err := os.ReadFile(checkpointPath(workDir))
	if err != nil {
		return nil, "", err
	}
	var cp struct {
		SavedAt string        `json:"saved_at"`
		History []ChatMessage `json:"history"`
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, "", err
	}
	return cp.History, cp.SavedAt, nil
}

// RestoreHistory 从检查点恢复历史（带锁, 线程安全）。
// v3.1: 重建固定头 (system 恒定版+记忆锚点+折叠索引), 历史正文跳过旧固定头保留。
func (a *AgentRunner) RestoreHistory(history []ChatMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.restoreHistoryLocked(history)
}

// restoreHistoryLocked 重建固定头 + 保留历史正文 (跳过旧固定头避免重复注入)。
func (a *AgentRunner) restoreHistoryLocked(history []ChatMessage) {
	head := a.buildFixedHead()
	rest := history
	for len(rest) > 0 && rest[0].Role == "system" {
		rest = rest[1:]
	}
	if len(rest) > 0 && strings.Contains(rest[0].Content, "<memory_context>") {
		rest = rest[1:]
	}
	if len(rest) > 0 && strings.Contains(rest[0].Content, "<folded_archive_index>") {
		rest = rest[1:]
	}
	a.history = make([]ChatMessage, 0, len(head)+len(rest))
	a.history = append(a.history, head...)
	a.history = append(a.history, rest...)
	a.headLen = len(head)
	// v3.2: 恢复时固定头用首值缓存, 基线同步 → 若 memory/折叠在运行中更新, syncDynamicTails 尾部 diff。
	a.lastMemText = a.initialMemText
	a.lastActiveTask = currentActiveTask(a.cfg.WorkDir)
	a.lastFoldedText = a.initialFoldedText
}

// buildFixedHead 构建固定头部: [system 恒定版] + [记忆锚点] + [折叠索引]。
// 记忆/索引变化不走重写, 由 syncDynamicTails 在运行时 diff 追加 (DSH project 机制)。
func (a *AgentRunner) buildFixedHead() []ChatMessage {
	head := []ChatMessage{
		{Role: "system", Content: buildSystemPromptStable(a.cfg.WorkDir, a.cfg.GatesEnabled)},
	}
	// v3.2 (DSH RuntimeContextProjection): 固定头首值 —— 首次调用读一次文件并缓存,
	// 此后不重读。memory/折叠索引的后续变化由 syncDynamicTails 在运行时 diff 追加到尾部
	// (完全不碰固定头), 保证 system+记忆锚点+折叠索引 前缀恒定的前提下仍能感知更新。
	if !a.headCached {
		a.initialMemText = buildMemoryTailText(a.cfg.WorkDir)
		a.initialFoldedText = compactFoldedIndex(a.cfg.WorkDir)
		a.headCached = true
	}
	if a.initialMemText != "" {
		head = append(head, ChatMessage{Role: "user", Content: "【持久记忆锚点】稳定段常驻, 变更以「记忆已更新」消息追加。\n<memory_context>\n" + a.initialMemText + "\n</memory_context>"})
	}
	if a.initialFoldedText != "" {
		head = append(head, ChatMessage{Role: "user", Content: "<folded_archive_index>\n" + a.initialFoldedText + "</folded_archive_index>\n输入「展开<名称>」预览摘要或「深入<名称>」读全文。"})
	}
	return head
}

// syncDynamicTails (DSH project 机制): 每轮请求前检测记忆锚点/待办/折叠索引变化。
// 变化只 append 更新消息到历史尾部 (绝不重写固定头) → 前缀缓存不断裂。
func (a *AgentRunner) syncDynamicTails() {
	if a.cfg == nil {
		return
	}
	if cur := buildMemoryTailText(a.cfg.WorkDir); cur != a.lastMemText {
		if a.lastMemText != "" {
			if diff := memoryTailDiff(a.lastMemText, cur); diff != "" {
				a.history = append(a.history, ChatMessage{Role: "user", Content: "【记忆已更新】\n" + diff})
			}
		}
		a.lastMemText = cur
	}
	if cur := currentActiveTask(a.cfg.WorkDir); cur != a.lastActiveTask {
		if a.lastActiveTask != "" && cur != "" {
			a.history = append(a.history, ChatMessage{Role: "user", Content: "【待办更新】" + cur})
		}
		a.lastActiveTask = cur
	}
	if cur := compactFoldedIndex(a.cfg.WorkDir); cur != a.lastFoldedText {
		if a.lastFoldedText != "" && cur != "" {
			a.history = append(a.history, ChatMessage{Role: "user", Content: "【档案索引已更新】\n" + cur})
		}
		a.lastFoldedText = cur
	}
}

// verifyHeadInvariant 固定头一致性守卫 (把 v3.1 血案焊死):
// headCached 机制下 buildFixedHead 恒返回首值缓存。一旦未来改动破坏"固定头恒定"
// (如 buildFixedHead 被改成重读文件, 或 headLen 未随 buildFixedHead 同步),
// 此断言在判定边界当场截断告警, 而不是等 /cache 事后发现命中率断崖 —— 前置拦截。
func (a *AgentRunner) verifyHeadInvariant() bool {
	head := a.buildFixedHead()
	if a.headLen != len(head) {
		return false
	}
	if len(a.history) < a.headLen {
		return false
	}
	for i := 0; i < a.headLen; i++ {
		if a.history[i].Role != head[i].Role || a.history[i].Content != head[i].Content {
			return false
		}
	}
	return true
}
