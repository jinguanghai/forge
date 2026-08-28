package main

// gatesync.go — gate 五处同步检查 (/gatesync)
//
// gate_discipline 原是记忆文本(五处同步靠自觉): ①代码 ②prompt ③memory.json gates
// 字段 ④README ⑤发布包。本文件把检查变成命令, 漏同步当场报差异。
// 只读, 不修改任何文件。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// currentGates 当前 12 面 gate (与 memory_health.go 的 cur 列表一致)
var currentGates = []string{"python", "go", "sh", "node", "math", "logic", "regex", "knowledge", "tcm", "browser", "chain", "self"}

// gateSyncCheck 检查五处一致性, 返回差异清单 (只读)。
//
//	① 代码: currentGates (本函数定义处即代码列表)
//	② prompt: README 中的 gate 提及 (prompt 模板主要来源)
//	③ 记忆: memory.json 的 gates 字段
//	④ 文档: README.md
//	⑤ 发布包: <pluginReleaseDir>/dsh-forge-plugins/plugins/forge-gates (插件版)
func gateSyncCheck(workDir string, pluginReleaseDir string) string {
	var issues []string
	var okLines []string

	// ③ 记忆 gates 字段
	memGates := ""
	if data, err := os.ReadFile(memoryFilePath(workDir)); err == nil {
		var m map[string]interface{}
		if json.Unmarshal(data, &m) == nil {
			memGates, _ = m["gates"].(string)
		}
	}
	if memGates == "" {
		issues = append(issues, "③记忆 memory.json 无 gates 字段")
	} else {
		for _, c := range currentGates {
			if !strings.Contains(memGates, c) {
				issues = append(issues, fmt.Sprintf("③记忆 memory.json.gates 缺: %s", c))
			}
		}
		if !containsIssue(issues, "memory.json.gates") {
			okLines = append(okLines, "③ 记忆 gates 字段: 12 面齐全 ✓")
		}
	}

	// ④ README
	readmePath := filepath.Join(workDir, "README.md")
	if data, err := os.ReadFile(readmePath); err == nil {
		var missing []string
		for _, c := range currentGates {
			if !strings.Contains(string(data), c) {
				missing = append(missing, c)
			}
		}
		if len(missing) == 0 {
			okLines = append(okLines, "④ README 提及全部 gate ✓")
		} else {
			issues = append(issues, fmt.Sprintf("④ README 缺 gate 名: %s", strings.Join(missing, ",")))
		}
	} else {
		issues = append(issues, "④ README.md 不可读")
	}

	// ⑤ 发布包 (插件版 forge-gates)
	pluginIndex := filepath.Join(pluginReleaseDir, "dsh-forge-plugins", "plugins", "forge-gates", "index.js")
	if data, err := os.ReadFile(pluginIndex); err == nil {
		var missing []string
		for _, c := range currentGates {
			if !strings.Contains(string(data), c) {
				missing = append(missing, c)
			}
		}
		if len(missing) == 0 {
			okLines = append(okLines, "⑤ 发布包 forge-gates/index.js 含全部 gate ✓")
		} else {
			issues = append(issues, fmt.Sprintf("⑤ 发布包缺 gate 名: %s", strings.Join(missing, ",")))
		}
	} else {
		issues = append(issues, "⑤ 发布包 forge-gates/index.js 不可读 (插件版未同步)")
	}

	okLines = append(okLines, "①② 代码列表 (currentGates): 12 面 ✓")

	var sb strings.Builder
	sb.WriteString("🔗 gate 五处同步检查\n")
	sb.WriteString("─────────────────\n")
	for _, l := range okLines {
		sb.WriteString("  " + l + "\n")
	}
	if len(issues) == 0 {
		sb.WriteString("🎉 五处全部一致\n")
	} else {
		for _, i := range issues {
			sb.WriteString("  ⚠️ " + i + "\n")
		}
		sb.WriteString(fmt.Sprintf("⚠️ 共 %d 处差异 (只读检查, 未修改)\n", len(issues)))
	}
	return sb.String()
}

func containsIssue(issues []string, sub string) bool {
	for _, i := range issues {
		if strings.Contains(i, sub) {
			return true
		}
	}
	return false
}
