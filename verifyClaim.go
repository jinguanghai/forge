package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// ─── 方案B: 虚报检测 gate (防幻觉虚报) ─────────────────────────
var verifiableClaimRe = regexp.MustCompile(
	`(?i)\b(git\s+commit|committed|pushed|merged|clean|build\s+pass|test\s+pass|go\s+build)\b|` +
		`已提交|已合并|已推送|已落地|已部署|已回滚|编译通过|构建通过|测试通过|验证通过|全绿|` +
		`\b[0-9a-f]{7,40}\b`)

func hasToolEvidence(messages []ChatMessage) bool {
	for _, m := range messages {
		if m.Role == "tool" {
			return true
		}
		if len(m.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// lastUserIndex 返回最后一条 user 消息的下标; 无 user 消息返回 -1。
func lastUserIndex(messages []ChatMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return i
		}
	}
	return -1
}

// hasToolEvidenceSinceUser 只统计"最后一条 user 消息之后"的工具证据。
//
// 动机 (20261004 实测打穿): buildStreamMessages 会把 a.history (历次任务的
// assistant/tool 消息) 拼进 messages (agent_stream_loop.go:157-158)。原判据
// 扫全会话 —— 于是"本轮新任务下模型零工具直接声称已提交", 只因历史里旧任务
// 用过工具就被判为有证据 -> 静默漏检。实测证据: 旧任务(user + tool_calls +
// tool) 后接新任务 user, 声称"已提交/测试通过" -> 原实现返回 false (放行)。
//
// 收窄到本轮后该场景被捕获; 无 user 消息时退化为全扫 (兼容历史调用方语义)。
func hasToolEvidenceSinceUser(messages []ChatMessage) bool {
	return hasToolEvidence(messages[lastUserIndex(messages)+1:])
}

// detectUnverifiedClaim 判定"完成态声称但本轮无工具证据"(收窄口径见上)。
func detectUnverifiedClaim(asst string, messages []ChatMessage) bool {
	if verifiableClaimRe.MatchString(asst) {
		return !hasToolEvidenceSinceUser(messages)
	}
	return false
}

func verifyClaimEnabled() bool {
	return os.Getenv("FORGE_VERIFY_CLAIM") != "0"
}

// verifyInterventionMsg 构造"证据先于声称"的强干预消息:
// 检测到虚报后, 把 runVerification() 的真实状态作为 user 消息注入 messages,
// 强制模型在下一轮看到客观证据后修正或坚持 (生成与执行分离原则的收尾防线)。
func verifyInterventionMsg(verif string) string {
	return autoInjectEnvelope("完成态核验", "【证据先于声称】你声称已经完成某项确定性操作（如提交/部署/构建通过/验证通过），"+
		"但本会话没有任何工具执行证据——这属于未经验证的声称。以下是系统对真实状态的独立核验结果，请据此修正你上一轮的结论："+
		verif+"\n\n若上述状态与你声称的不符，请明确指出并给出真实状态；若确认已由工具完成，请补充说明该工具调用的执行输出作为证据。")
}

// maxVerifyStrikes 虚报强干预上限: 最多强制重答 1 次, 防止"模型持续虚报→无限循环"。
const maxVerifyStrikes = 1

func runVerification() string {
	var b bytes.Buffer
	runCmd := func(name string, args ...string) {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			b.WriteString(fmt.Sprintf("[%s %s] %v; ", name, strings.Join(args, " "), err))
			return
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			trimmed = "(clean)"
		}
		b.WriteString(fmt.Sprintf("[%s] %s; ", name, strings.ReplaceAll(trimmed, "\n", " ")))
	}
	runCmd("git", "log", "--oneline", "-5")
	runCmd("git", "status", "--short")
	runCmd("go", "vet", ".")
	if b.Len() == 0 {
		return "(无输出)"
	}
	return b.String()
}
