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
)

// ─── Config ─────────────────────────────────────────────────

type Config struct {
	APIKey                string
	BaseURL               string
	Model                 string
	ModelFlash            string // 轻量模型 (简单任务)
	ModelPro              string // 重量模型 (复杂任务)
	ModelVision           string // 视觉模型 (识图, 官方 deepseek-flash —— V4-Pro 不支持图像理解)
	MiniMaxAPIKey         string // MiniMax 兜底密钥 (DeepSeek 故障转移, 可空=纯DeepSeek)
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
	CompactTokenThreshold int  // 对话预算 token 上限 (分母); 触发线 = 此值 x TriggerPercent%
	CompactTriggerPercent int  // 触发线: 对话预算占用达此百分比即动手 (DG-2, 默认 70)
	CompactTargetPercent  int  // 目标线: 压完对话预算占用应 <= 此百分比 (DG-2, 默认 50)
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
	PluginReleaseDir      string   // forge-gates 插件发布根目录 (默认仓库根同级的 forge_release, 可 FORGE_PLUGIN_RELEASE_DIR 覆盖)
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
		// 2026-09-11: DeepSeek V4.1 更新 —— 官方规范名收敛为 deepseek-flash /
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
		MaxHistoryMessages:    80, // V4-Flash 1M 上下文: 历史容量 40 → 80（公理一: 记忆仍须梳理，骨架优先）
		CompactEnabled:        getEnvInt("AGENT_COMPACT_ENABLED", 1) == 1,
		CompactTokenThreshold: getEnvInt("AGENT_COMPACT_TOKEN_THRESHOLD", 60000),
		CompactTriggerPercent: getEnvInt("AGENT_COMPACT_TRIGGER_PERCENT", 70),
		CompactTargetPercent:  getEnvInt("AGENT_COMPACT_TARGET_PERCENT", 50),
		CompactMinTurns:       getEnvInt("AGENT_COMPACT_MIN_TURNS", 10),
		MaxCodeSize:           1 * 1024 * 1024, // 1MB (was 512KB)
		MaxOutputLength:       24000,           // V4-Flash 1M 上下文: 工具输出截断 16KB → 24KB
		MaxConcurrent:         4,
		CacheMaxSize:          256, // (was 128)
		RetryMax:              3,
		RetryBackoff:          1 * time.Second,
		WorkDir:               wd,
		PluginReleaseDir:      filepath.Join(filepath.Dir(wd), "forge_release"),
	}
}

// ─── LoadConfig ─────────────────────────────────────────────
//
// 拆分 (20261018 T61): 原 146 行 / 圈复杂度 33 的单函数按「装载阶段」切成 5 段,
// 每段一个私有函数。语句顺序与拆分前逐字一致 —— 顺序即语义 (后段依赖前段填好的
// 默认值), 不是可自由重排的装饰。收益: 单段可独立单测, 不再只能靠端到端用例间接覆盖。

// LoadConfig 装载配置: .env → 出厂默认 → 分层环境变量覆盖 → 数值夹取。
func LoadConfig() (*Config, error) {
	if err := loadDotEnvIfPresent(); err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if err := applyAPIKey(cfg); err != nil {
		return nil, err
	}
	if err := applyBaseURL(cfg); err != nil {
		return nil, err
	}
	if err := applyModelRouting(cfg); err != nil {
		return nil, err
	}
	applyEnvNumberOverrides(cfg)
	applyRuntimeEnv(cfg)
	clampConfig(cfg)
	return cfg, nil
}

// loadDotEnvIfPresent 加载 .env: 先找 forge.exe 同目录, 再找当前目录。
//
// .env 加载 (20261004 自实现 loadEnvFile, 移除 github.com/joho/godotenv):
// 旧版 `_ = godotenv.Load(...)` 把错误整体丢弃 —— .env 读不动/语法坏时症状
// 表现为「密钥没设置」, 排查先怀疑自己再怀疑依赖。新版: 文件不存在 = 正常
// 静默(不报错); 文件存在但读失败/语法非法 = 显式返回错误。
func loadDotEnvIfPresent() error {
	// Load .env from forge.exe directory (absolute path)
	exePath, execErr := os.Executable()
	if execErr != nil {
		exePath = os.Args[0] // 失败回退: 用可执行文件参数
	}
	envFile := filepath.Join(filepath.Dir(exePath), ".env")
	if _, err := os.Stat(envFile); err == nil {
		if err := loadEnvFile(envFile); err != nil {
			return fmt.Errorf("加载 %s 失败: %w", envFile, err)
		}
		return nil
	}
	if _, err := os.Stat(".env"); err == nil {
		if err := loadEnvFile(".env"); err != nil {
			return fmt.Errorf("加载 .env 失败: %w", err)
		}
	}
	return nil
}

// applyAPIKey 装载 DeepSeek 密钥 (DEEPSEEK_API_KEY 优先, LLM_API_KEY 为兼容旧名)。
func applyAPIKey(cfg *Config) error {
	// API key (DeepSeek only)
	cfg.APIKey = os.Getenv("DEEPSEEK_API_KEY")
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("LLM_API_KEY")
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("%w: set DEEPSEEK_API_KEY", ErrMissingAPIKey)
	}
	return nil
}

