package main

// memory_write_sentinel.go — 记忆写入路径哨兵 (把"内容变化必经 SaveMemory"变成可判定)
//
// 背景 (20260912 实测): memory.json 存在绕过 SaveMemory 的写入路径 ——
// 14:16 主文件有写入但 anchor_audit.jsonl 无对应留痕 (末条为 20260911)。
// 现有护栏只审计"锚点字段改动": 纯动态字段写入 (key_findings/last_updated)
// 与外部覆盖 (rollback.cmd / 手工编辑 / 脚本) 均不留痕 → 旁路写入不可发现。
//
// 本哨兵把"每次 memory.json 内容变化都必须经过 SaveMemory"变成死程序判定:
//   ① 全量写入留痕: SaveMemory 每次成功写入后追加 memory_writes.jsonl
//      (time + 内容 sha256 前16 + size + 是否锚点改动)
//   ② 判据: 当前 memory.json 的 sha 必须出现在留痕集合中, 否则判为旁路写入
//      (旁路 = 未过 memHealthLint 体检 + 未留审计, 属护栏盲区)
//
// 设计对齐 guard.go / anchorGuardAudit: 只报不改 (只读哨兵), 留痕失败静默不阻断主流程。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const memoryWritesFileName = "memory_writes.jsonl"

// MemoryWriteEntry 记忆写入留痕条目 (memory_writes.jsonl 一行一条)。
type MemoryWriteEntry struct {
	Time   string `json:"time"`             // RFC3339 本地时间
	SHA    string `json:"sha"`              // 写入后 memory.json 内容 sha256 前16位
	Size   int    `json:"size"`             // 内容字节数
	Anchor bool   `json:"anchor"`           // 是否锚点字段改动 (与 anchor_audit 互补)
	Reason string `json:"reason,omitempty"` // 写入来源 (SaveMemory / baseline)
}

func memoryWritesPath(workDir string) string {
	return filepath.Join(workDir, memoryWritesFileName)
}

// recordMemoryWrite 追加一条写入留痕 (失败静默 —— 留痕失败不阻断主流程)。
func recordMemoryWrite(workDir string, data []byte, anchor bool, reason string) {
	entry := MemoryWriteEntry{
		Time:   time.Now().Format(time.RFC3339),
		SHA:    sysHashPrefix(data),
		Size:   len(data),
		Anchor: anchor,
		Reason: reason,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(memoryWritesPath(workDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(string(line) + "\n")
	_ = f.Close()
}

// loadMemoryWriteSHAs 读取留痕中的 sha 集合。返回 (集合, 有效条目数, 错误)。
// 文件不存在 = 无留痕 (返回空集合, 不算错误)。
func loadMemoryWriteSHAs(workDir string) (map[string]bool, int, error) {
	data, err := os.ReadFile(memoryWritesPath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, 0, nil
		}
		return nil, 0, err
	}
	set := make(map[string]bool)
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e MemoryWriteEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		n++
		if e.SHA != "" {
			set[e.SHA] = true
		}
	}
	return set, n, nil
}

// memoryWriteSentinel 校验 memory.json 当前内容是否有 SaveMemory 写入留痕。
//
// 返回 (ok, 消息)。ok=false 仅当"留痕基线已建立但当前内容不在留痕中" ——
// 即确凿的旁路写入。无法判定时 (无主文件/无留痕) 一律返回 ok=true, 宁缺毋滥。
func memoryWriteSentinel(workDir string) (bool, string) {
	data, err := os.ReadFile(memoryFilePath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return true, "✅ 写入路径: 主文件不存在 (首次运行), 不判定"
		}
		return false, fmt.Sprintf("🟡 写入路径: 读 memory.json 失败: %v", err)
	}
	set, n, err := loadMemoryWriteSHAs(workDir)
	if err != nil {
		return false, fmt.Sprintf("🟡 写入路径: 读写入留痕失败: %v", err)
	}
	if n == 0 {
		return true, "ℹ️ 写入路径: 尚无留痕基线 (哨兵自下次 SaveMemory 起生效)"
	}
	cur := sysHashPrefix(data)
	if set[cur] {
		return true, fmt.Sprintf("✅ 写入路径: 当前版本有 SaveMemory 留痕 (sha=%s, 留痕 %d 条)", cur, n)
	}
	return false, fmt.Sprintf("🔴 写入路径: 当前 memory.json (sha=%s, %d 字节) 无 SaveMemory 留痕 → 疑似旁路写入/外部覆盖 (未过体检、未留审计)", cur, len(data))
}

// ensureMemoryWriteBaseline 建立写入留痕基线 (启动时调用一次, 幂等)。
//
// 留痕文件已存在则不动; 不存在且主文件存在 → 记一条 baseline 条目。
// 目的: 让哨兵在部署后立即具备判定能力, 而非等下一次 SaveMemory。
func ensureMemoryWriteBaseline(workDir string) {
	if _, err := os.Stat(memoryWritesPath(workDir)); err == nil {
		return
	}
	data, err := os.ReadFile(memoryFilePath(workDir))
	if err != nil {
		return
	}
	recordMemoryWrite(workDir, data, false, "baseline")
}
