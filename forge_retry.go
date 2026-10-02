package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// cacheTTLForLang 返回缓存 TTL 秒数: 慢外部 gate 用长 TTL 提升复用率。
// 其余 gate 保持默认 600s。
func cacheTTLForLang(lang string) int {
	switch lang {
	case "knowledge", "browser", "tcm":
		return 1800
	}
	return 600
}

// isTransientError returns true for errors that are likely temporary
// (timeouts, network issues, tool not installed) and should not be cached.
// isTimeoutErr 确定性判定超时：runWithTimeout 在 ctx 到期时返回 ctx.Err()，
// 即 context.DeadlineExceeded。用 errors.Is 判定而非文本匹配 —— 子进程可能
// 在超时前已输出 stderr，文本化后的 Error 字段并不可靠。
func isTimeoutErr(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// isTransientError 判定「环境瞬时失败」(唯一用途: 决定失败结果是否写入缓存)。
// 确定性优先: 超时看 Timeout 字段(errors.Is 判定, 见 isTimeoutErr), 不看文本。
// 旧实现纯文本匹配 stderr —— 用户代码打印含 "connection"/"busy" 等词即被误判为瞬时,
// 真实失败不写缓存, 下次同样代码白跑一遍。
func isTransientError(r ForgeGateResult) bool {
	if r.Timeout || r.EnvFailure {
		return true
	}
	// 执行阶段的失败 = 用户代码自身失败, 与环境瞬时无关 → 一律可缓存。
	if r.Stage == "execute" {
		return false
	}
	// 编译/启动/路径解析阶段的非环境失败 = 代码文本决定, 必然复现 → 可缓存。
	// 旧实现在这里做文本匹配("timeout"/"not found"/"http error"/"connection"/
	// "busy"/"shutting down"), 与 shouldFallback 同一病灶: 把文本当类型用。
	// 环境性失败的产生点已全部打标 EnvFailure, 文本匹配一并删除。
	return false
}

// isDeterministicFailure 判定「同一份代码必然复现的失败」—— 重试零收益。
//
// 判据复用 isTransientError (环境瞬时的单一判据) 的补集, 并限定在编译阶段:
// 执行阶段的失败可能是外部依赖抖动 (网络/文件锁), 重试仍有价值; 而编译阶段的
// 非环境失败完全由代码文本决定, 重跑只会得到同一个错误。
//
// 与 Timeout 的分工: Timeout 是「预算已烧完」(重试要再烧一遍); 本判据是
// 「结果已确定」(重试连结果都不会变)。两者都不该重试, 但原因不同。
func isDeterministicFailure(r ForgeGateResult) bool {
	if r.OK || r.Timeout || r.Stage != "compile" {
		return false
	}
	return !isTransientError(r)
}

// shouldFallback returns true when a forge error is likely environmental
// (missing tool, timeout) and a fallback language might succeed.
//
// 判据全部类型化(EnvFailure / Timeout), 不看错误文本 —— 文本不是类型。
// 旧实现用 strings.Contains 嗅探 "timeout"/"not found"/"找不到"/
// "unsupported language", 实测三重失效(见 EnvFailure 字段注释)。
// 反例已钉入 TestShouldFallback: 错误文本含环境性关键词但未打标 → 不得换语言。
func shouldFallback(r ForgeGateResult) bool {
	if r.OK {
		return false
	}
	// 超时不换语言：同一份工作量换语言重跑会再烧一个超时周期。
	if r.Timeout {
		return false
	}
	return r.EnvFailure
}

// pickFallback suggests an alternative language when the primary fails.
func pickFallback(lang string) string {
	switch lang {
	case "python":
		return "node" // python failed → try node
	case "node", "js":
		return "python"
	default:
		return ""
	}
}

// validGateJSON reports whether code is a well-formed single JSON object.
// Text that merely STARTS with '{' but is invalid JSON (e.g. multi-line
// literals with raw newlines inside string values) is treated as raw input
// and gets re-wrapped by the caller so embedded newlines are JSON-escaped.
func validGateJSON(code string) bool {
	t := strings.TrimSpace(code)
	return strings.HasPrefix(t, "{") && json.Valid([]byte(t))
}

// parseGateReject 从 gate 的 stdout 提取「主动拒绝」标记。
// 类型化解析而非文本嗅探: 只有结构化字段 rejected:true 才算拒绝,
// 错误信息里出现 "rejected" 字样不算。
func parseGateReject(out string) (bool, string) {
	t := strings.TrimSpace(out)
	if !strings.HasPrefix(t, "{") {
		return false, ""
	}
	var v struct {
		Rejected bool   `json:"rejected"`
		Reason   string `json:"reason"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal([]byte(t), &v); err != nil || !v.Rejected {
		return false, ""
	}
	if v.Reason == "" {
		v.Reason = v.Error
	}
	return true, v.Reason
}

func (f *Forge) retryGate(code, lang, input string) ForgeGateResult {
	result := f.forgeGateSkipCache(code, lang, input, false)
	f.auditAttempt(lang, 1, result)
	// 超时不重试：重试一个已经跑满超时的任务，大概率再跑满一次。
	// 确定性失败同样不重试：编译阶段的语法/类型错误由代码文本决定, 重跑同一份代码
	// 必然复现 (实测 compile 失败 177 条中 167 条白跑 2 次重试)。
	if result.OK || f.retryMax <= 1 || result.Timeout || isDeterministicFailure(result) {
		return result
	}
	// Clear cache for this key so retries actually re-execute
	cacheKey := f.cacheKey(code, lang, input)
	f.cacheMu.Lock()
	f.removeCacheKeyLocked(cacheKey)
	f.cacheMu.Unlock()
	for attempt := 1; attempt < f.retryMax; attempt++ {
		time.Sleep(f.retryBackoff * time.Duration(1<<uint(attempt-1)))
		retryResult := f.forgeGateSkipCache(code, lang, input, true)
		f.auditAttempt(lang, attempt+1, retryResult)
		if retryResult.OK {
			retryResult.Retries = attempt
			// Persist the successful retry so future identical calls hit the cache.
			f.cacheResult(f.cacheKey(code, lang, input), retryResult)
			return retryResult
		}
		result = retryResult
		// 循环内同样不重试超时/确定性失败：入口检查只覆盖第 1 次尝试，若第 1 次是
		// 普通失败、第 2 次才跑满超时，继续下一轮只会再烧一个超时周期(实测残留
		// 3 条: go 162.5s / self 72.4s / self 161.8s, 均 retries=2)。
		if retryResult.Timeout || isDeterministicFailure(retryResult) {
			result.Retries = attempt
			return result
		}
		// Clear cache again before next retry
		f.cacheMu.Lock()
		delete(f.cache, cacheKey)
		f.cacheMu.Unlock()
	}
	result.Retries = f.retryMax - 1
	return result
}
