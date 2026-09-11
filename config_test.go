package main

import (
	"strings"
	"testing"
	"time"
)

// ─── LoadConfig: 环境变量注入 ────────────────────────────────
// 注意: godotenv.Load 不覆盖已存在的环境变量, t.Setenv 设的值优先;
// 空字符串也能压住 .env 中的真实值。

func TestLoadConfig_MissingAPIKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected error when API key missing")
	}
	if !strings.Contains(err.Error(), "API key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "test-key")
	// 压住 .env 真实覆盖项, 验证出厂默认
	t.Setenv("DEEPSEEK_BASE_URL", "")
	t.Setenv("DEEPSEEK_MODEL", "")
	t.Setenv("DEEPSEEK_MODEL_FLASH", "")
	t.Setenv("DEEPSEEK_MODEL_PRO", "")
	t.Setenv("DEEPSEEK_ROUTER", "")
	t.Setenv("FORGE_SHOW_REASONING", "")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.APIKey != "test-key" {
		t.Errorf("APIKey = %q, want test-key", cfg.APIKey)
	}
	if cfg.BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("BaseURL = %q, want default deepseek", cfg.BaseURL)
	}
	if cfg.RouterMode != RouterAuto {
		t.Errorf("RouterMode = %q, want %q", cfg.RouterMode, RouterAuto)
	}
	if cfg.ModelFlash == "" || cfg.ModelPro == "" {
		t.Error("ModelFlash/ModelPro must not be empty")
	}
	if cfg.MaxTokens != 131072 {
		t.Errorf("MaxTokens = %d, want 131072", cfg.MaxTokens)
	}
	if cfg.Temperature != 0.7 {
		t.Errorf("Temperature = %v, want 0.7", cfg.Temperature)
	}
	if cfg.MaxConcurrent != 4 {
		t.Errorf("MaxConcurrent = %d, want 4", cfg.MaxConcurrent)
	}
	if cfg.RetryMax != 3 {
		t.Errorf("RetryMax = %d, want 3", cfg.RetryMax)
	}
	if !cfg.ShowReasoning {
		t.Error("ShowReasoning default should be true")
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k")
	t.Setenv("DEEPSEEK_BASE_URL", "https://custom.example.com/v1")
	t.Setenv("DEEPSEEK_MODEL", "my-model")
	t.Setenv("DEEPSEEK_MODEL_FLASH", "flash-x")
	t.Setenv("DEEPSEEK_MODEL_PRO", "pro-x")
	t.Setenv("DEEPSEEK_ROUTER", "pro")
	t.Setenv("LLM_MAX_TOKENS", "9999")
	t.Setenv("LLM_TEMPERATURE", "1.5")
	t.Setenv("LLM_TOP_P", "0.3")
	t.Setenv("LLM_REQUEST_TIMEOUT", "30s")
	t.Setenv("LLM_STREAM_TIMEOUT", "60s")
	t.Setenv("FORGE_TOOL_TIMEOUT", "15s")
	t.Setenv("AGENT_MAX_CONSECUTIVE_FAILS", "7")
	t.Setenv("AGENT_MAX_HISTORY", "100")
	t.Setenv("FORGE_MAX_CONCURRENT", "2")
	t.Setenv("FORGE_CACHE_SIZE", "7")
	t.Setenv("FORGE_RETRY_MAX", "5")
	t.Setenv("FORGE_WORK_DIR", "C:/work")
	t.Setenv("FORGE_SHOW_REASONING", "false")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseURL != "https://custom.example.com/v1" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.Model != "my-model" {
		t.Errorf("Model = %q", cfg.Model)
	}
	if cfg.ModelFlash != "flash-x" || cfg.ModelPro != "pro-x" {
		t.Errorf("flash/pro = %q / %q", cfg.ModelFlash, cfg.ModelPro)
	}
	// DEEPSEEK_MODEL 非空先置 fixed, 再被 ROUTER=pro 覆盖
	if cfg.RouterMode != RouterPro {
		t.Errorf("RouterMode = %q, want %q", cfg.RouterMode, RouterPro)
	}
	if cfg.MaxTokens != 9999 {
		t.Errorf("MaxTokens = %d", cfg.MaxTokens)
	}
	if cfg.Temperature != 1.5 {
		t.Errorf("Temperature = %v", cfg.Temperature)
	}
	if cfg.TopP != 0.3 {
		t.Errorf("TopP = %v", cfg.TopP)
	}
	if cfg.RequestTimeout != 30*time.Second {
		t.Errorf("RequestTimeout = %v", cfg.RequestTimeout)
	}
	if cfg.MaxConsecutiveFails != 7 {
		t.Errorf("MaxConsecutiveFails = %d", cfg.MaxConsecutiveFails)
	}
	if cfg.MaxHistoryMessages != 100 {
		t.Errorf("MaxHistoryMessages = %d", cfg.MaxHistoryMessages)
	}
	if cfg.MaxConcurrent != 2 {
		t.Errorf("MaxConcurrent = %d", cfg.MaxConcurrent)
	}
	if cfg.CacheMaxSize != 7 {
		t.Errorf("CacheMaxSize = %d", cfg.CacheMaxSize)
	}
	if cfg.RetryMax != 5 {
		t.Errorf("RetryMax = %d", cfg.RetryMax)
	}
	if cfg.WorkDir != "C:/work" {
		t.Errorf("WorkDir = %q", cfg.WorkDir)
	}
	if cfg.ShowReasoning {
		t.Error("ShowReasoning should be false")
	}
}

