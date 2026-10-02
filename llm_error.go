package main

import (
	"errors"
	"fmt"
)

// ─── Retryable errors ──────────────────────────────────────

var (
	retryableHTTPStatuses = map[int]bool{
		429: true, // rate limited
		500: true,
		502: true,
		503: true,
		504: true,
	}
)

// ─── LLMError ───────────────────────────────────────────────

type LLMError struct {
	StatusCode int
	Body       string
	Message    string
	Type       string
}

func (e *LLMError) Error() string {
	if e.StatusCode > 0 {
		// 401: 给用户友好的中文提示
		if e.StatusCode == 401 {
			hint := "请设置正确的 API 密钥 (DEEPSEEK_API_KEY 或 LLM_API_KEY 环境变量，或在 .env 文件中配置)"
			return fmt.Sprintf("LLM HTTP 401 (鉴权失败): %s", hint)
		}
		return fmt.Sprintf("LLM HTTP %d: %s", e.StatusCode, e.Message)
	}
	return e.Message
}

// errPartialStream is returned when an SSE stream breaks after some events
// were already emitted. Retrying would replay the emitted content to the user,
// so it is treated as non-retryable.
var errPartialStream = errors.New("stream interrupted after partial output")

// errStreamTruncated is returned when the SSE stream ends without [DONE] and
// without any finish_reason while NO event was emitted yet. Nothing was shown
// to the user, so replaying the request is safe and cheap (identical message
// prefix -> prompt cache still hits).
var errStreamTruncated = errors.New("stream truncated before any output")

func isRetryable(err error) bool {
	if errors.Is(err, errPartialStream) {
		return false
	}
	if err == nil {
		return false
	}
	if le, ok := err.(*LLMError); ok {
		return retryableHTTPStatuses[le.StatusCode]
	}
	// Network errors are retryable
	return true
}
