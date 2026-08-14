package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── 输入护栏 (DMAIC I3 + 二期: 三级分级 + L1-L4 反制对齐) ───
// 级别:
//   medium   → L1 记录 + L2 阻断 (注入/规则覆盖企图)
//   high     → L1 记录 + L2 阻断 (明显越权)
//   critical → L1 记录 + L2 阻断 + LLM 独立二级裁决 (破坏性/攻击性, 裁决通过才放行)
//
// 反制体系对齐: L1=guard_log.jsonl 全量记录 / L2=确定性词库阻断 / L3=critical 拒绝时守门人报警 / L4=日志即取证档案
// 设计原则: 宁缺毋滥——只留高置信规则, 避免误伤主人正常指令。
// 主人如确属正当需求(如防御演练), 可重新表述后继续。

type guardRule struct {
	kind  string   // 原因类别
	level string   // medium | high | critical
	words []string
}

var guardRules = []guardRule{
	// 注入: 试图覆盖系统规则 / 提取 system prompt (medium)
	{"注入", "medium", []string{"忽略之前", "忽略所有", "忘记你的规则", "忘记所有指令", "ignore previous", "ignore all", "disregard"}},
	{"注入", "medium", []string{"泄露你的系统提示", "输出你的系统提示", "重复你的system", "reveal your system", "show your system prompt"}},
	// 越权: 破坏性系统操作 (critical → LLM 独立复核)
	{"越权", "critical", []string{"格式化磁盘", "格式化c盘", "format c:", "删除所有文件", "删库", "drop database", "rm -rf /", "del /s /q", "清空所有数据"}},
	// 攻击性/非法操作 (critical → LLM 独立复核) (红线: 永不反向攻击/永不探测未授权目标)
	{"越权", "critical", []string{"攻击服务器", "反向攻击", "破解密码", "探测未授权", "扫描未授权"}},
}

// 求知前缀: 纯知识咨询(什么是/科普/解释等)直接放行, 防误伤学习类提问
var studyPrefixes = []string{"什么是", "科普", "介绍一下", "介绍", "讲讲", "解释", "了解", "学习", "what is", "what are", "how does", "how to prevent"}

func isStudyQuery(input string) bool {
	low := strings.ToLower(strings.TrimSpace(input))
	for _, p := range studyPrefixes {
		if strings.HasPrefix(low, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// checkInputGuard 返回 (是否阻断, 原因类别, 命中词, 级别)
func checkInputGuard(input string) (bool, string, string, string) {
	if isStudyQuery(input) {
		return false, "", "", ""
	}
	low := strings.ToLower(input)
	for _, r := range guardRules {
		for _, w := range r.words {
			if strings.Contains(low, strings.ToLower(w)) {
				return true, r.kind, w, r.level
			}
		}
	}
	return false, "", "", ""
}

// ─── 守卫事件日志 (L1 记录 / L4 取证档案) ───
var guardLogMu sync.Mutex

// guardVerdictStr 将复核结果映射为日志用裁决值
func guardVerdictStr(allowed bool, err error) string {
	if err != nil {
		return "error"
	}
	if allowed {
		return "allow"
	}
	return "deny"
}

// logGuardEvent 追加一条守卫事件到 .forge/guard_log.jsonl (运行时文件, 不入基线)
func logGuardEvent(workDir, level, kind, hit, verdict, note, input string) {
	if workDir == "" {
		return
	}
	guardLogMu.Lock()
	defer guardLogMu.Unlock()
	dir := filepath.Join(workDir, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	// 输入脱敏: 只记录前 80 字
	in := input
	if r := []rune(in); len(r) > 80 {
		in = string(r[:80]) + "..."
	}
	entry := fmt.Sprintf(`{"ts":%q,"level":%q,"kind":%q,"hit":%q,"verdict":%q,"note":%q,"input":%q}`+"\n",
		time.Now().Format("2006-01-02T15:04:05"), level, kind, hit, verdict, note, in)
	f, err := os.OpenFile(filepath.Join(dir, "guard_log.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(entry)
}
