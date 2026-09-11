package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// ─── Config ─────────────────────────────────────────────────

type Config struct {
	APIKey                string
	BaseURL               string
	Model                 string
	ModelFlash            string // 轻量模型 (简单任务)
	ModelPro              string // 重量模型 (复杂任务)
	ModelVision           string // 视觉模型 (识图, 官方 deepseek-flash —— V4-Pro 不支持图像理解)
	MiniMaxAPIKey         string // MiniMax 备用密钥 (高峰路由, 可空=纯DeepSeek)
	MiniMaxBaseURL        string // MiniMax 端点 (可空)
	MiniMaxModel          string // MiniMax 模型名 (可空)
	RouterMode            string // auto | flash | pro | fixed
	MaxTokens             int
	Temperature           float64
	TopP                  float64
	RequestTimeout        time.Duration
	StreamTimeout         time.Duration
	ToolTimeout           time.Duration
	MaxConsecutiveFails   int
	MaxLoopStrikes        int // 循环拦截次数上限: 无进展检测拦截 N 次后自动收尾 (非回合上限)
	MaxHistoryMessages    int
	CompactEnabled        bool // 历史压缩开关 (六西格玛立项20260815): 超阈值先摘要再裁剪
	CompactTokenThreshold int  // 触发压缩的历史 token 估算阈值
	CompactMinTurns       int  // 压缩冷却: 距上次压缩最少间隔轮数 (防前缀频繁变)
	ShowReasoning         bool
	ReasoningEffort       string // "low"|"high"|"max" (V4-Pro 思考强度), 空=官方默认
	MaxCodeSize           int
	MaxOutputLength       int
	MaxConcurrent         int
	CacheMaxSize          int
	RetryMax              int
	RetryBackoff          time.Duration
	WorkDir               string
	CachePersistFile      string
	GatesEnabled          []string // 三期 I3: 启用的自托管 gate 列表; 空 = 全部启用
	PluginReleaseDir      string   // forge-gates 插件发布根目录 (默认 D:\forge_release, 可 FORGE_PLUGIN_RELEASE_DIR 覆盖)
}

// ─── Sentinel errors ────────────────────────────────────────

var (
	ErrMissingAPIKey = errors.New("API key not set")
	ErrInvalidURL    = errors.New("invalid API base URL")
	ErrInvalidModel  = errors.New("model name cannot be empty")
)

// ─── Production-grade defaults ──────────────────────────────

func DefaultConfig() *Config {
	wd, err := os.Getwd()
	if err != nil {
		// Getwd 失败(罕见): 回退当前目录, 避免 WorkDir 空串
		wd = "."
	}
	// 本体回退：若当前目录无 forge.go（例如从其他目录启动），
	// 则退回可执行文件所在目录——铸剑炉的本体在 exe 旁边
	if _, err := os.Stat(filepath.Join(wd, "forge.go")); err != nil {
		if exeDir, err2 := filepath.Abs(filepath.Dir(os.Args[0])); err2 == nil {
			if _, err3 := os.Stat(filepath.Join(exeDir, "forge.go")); err3 == nil {
				wd = exeDir
			}
		}
	}
	return &Config{
		BaseURL: "https://api.deepseek.com/v1",
		// 2026-09-11 金光海: DeepSeek V4.1 更新 —— 官方规范名收敛为 deepseek-flash /
		// deepseek-v4-pro 两个; 旧名 v4-flash / v4-flash-vision-exp 已下线(服务端静默别名到
		// V4.1-Flash); 图像理解仅 deepseek-flash 支持。
		// 2026-09-11 复核(官方 quick_start/pricing 脚注2): 官方已改口 —— 2026-09-14 之后
		// 继续提供 V4 Pro 服务, 计费不变(原"09-14 起 pro 全量路由到 Flash"作废)。
		// 仍统一 deepseek-flash 的理由: 官方基准 V4.1-Flash 在 Agent/工程项全面超 V4-Pro
		// (Terminal-Bench 2.1 90.6>87.9, DeepSWE v1.1 74.2>62.7, NL2Repo 65.4>61.5,
		// CyberGym 88.1>83.3, Agents' Last Exam 31.8>25.7), 仅 HLE 知识推理落后(36.8<42.7);
		// 价格未命中 1<4.5 元 / 输出 4<13.5 元(便宜 4.5 倍)。V4-Pro 留作知识推理手动回退。
		Model:                 "deepseek-flash",
		ModelFlash:            "deepseek-flash",
		ModelPro:              "deepseek-flash", // 回退 pro: 改 .env DEEPSEEK_MODEL_PRO=deepseek-v4-pro
		ModelVision:           "deepseek-flash",
		RouterMode:            RouterAuto,
		MaxTokens:             131072, // V4.1-Flash 推理+正文共享总预算; 按实际输出计费, 设大仅防截断
		Temperature:           0.7,
		TopP:                  0.95,
		RequestTimeout:        120 * time.Second,
		StreamTimeout:         300 * time.Second,
		ToolTimeout:           60 * time.Second,
		MaxConsecutiveFails:   5,
		MaxLoopStrikes:        4,  // 循环拦截 4 次后带进展收尾; 无回合上限, 真正干活可无限跑
		MaxHistoryMessages:    80, // V4-Flash 1M 上下文: 历史容量 40 → 80（公理三: 记忆仍须梳理，骨架优先）
		CompactEnabled:        getEnvInt("AGENT_COMPACT_ENABLED", 1) == 1,
		CompactTokenThreshold: getEnvInt("AGENT_COMPACT_TOKEN_THRESHOLD", 20000),
		CompactMinTurns:       getEnvInt("AGENT_COMPACT_MIN_TURNS", 10),
		MaxCodeSize:           1 * 1024 * 1024, // 1MB (was 512KB)
		MaxOutputLength:       24000,           // V4-Flash 1M 上下文: 工具输出截断 16KB → 24KB
		MaxConcurrent:         4,
		CacheMaxSize:          256, // (was 128)
		RetryMax:              3,
		RetryBackoff:          1 * time.Second,
		WorkDir:               wd,
		PluginReleaseDir:      "D:\\forge_release",
	}
}

