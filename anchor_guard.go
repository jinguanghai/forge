package main

// anchor_guard.go — 锚点写入护栏 (把记忆纪律变成程序)
//
// 背景: cache_discipline 原是记忆文本(靠模型自觉), 实测锚点改动日命中率 57-69%
// vs 稳定日 98%+; DeepSeek 缓存未命中价是命中价 30 倍 (flash $0.22 vs $0.007/M)。
// 本护栏把“集中改动/避免频繁改”从纪律文本变成程序强制:
//   ① 审计: 每次 SaveMemory 写入留痕到 anchor_audit.jsonl
//   ② 频率拦截: 同一天第 2 次写锚点 → 返回错误 (提示缓存重置成本, 要求合并改动)
//   ③ 开关: FORGE_ANCHOR_GUARD=0 关闭 (回滚通道, 不破坏原流程)
// 设计原则(对齐 guard.go): 宁缺毋滥, 只拦高频改动, 不误伤正常写入。

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AnchorAuditEntry 锚点写入审计条目 (anchor_audit.jsonl 一行一条)
type AnchorAuditEntry struct {
	Time    string   `json:"time"`               // RFC3339 本地时间
	File    string   `json:"file"`               // 被写入的文件 (memory.json)
	Fields  []string `json:"fields"`             // 顶层字段差异 (改了什么)
	SysHash string   `json:"sys_hash,omitempty"` // system 前缀指纹 (前16位)
	Reason  string   `json:"reason,omitempty"`   // 备注 (missing_last_updated 等)
}

const anchorAuditFileName = "anchor_audit.jsonl"

func anchorAuditPath(workDir string) string {
	return filepath.Join(workDir, anchorAuditFileName)
}

// anchorGuardEnabled 护栏开关: FORGE_ANCHOR_GUARD=0 关闭, 其余默认开启。
func anchorGuardEnabled() bool {
	return os.Getenv("FORGE_ANCHOR_GUARD") != "0"
}

// sysHashPrefix 计算数据 SHA-256 前 16 位 (system 前缀指纹)。
func sysHashPrefix(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:8])
}

// anchorFields 锚点字段清单——只有这些字段的变化会重置 system 前缀缓存。
// 纯会话片段 (key_findings 等) 的写入不触发频率拦截。
var anchorFields = []string{"identity", "role", "language", "working_dir", "gates", "axioms", "self_governance", "environment", "hardcoded_paths", "defense", "user_principle", "active_task"}

// isAnchorChange 判断字段差异是否涉及锚点字段。
func isAnchorChange(fields []string) bool {
	for _, f := range fields {
		for _, a := range anchorFields {
			if f == a {
				return true
			}
		}
	}
	return false
}

// anchorAuditTodayCount 统计今天的锚点写入次数 (按审计文件 time 字段, 仅锚点改动计数)。
func anchorAuditTodayCount(workDir string) (int, error) {
	data, err := os.ReadFile(anchorAuditPath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // 无审计文件 = 从未写过
		}
		return 0, err
	}
	today := time.Now().Format("2006-01-02")
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e AnchorAuditEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if strings.HasPrefix(e.Time, today) {
			count++
		}
	}
	return count, nil
}

// anchorGuardCheck 频率拦截: 同一天第 2 次写锚点 → 返回错误。
// 错误信息带缓存成本提示, 模型收到后应合并改动或改天再写。
func anchorGuardCheck(workDir string) error {
	if !anchorGuardEnabled() {
		return nil
	}
	count, err := anchorAuditTodayCount(workDir)
	if err != nil {
		return nil // 审计文件损坏不阻断写入 (宁缺毋滥)
	}
	if count >= 1 {
		return fmt.Errorf("锚点写入护栏: 今日已写 %d 次, 禁止第 %d 次 (缓存将重置, 未命中价是命中价 30 倍)。请合并改动到一次写入, 或设 FORGE_ANCHOR_GUARD=0 关闭护栏", count, count+1)
	}
	return nil
}

// anchorChangedFields 对比新旧记忆 JSON 的顶层字段差异 (审计用)。
// old 不存在/不可解析 (首次写入) 时, 返回新数据的全部键。
func anchorChangedFields(oldData, newData []byte) []string {
	var oldM, newM map[string]interface{}
	if json.Unmarshal(newData, &newM) != nil {
		return nil
	}
	if json.Unmarshal(oldData, &oldM) != nil {
		var all []string
		for k := range newM {
			all = append(all, k)
		}
		return all
	}
	var fields []string
	for k := range newM {
		if _, ok := oldM[k]; !ok {
			fields = append(fields, k)
		}
	}
	for k := range oldM {
		if _, ok := newM[k]; !ok {
			fields = append(fields, k)
		}
	}
	if len(fields) == 0 {
		for k, nv := range newM {
			if ov, ok := oldM[k]; !ok || !jsonEqual(ov, nv) {
				fields = append(fields, k)
			}
		}
	}
	return fields
}

// jsonEqual 简易 JSON 值比较 (序列化后比较)。
func jsonEqual(a, b interface{}) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// anchorGuardAudit 追加一条审计记录 (幂等, 失败静默——审计失败不阻断主流程)。
// 仅锚点字段改动入审计; 纯片段写入 (key_findings 等) 不计数。
func anchorGuardAudit(workDir string, fields []string, reason string) {
	if !anchorGuardEnabled() {
		return
	}
	if !isAnchorChange(fields) {
		return
	}
	entry := AnchorAuditEntry{
		Time:   time.Now().Format(time.RFC3339),
		File:   "memory.json",
		Fields: fields,
		Reason: reason,
	}
	if data, err := os.ReadFile(memoryFilePath(workDir)); err == nil {
		entry.SysHash = sysHashPrefix(data)
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(anchorAuditPath(workDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(string(line) + "\n")
	_ = f.Close()
}

// anchorAuditSummary 生成锚点改动摘要 (供 /anchor 命令与 /cache 归因)。
func anchorAuditSummary(workDir string) string {
	data, err := os.ReadFile(anchorAuditPath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return "  暂无锚点改动记录 (anchor_audit.jsonl)。"
		}
		return fmt.Sprintf("  ✗ 读审计失败: %v", err)
	}
	var entries []AnchorAuditEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e AnchorAuditEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return "  暂无锚点改动记录 (anchor_audit.jsonl)。"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  锚点改动总数: %d 次\n", len(entries)))
	cutoff := time.Now().AddDate(0, 0, -7).Format(time.RFC3339)
	recent := 0
	for _, e := range entries {
		if e.Time >= cutoff {
			recent++
		}
	}
	sb.WriteString(fmt.Sprintf("  近 7 天: %d 次\n", recent))
	sb.WriteString("  最近 5 条:\n")
	start := len(entries) - 5
	if start < 0 {
		start = 0
	}
	for _, e := range entries[start:] {
		fields := strings.Join(e.Fields, ",")
		if fields == "" {
			fields = "(未记录字段)"
		}
		sb.WriteString(fmt.Sprintf("    %s [%s] %s", e.Time, fields, e.Reason))
		if e.SysHash != "" {
			sb.WriteString(fmt.Sprintf(" sha=%s", e.SysHash))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
