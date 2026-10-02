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

// currentGates 当前 gate 清单 —— 引用唯一源 铸剑炉_GATES, 不再保留副本。
var currentGates = 铸剑炉_GATES

// pluginUnpublishedGates 有意不进入公开发布包的 gate (插件版 forge-gates)。
//
// sh 已于 20261001 退役 (实测失败率 48.2%), 不在 currentGates 中, 故本清单也不再列它。
//
// 显式声明的作用: ⑤ 从"永远报警"变成"可判定" —— 报警只对"本该发布却漏了"的
// gate 响, 有意不发布的在这里留档, 理由逐条可审计。
//
//	media    : 依赖 MiniMax 私有 API 密钥与账号, 第三方无密钥不可用
//	tcm      : 私有中医域 (家传理论/药对数据不入公开仓库)
//	browser  : 依赖 playwright, 发布环境通常未装 → 发布也不可用
//	self     : 自改源码 gate, 公开有风险
//	relation : Python 实现依赖本地源码扫描, 发布需移植为 JS (待定)
var pluginUnpublishedGates = []string{"tcm", "browser", "self", "relation", "media"}

// hasGateToken 检查 data 中是否出现"独立的" gate 名 (词边界判定)。
//
// 不用裸子串: 短名(如 sh)会被 push/shell/finish 之类无关词假命中 →
// 该面等于永远不报警。发布包 index.js 的写法是 gate: 'x', fixture 用裸名列表,
// 词边界判定对两种写法都成立。
func hasGateToken(data, name string) bool {
	for i := 0; ; {
		j := strings.Index(data[i:], name)
		if j < 0 {
			return false
		}
		j += i
		var l, r byte = ' ', ' '
		if j > 0 {
			l = data[j-1]
		}
		if j+len(name) < len(data) {
			r = data[j+len(name)]
		}
		if !isWordByte(l) && !isWordByte(r) {
			return true
		}
		i = j + len(name)
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// isPluginUnpublished 判定该 gate 是否"有意不发布"。
func isPluginUnpublished(name string) bool {
	for _, g := range pluginUnpublishedGates {
		if g == name {
			return true
		}
	}
	return false
}

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
			okLines = append(okLines, fmt.Sprintf("③ 记忆 gates 字段: %d 面齐全 ✓", len(currentGates)))
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
		shouldPublish := 0
		for _, c := range currentGates {
			if isPluginUnpublished(c) {
				continue
			}
			shouldPublish++
			if !hasGateToken(string(data), c) {
				missing = append(missing, c)
			}
		}
		if len(missing) == 0 {
			okLines = append(okLines, fmt.Sprintf(
				"⑤ 发布包 forge-gates/index.js: 应发布 %d 面齐全 ✓ (有意不发布 %d 面: %s)",
				shouldPublish, len(pluginUnpublishedGates), strings.Join(pluginUnpublishedGates, ",")))
		} else {
			issues = append(issues, fmt.Sprintf("⑤ 发布包缺 gate 名: %s", strings.Join(missing, ",")))
		}
	} else {
		issues = append(issues, "⑤ 发布包 forge-gates/index.js 不可读 (插件版未同步)")
	}

	okLines = append(okLines, fmt.Sprintf("①② 代码列表 (currentGates): %d 面 ✓", len(currentGates)))

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
