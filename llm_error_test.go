package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestLLMError_RetryableStatuses(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 504} {
		if !isRetryable(&LLMError{StatusCode: code}) {
			t.Errorf("HTTP %d 应可重试", code)
		}
	}
}

func TestLLMError_NonRetryableStatuses(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 422} {
		if isRetryable(&LLMError{StatusCode: code}) {
			t.Errorf("HTTP %d 不应可重试 (重试无意义且计费)", code)
		}
	}
}

// 半截输出重试 = 用户看到重复内容。这是硬红线, 包裹后也必须识别。
func TestLLMError_PartialStreamNeverRetried(t *testing.T) {
	if isRetryable(errPartialStream) {
		t.Fatal("errPartialStream 必须不可重试")
	}
	if isRetryable(fmt.Errorf("read stream: %w", errPartialStream)) {
		t.Fatal("errors.Is 包裹后仍应识别为不可重试")
	}
}

// 零输出截断重发安全: 前缀未变, 缓存仍命中。
func TestLLMError_TruncatedBeforeOutputIsRetryable(t *testing.T) {
	if !isRetryable(errStreamTruncated) {
		t.Fatal("errStreamTruncated 应可重试")
	}
}

func TestLLMError_NilNotRetryable(t *testing.T) {
	if isRetryable(nil) {
		t.Fatal("nil 不应判为可重试")
	}
}

func TestLLMError_NetworkErrorRetryable(t *testing.T) {
	if !isRetryable(errors.New("dial tcp 127.0.0.1:443: connectex: connection refused")) {
		t.Fatal("网络错误应可重试")
	}
}

func TestLLMError_401MessageIsChineseHint(t *testing.T) {
	got := (&LLMError{StatusCode: 401, Message: "Authentication Fails"}).Error()
	if !strings.Contains(got, "401") || !strings.Contains(got, "API 密钥") {
		t.Fatalf("401 应给出中文密钥提示, 实际: %s", got)
	}
}

func TestLLMError_PlainStatusMessage(t *testing.T) {
	got := (&LLMError{StatusCode: 500, Message: "server error"}).Error()
	if got != "LLM HTTP 500: server error" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestLLMError_NoStatusFallsBackToMessage(t *testing.T) {
	if got := (&LLMError{Message: "boom"}).Error(); got != "boom" {
		t.Fatalf("无状态码应直接返回 Message, 实际 %q", got)
	}
}