// applyBaseURL 装载并校验 API 端点。
func applyBaseURL(cfg *Config) error {
	// Base URL
	if v := os.Getenv("DEEPSEEK_BASE_URL"); v != "" {
		cfg.BaseURL = strings.TrimSpace(v)
	}
	if u, err := url.Parse(cfg.BaseURL); err != nil || u.Scheme == "" {
		return fmt.Errorf("%w: %q", ErrInvalidURL, cfg.BaseURL)
	}
	return nil
}

// applyModelRouting 装载模型名与路由模式, 并归一非法值 (含 MiniMax 可选兜底字段)。
func applyModelRouting(cfg *Config) error {
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
	// MiniMax 故障兜底 (可选): 三字段齐备才激活, 否则纯 DeepSeek。
	// 20261011 (T31): 已撤销"高峰时段自动路由" —— 只在 DeepSeek 请求失败时兜底一次。
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
		return ErrInvalidModel
	}
	return nil
}

// applyRuntimeEnv 装载运行期开关 (缓存文件 / gate 白名单 / 推理强度 / 工作目录)。
func applyRuntimeEnv(cfg *Config) {
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
}

// clampConfig 把数值参数压进安全区间 (防止 .env 手改出越界值)。
func clampConfig(cfg *Config) {
	// Clamp to safe ranges
	cfg.MaxConsecutiveFails = clamp(cfg.MaxConsecutiveFails, 1, 50)
	cfg.MaxLoopStrikes = clamp(cfg.MaxLoopStrikes, 1, 20)
	cfg.MaxHistoryMessages = clamp(cfg.MaxHistoryMessages, 4, 500)
	cfg.CompactTokenThreshold = clamp(cfg.CompactTokenThreshold, 1000, 200000)
	cfg.CompactTriggerPercent = clamp(cfg.CompactTriggerPercent, 10, 100)
	cfg.CompactTargetPercent = clamp(cfg.CompactTargetPercent, 1, 99)
	normalizeCompactPercents(cfg)
	cfg.CompactMinTurns = clamp(cfg.CompactMinTurns, 1, 100)
	cfg.MaxConcurrent = clamp(cfg.MaxConcurrent, 1, 64)
	cfg.CacheMaxSize = clamp(cfg.CacheMaxSize, 0, 10000)
	cfg.MaxCodeSize = clamp(cfg.MaxCodeSize, 1024, 50*1024*1024) // up to 50MB
	cfg.RetryMax = clamp(cfg.RetryMax, 0, 10)
	cfg.MaxOutputLength = clamp(cfg.MaxOutputLength, 256, 512*1024) // up to 512KB
	cfg.MaxTokens = clamp(cfg.MaxTokens, 256, 262144)               // V4 输出上限 384K
	cfg.Temperature = clampFloat(cfg.Temperature, 0, 2.0)
	cfg.TopP = clampFloat(cfg.TopP, 0, 1.0)
}

// normalizeCompactPercents 压缩预算配置矛盾自动纠正 (DG-2): 目标线必须低于触发线 ——
// 否则每次压缩都注定不达标 (白付摘要费 + 冷却一过又撞线; 实测旧实现 23.9% 的压缩
// 压完仍剩 <30% 即此形态)。抽成纯函数以便单测: 内联在 LoadConfig 里就只能靠端到端
// 用例间接覆盖 (公理三: 架构约束必须是死程序可判定的)。
func normalizeCompactPercents(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.CompactTargetPercent >= cfg.CompactTriggerPercent {
		cfg.CompactTargetPercent = cfg.CompactTriggerPercent / 2
	}
}

// applyEnvNumberOverrides 从环境变量应用数值/时长类覆盖。
// 未设置的键保留 DefaultConfig 的默认值 (getEnvX 的 fallback 语义)。
func applyEnvNumberOverrides(cfg *Config) {
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
	cfg.CompactTriggerPercent = getEnvInt("AGENT_COMPACT_TRIGGER_PERCENT", cfg.CompactTriggerPercent)
	cfg.CompactTargetPercent = getEnvInt("AGENT_COMPACT_TARGET_PERCENT", cfg.CompactTargetPercent)
	cfg.CompactMinTurns = getEnvInt("AGENT_COMPACT_MIN_TURNS", cfg.CompactMinTurns)
	cfg.MaxConcurrent = getEnvInt("FORGE_MAX_CONCURRENT", cfg.MaxConcurrent)
	cfg.CacheMaxSize = getEnvInt("FORGE_CACHE_SIZE", cfg.CacheMaxSize)
	cfg.MaxCodeSize = getEnvInt("FORGE_MAX_CODE_SIZE", cfg.MaxCodeSize)
	cfg.MaxOutputLength = getEnvInt("FORGE_MAX_OUTPUT", cfg.MaxOutputLength)
	cfg.RetryMax = getEnvInt("FORGE_RETRY_MAX", cfg.RetryMax)
	cfg.RetryBackoff = getEnvDuration("FORGE_RETRY_BACKOFF", cfg.RetryBackoff)
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
