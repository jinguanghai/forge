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
	Time     string   `json:"time"`                // RFC3339 本地时间
	File     string   `json:"file"`                // 被写入的文件 (memory.json)
	Fields   []string `json:"fields"`              // 顶层字段差异 (改了什么)
	SysHash  string   `json:"sys_hash,omitempty"`  // system 前缀指纹 (前16位)
	Reason   string   `json:"reason,omitempty"`    // 备注 (missing_last_updated 等)
	GuardOff bool     `json:"guard_off,omitempty"` // true=护栏关闭时写入 (留痕但不计配额)
	Src      string   `json:"src,omitempty"`       // 写入路径: exempt=事后认领 (内容未变, 不计配额)
}

const anchorAuditFileName = "anchor_audit.jsonl"

// anchorAuditSrcExempt 审计条目 src 取值: 事后认领 (exempt 不改内容, 故不计配额)。
// 与 Python 出口工具 (.forge/forge-tools/memory_write.py) 同源, 由 anchor_fields_sentinel_test.go 钉住。
const anchorAuditSrcExempt = "exempt"

// anchorExemptWarnThreshold 当日豁免次数达到该值即在摘要中告警。
//
// 豁免(关护栏 / 事后认领)不计配额是设计, 但"不计配额" != "不产生成本": 每次豁免同样
// 改锚点、同样重置缓存前缀。阈值让绕行可见 —— 20261003 实测当日 14 次锚点写入全走豁免,
// 而 anchorAuditTodayCount 返回 0, 摘要只报计配额的那个数 (测量失真: 不可见即不可管理)。
const anchorExemptWarnThreshold = 3

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

// dynamicMemoryFields 动态字段清单 —— 不进 system 固定头的字段 (唯一真相源)。
//
// 两处消费方共用这一份清单, 防止各自维护导致漂移:
//   - buildMemoryTailText : 剔除后进 system 固定头 (记忆锚点段)
//   - isAnchorChange      : 判定是否锚点改动
//
// 20260925: 注释原写 "buildSystemPrompt / stripDynamicMemory" —— 前者早已被
// buildSystemPromptStable 取代(全库无此符号), 后者生产零调用已删; 注释腐化本身
// 就是"接线缺口"的一种, 由 relation gate 扫描捕获。
//
// 与此互补的"锚点集合"不另设白名单 —— 由 isAnchorChange 反推 (非动态即锚点)。
var dynamicMemoryFields = []string{
	// 由 RecallMemory 动态召回
	"key_findings",
	// 由 RecallMemory 动态召回 (独立配额 lessonsRecallTopK; 常驻硬约束见 lessons_core)
	"lessons",
	// 由 compactFoldedIndex 精简注入
	"folded_memory",
	// 变化走 syncDynamicTails 尾部 diff
	"active_task",
	// 时间戳, 每次记忆写入必变
	"last_updated",
}

// isDynamicMemoryField 判断单个字段是否为动态字段 (不进固定头)。
func isDynamicMemoryField(name string) bool {
	for _, d := range dynamicMemoryFields {
		if name == d {
			return true
		}
	}
	return false
}

// isAnchorChange 判断字段差异是否涉及锚点字段 —— fail-safe: 非动态即锚点。
//
// 判据方向于 20260913 反转。原实现用 anchorFields 白名单 (12 个字段), 需人工维护,
// 与实际进固定头的字段集合脱节, 实测漏检 6 个 (architecture/lessons/user_profile/
// swordless_roadmap/evolution_consensus/rescue), 且新增字段默认免检 (fail-open);
// 漏检字段连审计都不留痕 (anchorGuardAudit 首行即 return), 故长期无人发现。
// 现改为反向: 只要存在任一非动态字段 → 判为锚点改动。
// 新增顶层字段自动纳入保护 (fail-safe), 无需同步改此处。
func isAnchorChange(fields []string) bool {
	for _, f := range fields {
		if !isDynamicMemoryField(f) {
			return true
		}
	}
	return false
}

