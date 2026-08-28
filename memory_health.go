package main

// memory_health.go — 记忆体检 (/memhealth)
//
// 检测项: ① UTF-8 有效性 (乱码) ② JSON 结构 ③ 必填锚点字段 ④ 过时条目 (已删 gate)
// 输出: 体检报告 (异常项 + 建议)。只读, 不修改记忆。
// 目标: 给 memory.json 一个"外部把关" (之前靠"坏到发现才修")。

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// memHealthLint 写入前体检: 分层检查, 不过禁止写。
//
//	硬检查 (永远执行): UTF-8 有效 + JSON 合法——防乱码/防写坏文件。
//	软检查 (仅主记忆结构): 数据含 axioms 或 self_governance 时视为完整主记忆,
//	要求必填锚点字段齐全; 会话级片段 (如仅 key_findings) 不适用完整锚点要求。
//
// 由 SaveMemory 在写前调用——把"记忆没梳理好就是噪音"从纪律文本变成程序检查。
func memHealthLint(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("写入前体检: UTF-8 校验失败 (GBK 乱码风险), 拒绝写入")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("写入前体检: JSON 解析失败: %v", err)
	}
	_, hasAxioms := m["axioms"]
	_, hasGovernance := m["self_governance"]
	if hasAxioms || hasGovernance {
		for _, f := range []string{"identity", "role", "language", "working_dir", "gates", "axioms", "self_governance"} {
			if _, ok := m[f]; !ok {
				return fmt.Errorf("写入前体检: 主记忆必填锚点字段缺失: %s (记忆不完整, 拒绝写入)", f)
			}
		}
	}
	return nil
}

// memHealthReport 生成记忆体检报告。只读, 永不 panic。
func memHealthReport(workDir string) string {
	var issues []string
	var infos []string

	data, recovered, err := LoadMemory(workDir)
	if err != nil {
		return fmt.Sprintf("❌ 记忆不可读: %v", err)
	}
	if recovered {
		infos = append(infos, "ℹ️ 当前数据来自 .bak 恢复 (主文件曾损坏)")
	}

	// ① UTF-8 有效性
	if !utf8.Valid(data) {
		issues = append(issues, "🔴 UTF-8 校验失败: 存在非法字节 (GBK 乱码风险), 需重写修复")
	} else {
		infos = append(infos, "✅ UTF-8 有效")
	}

	// ② JSON 结构
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		issues = append(issues, fmt.Sprintf("🔴 JSON 解析失败: %v", err))
		return strings.Join(append(infos, issues...), "\n")
	}
	infos = append(infos, "✅ JSON 结构有效")

	// ③ 必填锚点字段 (memStableOrder 核心)
	required := []string{"identity", "role", "language", "working_dir", "gates", "axioms", "self_governance"}
	for _, f := range required {
		if _, ok := m[f]; !ok {
			issues = append(issues, fmt.Sprintf("🟡 必填字段缺失: %s", f))
		}
	}

	// ④ 过时条目: key_findings 含已删 gate 名
	// 注意: 排除否定上下文 (提到"已移除/已裁剪/降级"是说明, 不是依赖)
	goneGates := []string{"eprover", "repair_gate", "system_gate", "deno", "rust", "tcc"}
	negCtx := []string{"已移除", "已裁剪", "降级", "裁剪后", "裁剪"}
	if kfs, ok := m["key_findings"].([]interface{}); ok {
		for _, kf := range kfs {
			if item, ok := kf.(map[string]interface{}); ok {
				title, _ := item["title"].(string)
				content, _ := item["content"].(string)
				for _, g := range goneGates {
					if strings.Contains(title, g) {
						issues = append(issues, fmt.Sprintf("🟡 key_findings 标题含已删 gate: %s", title))
						break
					}
					if strings.Contains(content, g) {
						isNeg := false
						for _, n := range negCtx {
							if strings.Contains(content, n) {
								isNeg = true
								break
							}
						}
						if !isNeg {
							issues = append(issues, fmt.Sprintf("🟡 key_findings 依赖已删 gate: %s (含 %q)", title, g))
							break
						}
					}
				}
			}
		}
	}

	// ⑤ gates 字段一致性: 应含当前 12 面
	if g, ok := m["gates"].(string); ok {
		cur := []string{"python", "go", "sh", "node", "math", "logic", "regex", "knowledge", "tcm", "browser", "chain", "self"}
		for _, c := range cur {
			if !strings.Contains(g, c) {
				issues = append(issues, fmt.Sprintf("🟡 gates 字段缺当前面: %s", c))
			}
		}
	}

	// 汇总
	var sb strings.Builder
	sb.WriteString("📋 记忆体检报告\n")
	sb.WriteString("─────────────\n")
	for _, i := range infos {
		sb.WriteString(i + "\n")
	}
	if len(issues) == 0 {
		sb.WriteString("🎉 无异常: 记忆健康\n")
	} else {
		for _, i := range issues {
			sb.WriteString(i + "\n")
		}
		sb.WriteString(fmt.Sprintf("⚠️ 共 %d 项待处理 (只读报告, 未修改)\n", len(issues)))
	}
	sb.WriteString("建议: 异常项可让铸剑炉按三公理重写记忆 (原子写自动 .bak)\n")
	return sb.String()
}