// ─── LoadConfig ─────────────────────────────────────────────

func LoadConfig() (*Config, error) {
	// Load .env from forge.exe directory (absolute path)
	exePath, execErr := os.Executable()
	if execErr != nil {
		exePath = os.Args[0] // 失败回退: 用可执行文件参数
	}
	envFile := filepath.Join(filepath.Dir(exePath), ".env")
	if _, err := os.Stat(envFile); err == nil {
		_ = godotenv.Load(envFile)
	} else {
		_ = godotenv.Load() // fallback to current directory
	}

	cfg := DefaultConfig()

	// API key (DeepSeek only)
	cfg.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("LLM_API_KEY")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: set DEEPSEEK_API_KEY", ErrMissingAPIKey)
	}

	// Base URL
	if v := os.Getenv("DEEPSEEK_BASE_URL"); v != "" {
		cfg.BaseURL = strings.TrimSpace(v)
	}
	if u, err := url.Parse(cfg.BaseURL); err != nil || u.Scheme == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidURL, cfg.BaseURL)
	}

	// Model (fixed 模式兼容: 显式设置 DEEPSEEK_MODEL 则固定用该模型)
	if v := os.Getenv("DEEPSEEK_MODEL"); v != "" {
		cfg.Model = strings.TrimSpace(v)
		cfg.RouterMode = RouterFixed
	}
	if v := os.Getenv("DEEPSEEK_MODEL_FLASH"); v != "" {
		cfg.ModelFlash = strings.TrimSpace(v)
	}
	if v := os.Getenv("DEEPSEEK_MODEL_PRO"); v != "" {
		cfg.ModelPro = strings.TrimSpace(v)
	}
	if v := os.Getenv("DEEPSEEK_MODEL_VISION"); v != "" {
		cfg.ModelVision = strings.TrimSpace(v)
	}
	// MiniMax 高峰路由 (可选): 三字段齐备才激活, 否则纯 DeepSeek。
	if v := os.Getenv("MINIMAX_API_KEY"); v != "" {
		cfg.MiniMaxAPIKey = strings.TrimSpace(v)
	}
	if v := os.Getenv("MINIMAX_BASE_URL"); v != "" {
		cfg.MiniMaxBaseURL = strings.TrimSpace(v)
	}
	if v := os.Getenv("MINIMAX_MODEL"); v != "" {
		cfg.MiniMaxModel = strings.TrimSpace(v)
	}

	if v := os.Getenv("DEEPSEEK_ROUTER"); v != "" {
		cfg.RouterMode = strings.ToLower(strings.TrimSpace(v))
	}
	if cfg.Model == "" {
		cfg.Model = cfg.ModelPro
	}
	if cfg.ModelFlash == "" {
		cfg.ModelFlash = "deepseek-flash"
	}
	if cfg.ModelPro == "" {
		cfg.ModelPro = "deepseek-flash"
	}
	if cfg.ModelVision == "" {
		cfg.ModelVision = "deepseek-flash"
	}
	switch cfg.RouterMode {
	case RouterFlash, RouterPro, RouterFixed:
	default:
		cfg.RouterMode = RouterAuto
	}
	if cfg.Model == "" {
		return nil, ErrInvalidModel
	}

	// Numeric overrides from environment
	cfg.MaxTokens = getEnvInt("LLM_MAX_TOKENS", cfg.MaxTokens)
	cfg.Temperature = getEnvFloat("LLM_TEMPERATURE", cfg.Temperature)
	cfg.TopP = getEnvFloat("LLM_TOP_P", cfg.TopP)
	cfg.RequestTimeout = getEnvDuration("LLM_REQUEST_TIMEOUT", cfg.RequestTimeout)
	cfg.StreamTimeout = getEnvDuration("LLM_STREAM_TIMEOUT", cfg.StreamTimeout)
	cfg.ToolTimeout = getEnvDuration("FORGE_TOOL_TIMEOUT", cfg.ToolTimeout)
	cfg.MaxConsecutiveFails = getEnvInt("AGENT_MAX_CONSECUTIVE_FAILS", cfg.MaxConsecutiveFails)
	cfg.MaxLoopStrikes = getEnvInt("AGENT_MAX_LOOP_STRIKES", cfg.MaxLoopStrikes)
	cfg.MaxHistoryMessages = getEnvInt("AGENT_MAX_HISTORY", cfg.MaxHistoryMessages)
	cfg.CompactEnabled = getEnvInt("AGENT_COMPACT_ENABLED", 1) == 1
	cfg.CompactTokenThreshold = getEnvInt("AGENT_COMPACT_TOKEN_THRESHOLD", cfg.CompactTokenThreshold)
	cfg.CompactMinTurns = getEnvInt("AGENT_COMPACT_MIN_TURNS", cfg.CompactMinTurns)
	cfg.MaxConcurrent = getEnvInt("FORGE_MAX_CONCURRENT", cfg.MaxConcurrent)
	cfg.CacheMaxSize = getEnvInt("FORGE_CACHE_SIZE", cfg.CacheMaxSize)
	cfg.MaxCodeSize = getEnvInt("FORGE_MAX_CODE_SIZE", cfg.MaxCodeSize)
	cfg.MaxOutputLength = getEnvInt("FORGE_MAX_OUTPUT", cfg.MaxOutputLength)
	cfg.RetryMax = getEnvInt("FORGE_RETRY_MAX", cfg.RetryMax)
	cfg.RetryBackoff = getEnvDuration("FORGE_RETRY_BACKOFF", cfg.RetryBackoff)

	// Cache persistence
	cfg.CachePersistFile = getEnv("FORGE_CACHE_PERSIST_FILE", cfg.CachePersistFile)

	// gate 启用配置 (逗号分隔; 空 = 全部启用)
	if v := os.Getenv("FORGE_GATES_ENABLED"); strings.TrimSpace(v) != "" {
		for _, g := range strings.Split(v, ",") {
			g = strings.TrimSpace(g)
			if g != "" {
				cfg.GatesEnabled = append(cfg.GatesEnabled, g)
			}
		}
	}

	if v := os.Getenv("FORGE_SHOW_REASONING"); v != "" {
		cfg.ShowReasoning, _ = strconv.ParseBool(strings.TrimSpace(v))
	} else {
		cfg.ShowReasoning = true // default: reasoning on
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("DEEPSEEK_REASONING_EFFORT"))); v != "" {
		switch v {
		case "low", "high", "max":
			cfg.ReasoningEffort = v
		default:
			fmt.Fprintf(os.Stderr, "⚠️ 忽略非法 DEEPSEEK_REASONING_EFFORT=%q (可选: low/high/max)\n", v)
		}
	}

	if v := os.Getenv("FORGE_WORK_DIR"); v != "" {
		cfg.WorkDir = v
	}
	if v := os.Getenv("FORGE_PLUGIN_RELEASE_DIR"); v != "" {
		cfg.PluginReleaseDir = v
	}

	// Clamp to safe ranges
	cfg.MaxConsecutiveFails = clamp(cfg.MaxConsecutiveFails, 1, 50)
	cfg.MaxLoopStrikes = clamp(cfg.MaxLoopStrikes, 1, 20)
	cfg.MaxHistoryMessages = clamp(cfg.MaxHistoryMessages, 4, 500)
	cfg.CompactTokenThreshold = clamp(cfg.CompactTokenThreshold, 1000, 200000)
	cfg.CompactMinTurns = clamp(cfg.CompactMinTurns, 1, 100)
	cfg.MaxConcurrent = clamp(cfg.MaxConcurrent, 1, 64)
	cfg.CacheMaxSize = clamp(cfg.CacheMaxSize, 0, 10000)
	cfg.MaxCodeSize = clamp(cfg.MaxCodeSize, 1024, 50*1024*1024) // up to 50MB
	cfg.RetryMax = clamp(cfg.RetryMax, 0, 10)
	cfg.MaxOutputLength = clamp(cfg.MaxOutputLength, 256, 512*1024) // up to 512KB
	cfg.MaxTokens = clamp(cfg.MaxTokens, 256, 262144)               // V4 输出上限 384K
	cfg.Temperature = clampFloat(cfg.Temperature, 0, 2.0)
	cfg.TopP = clampFloat(cfg.TopP, 0, 1.0)

	return cfg, nil
}

// ─── Environment helpers ────────────────────────────────────

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return fallback
}

// ─── Math helpers ───────────────────────────────────────────

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
