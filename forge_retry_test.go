package main

// forge_retry_test.go — 重试/降级判定的哨兵。
//
// 这些判据决定"失败后要不要重试、要不要换语言"。判错的代价不对称:
// 对确定性失败重试 = 白耗时间(且放大成 30+1+30+2+30 秒),
// 对瞬时失败不重试 = 白白失败。

import (
	"context"
	"errors"
	"testing"
)

func TestForgeRetry_CacheTTL(t *testing.T) {
	if got := cacheTTLForLang("go"); got != 600 {
		t.Errorf("cacheTTLForLang(\"go\") = %d, 期望 600", got)
	}
	// 未知语言必须有兜底 TTL(返回 0 会让缓存立刻失效, 等于关掉缓存)
	if got := cacheTTLForLang(""); got == 0 {
		t.Error("未知语言 TTL 不得为 0(会使缓存永不命中)")
	}
}

func TestForgeRetry_IsTimeoutErr(t *testing.T) {
	if isTimeoutErr(errors.New("connection refused")) {
		t.Error("普通错误不应判定为超时")
	}
	if !isTimeoutErr(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded 应判定为超时")
	}
}

func TestForgeRetry_IsTransientError(t *testing.T) {
	// Timeout 标记是瞬时失败的判据之一
	if !isTransientError(ForgeGateResult{Timeout: true}) {
		t.Error("Timeout=true 应判定为瞬时失败")
	}
	// 无任何瞬时特征 -> 不得判为瞬时(否则会无限重试确定性失败)
	if isTransientError(ForgeGateResult{Error: "compile error"}) {
		t.Error("普通编译错误不应判定为瞬时失败")
	}
}

func TestForgeRetry_IsDeterministicFailure(t *testing.T) {
	// 编译阶段的失败是确定性的: 同样的代码重跑必然同样失败
	if !isDeterministicFailure(ForgeGateResult{Stage: "compile", ExitCode: 1}) {
		t.Error("编译阶段失败应判定为确定性失败")
	}
}

func TestForgeRetry_ShouldFallback(t *testing.T) {
	if shouldFallback(ForgeGateResult{OK: true}) {
		t.Error("成功的结果不得触发降级")
	}
}

func TestForgeRetry_PickFallback(t *testing.T) {
	// python 有降级目标 node; go 没有(返回空串表示无降级)
	if got := pickFallback("python"); got != "node" {
		t.Errorf("pickFallback(\"python\") = %q, 期望 \"node\"", got)
	}
	if got := pickFallback("go"); got != "" {
		t.Errorf("pickFallback(\"go\") = %q, 期望空串", got)
	}
}

func TestForgeRetry_ValidGateJSON(t *testing.T) {
	if !validGateJSON(`{"a":1}`) {
		t.Error("合法 JSON 对象应判定为 true")
	}
	if validGateJSON("nope") {
		t.Error("非 JSON 应判定为 false")
	}
}

func TestForgeRetry_ParseGateReject(t *testing.T) {
	ok, reason := parseGateReject(`{"rejected":true,"reason":"why"}`)
	if !ok || reason != "why" {
		t.Errorf("= (%v,%q), 期望 (true,\"why\")", ok, reason)
	}
	// rejected=false 与非法输入都必须返回 false(调用方据此决定是否采纳拒绝)
	if ok, _ := parseGateReject(`{"rejected":false}`); ok {
		t.Error("rejected=false 不得判定为拒绝")
	}
	if ok, _ := parseGateReject("plain text"); ok {
		t.Error("非 JSON 不得判定为拒绝")
	}
	// error 字段是 reason 的兜底
	if _, r := parseGateReject(`{"rejected":true,"error":"e"}`); r != "e" {
		t.Errorf("reason 缺失时应回落到 error 字段, 得到 %q", r)
	}
}