// anchorAuditTodayStats 统计今日锚点条目: total=今日全部锚点改动, counted=其中计配额者;
// total-counted = 今日豁免数 (guard_off 关护栏写入 / src=exempt 事后认领)。
//
// 拦截只看 counted (语义与修正前逐字节一致); total 专供度量与告警 —— 二者同源同遍历,
// 不另写一份判据, 避免拦截与度量各读一遍文件后漂移。
func anchorAuditTodayStats(workDir string) (total, counted int, err error) {
	data, err := os.ReadFile(anchorAuditPath(workDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil // 无审计文件 = 从未写过
		}
		return 0, 0, err
	}
	today := time.Now().Format("2006-01-02")
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e AnchorAuditEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if !strings.HasPrefix(e.Time, today) {
			continue
		}
		total++
		if !e.GuardOff && e.Src != anchorAuditSrcExempt {
			counted++
		}
	}
	return total, counted, nil
}

// anchorAuditTodayCount 今日计配额次数 (护栏拦截判据)。豁免语义见 anchorAuditTodayStats:
//   - GuardOff (护栏关闭时写入) —— 关护栏=不占配额, 语义与修正前一致;
//   - Src=exempt (事后认领) —— 内容未变, 补留痕不该挤占当日改动额度。
//
// 20261003 背景: Python 出口工具曾把 GuardOff 硬编码 true, 于是走该工具的写入全部
// 落进上面第一条豁免 → 配额计数恒 0, 护栏被合规工具绕过 (名实不符: 审计还显 [护栏关闭])。
func anchorAuditTodayCount(workDir string) (int, error) {
	_, counted, err := anchorAuditTodayStats(workDir)
	return counted, err
}

// anchorGuardCheck 频率拦截: 同一天第 2 次写锚点 → 返回错误。
// 错误信息带缓存成本提示, 模型收到后应合并改动或改天再写。
func anchorGuardCheck(workDir string) error {
	if !anchorGuardEnabled() {
		return nil
	}
	counted, err := anchorAuditTodayCount(workDir)
	if err != nil {
		return nil // 审计文件损坏不阻断写入 (宁缺毋滥)
	}
	if counted >= 1 {
		// 提示里的"今日已写"必须是实际改动量: 只报计配额数会让模型低估已付出的缓存成本
		// (实测某日实际 14 次而计配额 1 次, 拦截文案却称"今日已写 1 次")。
		total, _, _ := anchorAuditTodayStats(workDir)
		return fmt.Errorf("锚点写入护栏: 今日锚点改动 %d 次 (其中计配额 %d 次), 禁止第 %d 次 (缓存将重置, 未命中价是命中价 30 倍)。请合并改动到一次写入, 或设 FORGE_ANCHOR_GUARD=0 关闭护栏", total, counted, counted+1)
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
//
// 20260925 修正: 原实现首行 `if !anchorGuardEnabled() { return }` —— 关护栏不只是
// 放行写入, 还让审计彻底静默("关掉护栏即无人知道发生过写入", 属失效不留痕家族)。
// 现改为: 关护栏照写审计并打 GuardOff 标记, 该条目不占当日配额
// (anchorAuditTodayCount 跳过), 故护栏关闭时的放行语义逐字节不变。
func anchorGuardAudit(workDir string, fields []string, reason string) {
	if !isAnchorChange(fields) {
		return
	}
	guardOff := !anchorGuardEnabled()
	if guardOff {
		if reason == "" {
			reason = "guard_disabled"
		} else {
			reason += " guard_disabled"
		}
	}
	entry := AnchorAuditEntry{
		Time:     time.Now().Format(time.RFC3339),
		File:     "memory.json",
		Fields:   fields,
		Reason:   reason,
		GuardOff: guardOff,
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
	// 今日单列 + 豁免可见: 拦截只看计配额数, 但绕行同样烧缓存, 必须让主人看见实际改动量。
	if tTotal, tCounted, terr := anchorAuditTodayStats(workDir); terr == nil && tTotal > 0 {
		sb.WriteString(fmt.Sprintf("  今日: 锚点改动 %d 次 (计配额 %d / 豁免 %d)\n", tTotal, tCounted, tTotal-tCounted))
		if tTotal-tCounted >= anchorExemptWarnThreshold {
			sb.WriteString(fmt.Sprintf("  ⚠ 今日豁免 %d 次 —— 配额被绕行, 缓存重置成本按 %d 次计 (豁免不占配额 != 无成本)\n", tTotal-tCounted, tTotal))
		}
	}
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
		if e.GuardOff {
			sb.WriteString(" [护栏关闭]")
		}
		if e.SysHash != "" {
			sb.WriteString(fmt.Sprintf(" sha=%s", e.SysHash))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
