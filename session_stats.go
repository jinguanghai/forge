package main

// session_stats.go — 按会话的 gate 统计 (/stats 增强)
//
// 消费 events.jsonl 的 tool_result 事件 (data.ok 标志, 与 health_report 同源),
// 按 session 字段过滤当前会话; legacy 事件(session 为空)计入 legacy 会话。
// 输出: 总调用/成功率 + 各 gate(lang) 调用数与成功率 + 失败 Top。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// gateStat 单个 gate 的统计
type gateStat struct {
	Lang    string
	Calls   int
	OK      int
	Fail    int
	LastErr string
}

// sessionStats 会话级统计结果
type sessionStats struct {
	SessionID string
	Total     int
	OK        int
	Fail      int
	Gates     []gateStat // 按调用数降序
}

// eventsFilePath 当前事件日志路径 (按会话目录)
func eventsFilePath(workDir string) string {
	if id := currentSession(); id != "" {
		return filepath.Join(sessionDir(workDir, id), "events.jsonl")
	}
	return filepath.Join(workDir, ".forge", "events.jsonl")
}

// collectSessionStats 扫描事件日志, 统计当前会话的 gate 调用。
// 事件无 session 字段(legacy)且当前为 legacy 模式 → 计入。
// 事件 session 字段匹配当前会话 → 计入。其他 → 跳过。
// 永不 panic, 读失败返回空统计。
func collectSessionStats(workDir string) *sessionStats {
	out := &sessionStats{SessionID: currentSession()}
	if workDir == "" {
		return out
	}
	path := eventsFilePath(workDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	sid := currentSession()
	byLang := make(map[string]*gateStat)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev Event
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != EvToolResult {
			continue
		}
		// 会话过滤: 事件 session 非空且不等于当前 → 跳过
		if ev.Session != "" && ev.Session != sid {
			continue
		}
		ok := true
		if m, okm := ev.Data.(map[string]interface{}); okm {
			if v, okv := m["ok"]; okv {
				if b, okb := v.(bool); okb {
					ok = b
				}
			}
		}
		lang := ev.Detail
		if lang == "" {
			lang = "unknown"
		}
		st := byLang[lang]
		if st == nil {
			st = &gateStat{Lang: lang}
			byLang[lang] = st
		}
		st.Calls++
		if ok {
			st.OK++
			out.OK++
		} else {
			st.Fail++
			out.Fail++
			if st.LastErr == "" && ev.Detail != "" {
				st.LastErr = truncateCN(ev.Detail, 40)
			}
		}
		out.Total++
	}
	for _, st := range byLang {
		out.Gates = append(out.Gates, *st)
	}
	sort.Slice(out.Gates, func(i, j int) bool {
		if out.Gates[i].Calls != out.Gates[j].Calls {
			return out.Gates[i].Calls > out.Gates[j].Calls
		}
		return out.Gates[i].Lang < out.Gates[j].Lang
	})
	return out
}