func TestLoadConfig_InvalidURL(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k")
	t.Setenv("DEEPSEEK_BASE_URL", "not-a-url")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("expected ErrInvalidURL")
	}
	if !strings.Contains(err.Error(), "invalid API base URL") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfig_RouterModes(t *testing.T) {
	cases := map[string]string{
		"auto":    RouterAuto,
		"flash":   RouterFlash,
		"pro":     RouterPro,
		"fixed":   RouterFixed,
		"garbage": RouterAuto, // 非法值回退 auto
	}
	for in, want := range cases {
		t.Setenv("DEEPSEEK_API_KEY", "k")
		t.Setenv("DEEPSEEK_ROUTER", in)
		t.Setenv("DEEPSEEK_MODEL", "") // 避免 MODEL 非空把模式锁成 fixed
		cfg, err := LoadConfig()
		if err != nil {
			t.Fatalf("router=%s: %v", in, err)
		}
		if cfg.RouterMode != want {
			t.Errorf("router=%s -> mode=%q, want %q", in, cfg.RouterMode, want)
		}
	}
}

func TestLoadConfig_Clamp(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "k")
	t.Setenv("FORGE_CACHE_SIZE", "999999")
	t.Setenv("AGENT_MAX_HISTORY", "99999")
	t.Setenv("LLM_TEMPERATURE", "9.9")
	t.Setenv("FORGE_MAX_CONCURRENT", "0")
	t.Setenv("FORGE_RETRY_MAX", "99")
	t.Setenv("LLM_MAX_TOKENS", "1") // clamp 下限 256

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.CacheMaxSize != 10000 {
		t.Errorf("CacheMaxSize = %d, want 10000 (clamped)", cfg.CacheMaxSize)
	}
	if cfg.MaxHistoryMessages != 500 {
		t.Errorf("MaxHistoryMessages = %d, want 500 (clamped)", cfg.MaxHistoryMessages)
	}
	if cfg.Temperature != 2.0 {
		t.Errorf("Temperature = %v, want 2.0 (clamped)", cfg.Temperature)
	}
	if cfg.MaxConcurrent != 1 {
		t.Errorf("MaxConcurrent = %d, want 1 (clamped)", cfg.MaxConcurrent)
	}
	if cfg.RetryMax != 10 {
		t.Errorf("RetryMax = %d, want 10 (clamped)", cfg.RetryMax)
	}
	if cfg.MaxTokens != 256 {
		t.Errorf("MaxTokens = %d, want 256 (clamped)", cfg.MaxTokens)
	}
}

// ─── Env helpers ────────────────────────────────────────────

func TestGetEnvHelpers(t *testing.T) {
	t.Setenv("TEST_INT_OK", "42")
	t.Setenv("TEST_INT_BAD", "abc")
	t.Setenv("TEST_FLOAT_OK", "3.5")
	t.Setenv("TEST_FLOAT_BAD", "x")
	t.Setenv("TEST_DUR_OK", "2500ms")
	t.Setenv("TEST_DUR_BAD", "nope")

	if v := getEnvInt("TEST_INT_OK", 1); v != 42 {
		t.Errorf("getEnvInt ok = %d", v)
	}
	if v := getEnvInt("TEST_INT_BAD", 1); v != 1 {
		t.Errorf("getEnvInt bad = %d, want fallback 1", v)
	}
	if v := getEnvInt("TEST_INT_MISSING", 7); v != 7 {
		t.Errorf("getEnvInt missing = %d, want 7", v)
	}
	if v := getEnvFloat("TEST_FLOAT_OK", 1); v != 3.5 {
		t.Errorf("getEnvFloat ok = %v", v)
	}
	if v := getEnvFloat("TEST_FLOAT_BAD", 1); v != 1 {
		t.Errorf("getEnvFloat bad = %v", v)
	}
	if v := getEnvDuration("TEST_DUR_OK", time.Second); v != 2500*time.Millisecond {
		t.Errorf("getEnvDuration ok = %v", v)
	}
	if v := getEnvDuration("TEST_DUR_BAD", time.Second); v != time.Second {
		t.Errorf("getEnvDuration bad = %v", v)
	}
	if v := getEnv("TEST_STR", "fb"); v != "fb" {
		t.Errorf("getEnv missing = %q", v)
	}
}

func TestClampHelpers(t *testing.T) {
	if v := clamp(5, 0, 10); v != 5 {
		t.Errorf("clamp mid = %d", v)
	}
	if v := clamp(-1, 0, 10); v != 0 {
		t.Errorf("clamp lo = %d", v)
	}
	if v := clamp(99, 0, 10); v != 10 {
		t.Errorf("clamp hi = %d", v)
	}
	if v := clampFloat(0.5, 0, 1); v != 0.5 {
		t.Errorf("clampFloat mid = %v", v)
	}
	if v := clampFloat(-3, 0, 1); v != 0 {
		t.Errorf("clampFloat lo = %v", v)
	}
	if v := clampFloat(7, 0, 1); v != 1 {
		t.Errorf("clampFloat hi = %v", v)
	}
	if max(3, 7) != 7 || max(9, 2) != 9 {
		t.Error("max wrong")
	}
}
