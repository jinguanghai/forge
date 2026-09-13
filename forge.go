package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ForgeToolName        = "forge"
	ForgeToolDescription = "铸剑炉: 写代码，自动编译执行，用完销毁。唯一的工具。"
	ForgeToolsDir        = ".forge/forge-tools"
	ForgeMemoryDir       = ".forge/memory"
	MaxForgeOutputLength = 8000
	ForgeHeadKeep        = 6000
	ForgeTailKeep        = 2000
)
const ForgeInputEnv = "铸剑炉_INPUT"

var (
	ErrTooBusy      = errors.New("forge满载")
	ErrShuttingDown = errors.New("forge正在关闭")

	// ErrCancelled: 用户按 Ctrl+C 取消了本次执行 (与程序关停 ErrShuttingDown 区分)。
	ErrCancelled = errors.New("本次执行已被用户取消")
)

// CompilerDef describes how to check and execute code in a language.
type CompilerDef struct {
	Check          []string
	Exec           []string
	Lint           []string
	Ext            string
	CompileTimeout time.Duration
	ExecTimeout    time.Duration
	InlineCode     bool
	SelfHosted     bool
}

// 铸剑炉_GATES 是 gate 清单的唯一源 (single source of truth)。
//
// 此前清单有 3 份独立副本: gatesync.go 的 currentGates、memory_health.go 体检
// 里的局部 cur、以及本文件 铸剑炉_COMPILERS 的 key 集合。新增/删除 gate 需手工
// 同步三处, 漏一处即静默脱节 (清单式防护腐化的经典形态)。现统一引用本变量;
// 表 (map) 与清单的一致性由 gates_consistency_test.go 的哨兵把守。
// ⚠️ 只读: 任何调用方都不得修改本 slice。
// 超时兜底单一源 —— 未配置时的默认值集中于此, 禁止散落字面量。
// (散落字面量的危害: 改一处漏一处 → 同类行为静默不一致)
const (
	defaultGateTimeout  = 30 * time.Second // 通用 gate 兜底 (编译器表未配 ExecTimeout)
	deadBoundaryTimeout = 20 * time.Second // 编译产物(死边界)调用兜底
	shGateTimeout       = 15 * time.Second // selfHostedSh 内联 sh 执行
)

var 铸剑炉_GATES = []string{
	"python", "go", "sh", "node", "math", "logic",
	"regex", "knowledge", "tcm", "browser", "chain", "self",
}

var 铸剑炉_COMPILERS = map[string]CompilerDef{
	"go": {
		Ext:            ".go",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     false,
		SelfHosted:     true,
	},
	"sh": {
		Ext:            "",
		CompileTimeout: 0,
		ExecTimeout:    15 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"math": {
		Ext:            "",
		CompileTimeout: 15 * time.Second,
		ExecTimeout:    15 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"logic": {
		Ext:            "",
		CompileTimeout: 15 * time.Second,
		ExecTimeout:    15 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"knowledge": {
		Ext:            "",
		CompileTimeout: 30 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"regex": {
		Ext:            "",
		CompileTimeout: 10 * time.Second,
		ExecTimeout:    10 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"chain": {
		Ext:            "",
		CompileTimeout: 30 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"python": {
		Check:          []string{"python", "-m", "py_compile"},
		Exec:           []string{"python", "{file}"},
		Ext:            ".py",
		CompileTimeout: 10 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     false,
	},
	"node": {
		Exec:           []string{"node", "-"},
		Ext:            ".js",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     true,
	},
	"self": {
		Ext:            "",
		CompileTimeout: 60 * time.Second,
		ExecTimeout:    60 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"tcm": {
		Ext:            "",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    10 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"browser": {
		Ext:            ".py",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    120 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
}

// CompilerError represents a parsed compiler diagnostic.
type CompilerError struct {
	Lang string `json:"lang"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Msg  string `json:"msg"`
}

type ForgeParams struct {
	Action string `json:"action"`
	Code   string `json:"code"`
	Lang   string `json:"lang"`
	Input  string `json:"input"`
}

type ForgeGateResult struct {
	OK       bool   `json:"ok"`
	Lang     string `json:"lang"`
	Stage    string `json:"stage,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Lint     string `json:"lint,omitempty"`
	ExitCode int    `json:"exit_code"`
	Duration int64  `json:"duration_ms"`
	Error    string `json:"error,omitempty"`
	CodeSize int    `json:"code_size"`
	Retries  int    `json:"retries,omitempty"`

	// Timeout 标记「执行超时」类失败。retryGate 不重试、shouldFallback 不换
	// 语言：已经烧完整个超时预算的任务，重跑只会再烧一遍(实测 3 次重试把
	// 30s 放大成 93.6s)，对用户零收益。
	Timeout     bool   `json:"timeout,omitempty"`
	CodeLines   int    `json:"code_lines"`
	Summary     string `json:"summary,omitempty"`
	Diagnostics string `json:"diagnostics,omitempty"`

	// CachedAt is the unix timestamp (seconds) when this result was cached.
	// Used for TTL-based invalidation; 0 means an old-format entry (expired).
	CachedAt int64 `json:"cached_at,omitempty"`
}

// atoi converts a string to int, returning 0 on failure.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// truncateMsg caps a diagnostic message to a reasonable length.
func truncateMsg(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

func parseGoErr(text string) []CompilerError {
	var out []CompilerError
	re := regexp.MustCompile(`([^:\s]+\.go):(\d+):(\d+):\s*(.+)`)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		out = append(out, CompilerError{
			Lang: "go", Line: atoi(m[2]), Col: atoi(m[3]),
			Msg: strings.TrimSpace(m[4]),
		})
	}
	return out
}

func parsePyErr(text string) []CompilerError {
	var out []CompilerError
	lineRe := regexp.MustCompile(`File "[^"]*", line (\d+)`)
	msgRe := regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*(?:Error|Exception): [^\r\n]+)`)
	caretRe := regexp.MustCompile(`(?m)^(\s*)\^`)
	msg := ""
	if mm := msgRe.FindStringSubmatch(text); len(mm) > 1 {
		msg = strings.TrimSpace(mm[1])
	}
	col := 0
	if cm := caretRe.FindStringSubmatch(text); len(cm) > 1 {
		col = len(cm[1]) + 1
	}
	for _, m := range lineRe.FindAllStringSubmatch(text, -1) {
		out = append(out, CompilerError{
			Lang: "python", Line: atoi(m[1]), Col: col,
			Msg: truncateMsg(msg),
		})
	}
	return out
}
func parseNodeErr(text string) []CompilerError {
	var out []CompilerError
	posRe := regexp.MustCompile(`(?m)^\[(?:stdin|eval)\]:(\d+)(?::(\d+))?`)
	msgRe := regexp.MustCompile(`(?m)^([A-Za-z]+Error: [^\r\n]+)`)
	caretRe := regexp.MustCompile(`(?m)^(\s*)\^`)
	msg := ""
	if mm := msgRe.FindStringSubmatch(text); len(mm) > 1 {
		msg = strings.TrimSpace(mm[1])
	}
	col := 0
	if cm := caretRe.FindStringSubmatch(text); len(cm) > 1 {
		col = len(cm[1]) + 1
	}
	for _, m := range posRe.FindAllStringSubmatch(text, -1) {
		c := atoi(m[2])
		if c == 0 {
			c = col
		}
		out = append(out, CompilerError{
			Lang: "node", Line: atoi(m[1]), Col: c,
			Msg: truncateMsg(msg),
		})
	}
	return out
}
func parseCompilerError(lang, text string) []CompilerError {
	// Strip ANSI color codes before parsing.
	ansiRe := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	text = ansiRe.ReplaceAllString(text, "")
	switch lang {
	case "go":
		return parseGoErr(text)
	case "python":
		return parsePyErr(text)
	case "node":
		return parseNodeErr(text)
	default:
		return nil
	}
}
func compilerErrorsToJSON(errs []CompilerError) string {
	if len(errs) == 0 {
		return ""
	}
	b, _ := json.Marshal(errs)
	return string(b)
}

type Forge struct {
	workDir   string
	toolsDir  string
	memoryDir string
	ctx       context.Context

	// runCtx: 当前工具执行的 ctx (agent 每次 run 前 SetRunCtx, 用完置 nil)。
	// 用途: 让第一次 Ctrl+C 能中断正在执行的工具 —— 此前 Build 内部一律用 f.ctx 独立派生
	// 超时 ctx, 取消 runCtx 对工具执行与重试循环完全无效, 用户只能按第二次(=直接退出程序)。
	runCtx           context.Context
	cancel           context.CancelFunc
	sem              chan struct{}
	cache            map[string]ForgeGateResult
	cacheKeys        []string // FIFO insertion order for LRU eviction
	cacheMaxSize     int
	maxConcurrent    int
	cacheMu          sync.RWMutex
	cacheSaveCounter int
	cachePersistFile string
	cacheDiskMu      sync.Mutex // serializes disk writes to prevent gob corruption
	retryMax         int
	retryBackoff     time.Duration
	cfg              *Config // needed by self-hosted gates

	// Runtime statistics (visible via Stats())
	statBuilds    atomic.Int64
	statCacheHits atomic.Int64
	statErrors    atomic.Int64
}

func NewForge(workDir string, cfg *Config) *Forge {

	// Clean up stale temp directories from previous runs
	forgeCleanupStaleTempDirs()
	ctx, cancel := context.WithCancel(context.Background())
	f := &Forge{
		workDir:          workDir,
		toolsDir:         filepath.Join(workDir, ForgeToolsDir),
		memoryDir:        filepath.Join(workDir, ForgeMemoryDir),
		ctx:              ctx,
		cancel:           cancel,
		sem:              make(chan struct{}, clamp(cfg.MaxConcurrent, 1, 64)),
		cache:            make(map[string]ForgeGateResult),
		cacheKeys:        make([]string, 0, clamp(cfg.CacheMaxSize, 0, 10000)),
		cacheMaxSize:     clamp(cfg.CacheMaxSize, 0, 10000),
		maxConcurrent:    clamp(cfg.MaxConcurrent, 1, 64),
		retryMax:         clamp(cfg.RetryMax, 0, 10),
		retryBackoff:     cfg.RetryBackoff,
		cfg:              cfg,
		cachePersistFile: filepath.Join(workDir, "forge_cache.gob"),
	}
	os.MkdirAll(f.toolsDir, 0755)
	os.MkdirAll(f.memoryDir, 0755)
	f.loadCacheFromDisk()
	return f
}

func (f *Forge) Shutdown() {
	f.saveCacheToDisk()
	f.cancel()
}

// SetRunCtx 由 agent 在每次工具执行前调用, 传入本次 run 的 ctx; 用完传 nil 恢复默认。
func (f *Forge) SetRunCtx(ctx context.Context) { f.runCtx = ctx }

// effCtx 返回本次工具执行应使用的 ctx: 优先 runCtx, 否则 f.ctx。
func (f *Forge) effCtx() context.Context {
	if f.runCtx != nil {
		return f.runCtx
	}
	return f.ctx
}

// Build is the main entry point: write code, validate, execute, destroy.

// Returns formatted output, the full result struct, and any error.
func (f *Forge) Build(code, lang, input string) (string, *ForgeGateResult, error) {
	langOmitted := lang == ""
	fallbackUsed := false
	buildStart := time.Now()
	select {
	case f.sem <- struct{}{}:
		defer func() { <-f.sem }()
	case <-f.effCtx().Done():

		// runCtx 被取消 = 用户按了 Ctrl+C; f.ctx 被取消 = 程序关停
		if f.runCtx != nil && f.runCtx.Err() != nil {
			return "", nil, ErrCancelled
		}
		return "", nil, ErrShuttingDown
	case <-time.After(60 * time.Second):
		return "", nil, ErrTooBusy
	}
	if lang == "" {
		lang = forgeDetectLang(code, "")
	}

	// Normalize common aliases
	switch lang {
	case "bash":
		lang = "sh"
	case "javascript", "js":
		lang = "node"

		// "c" 已无对应编译器 (tcc 裁剪), 不再映射, 交给自动检测/报错
	}

	// ─── 危险代码审批 (代码层强制, 非提示铁律) ───
	// 模型要执行的代码命中危险模式, 或调用 self 自改 gate → 终端 y/N 批准后才执行。
	// 拒绝时返回回执给模型 (代码不执行), 不产生错误状态 (避免触发失败重试链)。
	kind, hit, danger := checkDangerousCode(code)
	if !danger {
		// 第二道防线: 受保护目标 × 破坏谓词共现 (拦 os.remove("memory.json") 等)
		if k2, h2, d2 := checkDangerousTarget(code, f.workDir); d2 {
			kind, hit, danger = k2, h2, true
		}
	}
	if danger || lang == "self" {
		if kind == "" {
			kind = "自改"
			hit = "self gate"
		}
		if !f.confirmDangerous(code, kind, hit) {
			logGuardEvent(f.workDir, "medium", kind, hit, "deny", "代码层审批拒绝", code)
			return fmt.Sprintf("--- ⛔ 操作被主人拒绝 ---\n危险操作 [%s] 命中「%s」\n主人未批准, 代码未执行。请调整方案(改用更安全的方式, 或先向主人说明用途取得批准)。\n--- 结束 ---", kind, hit), nil, nil
		}
		logGuardEvent(f.workDir, "medium", kind, hit, "allow", "代码层审批通过", code)
	}

	var result ForgeGateResult
	defer func() { f.auditGate(lang, langOmitted, fallbackUsed, buildStart, len(code), len(input), &result) }()
	if f.retryMax > 1 {
		result = f.retryGate(code, lang, input)
	} else {
		result = f.forgeGate(code, lang, input)
	}
	if !result.OK {
		// Attempt fallback: tool-not-found/timeout → try next language
		if shouldFallback(result) {
			fb := pickFallback(lang)
			if fb != "" {
				slog.Info("forge fallback", "from", lang, "to", fb)
				fbResult := f.forgeGate(code, fb, input)
				if fbResult.OK {
					fallbackUsed = true
					result = fbResult
					return f.formatResult(result), &result, nil
				}
			}
		}
		errMsg := result.Error
		if result.Stderr != "" {
			errMsg = result.Stderr
		}
		errOutput := fmt.Sprintf("--- %s 失败 [%s] ---\n错误: %s\n标准错误:\n%s\n--- 结束 ---",
			lang, result.Stage, result.Error, result.Stderr)
		if result.Diagnostics != "" {
			errOutput += "\n--- diagnostics ---\n" + result.Diagnostics
		}
		return errOutput, &result, fmt.Errorf("%s 门失败: %s", lang, errMsg)
	}
	return f.formatResult(result), &result, nil
}

// confirmDangerous 危险操作终端交互确认。
// 返回 true = 主人批准; false = 拒绝。批准/拒绝均记入事件链。
func (f *Forge) confirmDangerous(code, kind, hit string) bool {
	fmt.Fprintf(os.Stderr, "\n%s 危险操作检测 [%s] 命中「%s」\n", color(ansi.yellow, "🛡"), kind, hit)
	fmt.Fprintf(os.Stderr, "  代码: %s\n", dim(summarizeCode(code)))
	fmt.Fprintf(os.Stderr, "%s 批准执行? (y/N): ", color(ansi.yellow, "⚠"))
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		line = ""
	}
	allowed := parseApproval(line)
	if allowed {
		logEvent(EvApproved, kind, map[string]string{"hit": hit, "verdict": "allow"})
	} else {
		logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "verdict": "deny"})
	}
	return allowed
}

// auditFilePath 返回审计文件 (gate_audit.jsonl) 的写入路径。
//
// 优先级: 有效工作目录 (非空且非 ".") → 用它; 否则回退 FORGE_AUDIT_PATH。
// 回退分支是给测试用的隔离出口: 测试进程的 WorkDir 常为 "" 或 "." (= 仓库根),
// 直接写入会把假数据混进生产审计日志 —— 实测一次全量测试注入 33 行
// (28 条 gate + 4 条 compact + 1 条 compact_failed), 直接污染
// "gate 失败率/耗时" 与 "压缩成功率" 的统计基础 (治理决策的度量输入)。
// 测试侧由 audit_isolation_test.go 的 TestMain 把该 env 指向临时目录。
func auditFilePath(dir string) string {
	if dir != "" && dir != "." {
		return filepath.Join(dir, "gate_audit.jsonl")
	}
	if p := os.Getenv("FORGE_AUDIT_PATH"); p != "" {
		return p
	}
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "gate_audit.jsonl")
}

// auditGate appends one JSON line per Build call to gate_audit.jsonl.
// Best-effort only: failures are silent so auditing never blocks the main path.
// Disable with FORGE_GATE_AUDIT=0.
func (f *Forge) auditGate(lang string, omitted bool, fallbackUsed bool, start time.Time, codeLen, inputLen int, res *ForgeGateResult) {
	if os.Getenv("FORGE_GATE_AUDIT") == "0" {
		return
	}
	entry := map[string]interface{}{
		"ts":           time.Now().Format(time.RFC3339Nano),
		"lang":         lang,
		"lang_omitted": omitted,
		"fallback":     fallbackUsed,
		"ok":           res != nil && res.OK,
		"duration_ms":  time.Since(start).Milliseconds(),
		"code_len":     codeLen,
		"input_len":    inputLen,
	}
	if res != nil {
		entry["retries"] = res.Retries
		if !res.OK {
			entry["stage"] = res.Stage
			snip := res.Error
			if len(snip) > 100 {
				snip = snip[:100]
			}
			entry["err_snip"] = snip
		}
	}
	appendAuditJSONL(auditFilePath(f.workDir), entry)
}

// auditWriteMu 串行化 gate_audit.jsonl 的追加写入。
// 审计文件只保留这一条写入路径 —— 此前 forge 侧用实例 auditMu、agent 侧完全无锁,
// 同一文件的同类写入两套规则 (结构上不一致: 改一处漏一处)。
var auditWriteMu sync.Mutex

// appendAuditJSONL 把一条 JSON 追加到审计文件 (唯一写入路径, 全进程串行)。
// best-effort: 失败静默, 审计永不阻断主流程。
func appendAuditJSONL(path string, entry map[string]interface{}) {
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	auditWriteMu.Lock()
	defer auditWriteMu.Unlock()
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer fh.Close()
	_, _ = fh.Write(append(data, '\n'))
}

// langEmoji returns an emoji for the language.

// noEmoji: set FORGE_NO_EMOJI=1 to disable emoji in tool output
// (old Windows consoles without emoji fonts render them as boxes).
var noEmoji = os.Getenv("FORGE_NO_EMOJI") == "1"

func asciiLangEmoji(lang string) string {
	switch lang {
	case "python":
		return "[Py]"
	case "go":
		return "[Go]"
	case "sh", "bash":
		return "[sh]"
	case "node", "javascript":
		return "[JS]"
	case "math":
		return "[S]"
	case "logic":
		return "[-]"
	case "knowledge":
		return "[KB]"
	case "regex":
		return "[RX]"
	case "chain":
		return "[CH]"
	case "self":
		return "[SELF]"
	case "tcm":
		return "[TCM]"
	case "browser":
		return "[BR]"
	default:
		return "[TOOL]"
	}
}

func langEmoji(lang string) string {
	if noEmoji {
		return asciiLangEmoji(lang)
	}
	switch lang {
	case "python":
		return "🐍"
	case "go":
		return "🔵"
	case "sh", "bash":
		return "💻"
	case "node", "javascript":
		return "🟢"
	case "math":
		return "🔢"
	case "logic":
		return "🧠"
	case "knowledge":
		return "📚"
	case "regex":
		return "🔍"
	case "chain":
		return "⛓️"
	case "self":
		return "🧬"
	case "tcm":
		return "☯️"
	case "browser":
		return "🌐"
	default:
		return "🔧"
	}
}

// cacheTTLForLang 返回缓存 TTL 秒数: 慢外部 gate 用长 TTL 提升复用率。
// 其余 gate 保持默认 600s。
func cacheTTLForLang(lang string) int {
	switch lang {
	case "knowledge", "browser", "tcm":
		return 1800
	}
	return 600
}

func (f *Forge) forgeGateSkipCache(code, lang, input string, skipCache bool) ForgeGateResult {
	cacheKey := f.cacheKey(code, lang, input)
	if !skipCache {
		f.cacheMu.RLock()
		if cached, ok := f.cache[cacheKey]; ok {
			// TTL invalidation: stale entries (and old-format entries with
			// CachedAt==0) are treated as misses and re-executed, so the agent
			// never serves long-outdated results for stateful code.
			// 慢 gate 长 TTL: knowledge/browser/tcm 外部延迟高,
			// 600s TTL 复用率低 → 1800s; 其余保持 600s。
			cacheTTLSeconds := cacheTTLForLang(lang)
			if time.Now().Unix()-cached.CachedAt > int64(cacheTTLSeconds) {
				f.cacheMu.RUnlock()
				f.cacheMu.Lock()
				delete(f.cache, cacheKey)
				for i, k := range f.cacheKeys {
					if k == cacheKey {
						f.cacheKeys = append(f.cacheKeys[:i], f.cacheKeys[i+1:]...)
						break
					}
				}
				f.cacheMu.Unlock()
			} else {
				f.cacheMu.RUnlock()
				f.statCacheHits.Add(1)
				slog.Debug("forge cache hit", "key", cacheKey)
				return cached
			}
		} else {
			f.cacheMu.RUnlock()
		}
	}
	start := time.Now()
	codeSize := len(code)
	codeLines := len(strings.Split(strings.TrimSpace(code), "\n"))
	compiler, ok := 铸剑炉_COMPILERS[lang]
	if !ok {
		detected := forgeDetectLang(code, "python")
		if detected != lang {
			slog.Info("forge lang fallback", "requested", lang, "detected", detected)
			lang = detected
			compiler, ok = 铸剑炉_COMPILERS[lang]
		}
		if !ok {
			result := ForgeGateResult{
				OK: false, Lang: lang, Stage: "compile",
				Error:    fmt.Sprintf("不支持的语言: %s。支持的语言: %s", lang, f.supportedLangs()),
				Duration: time.Since(start).Milliseconds(),
				CodeSize: codeSize, CodeLines: codeLines,
			}
			if !skipCache {
				f.cacheResult(cacheKey, result)
			}
			return result
		}
	}
	if strings.TrimSpace(code) == "" {
		result := ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error: "代码为空", Duration: time.Since(start).Milliseconds(),
			CodeSize: codeSize, CodeLines: codeLines,
		}
		if !skipCache {
			f.cacheResult(cacheKey, result)
		}
		return result
	}

	var result ForgeGateResult
	f.statBuilds.Add(1)
	if compiler.SelfHosted {
		result = f.forgeGateSelfHosted(code, lang, compiler, input, start)
	} else if compiler.InlineCode {
		result = f.forgeGateInline(code, lang, compiler, input, start)
	} else {
		result = f.forgeGateFile(code, lang, compiler, input, start)
	}
	result.CodeSize = codeSize
	result.CodeLines = codeLines
	result.Summary = summarizeOutput(result.Stdout, result.Stderr)
	if !result.OK {
		f.statErrors.Add(1)
	}

	// Wire up the CompilerError subsystem: on failure, parse stderr into
	// structured diagnostics (JSON) so callers get precise error locations.
	if !result.OK && result.Stderr != "" {
		if diag := compilerErrorsToJSON(parseCompilerError(result.Lang, result.Stderr)); diag != "" {
			result.Diagnostics = diag
		}
	}

	// Only cache successful results or permanent failures.
	// Transient failures (timeouts, tool-not-found) are NOT cached
	// so they can be retried on next invocation.
	if !skipCache && (result.OK || !isTransientError(result)) {
		f.cacheResult(cacheKey, result)
	}
	return result
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
	if r.Timeout {
		return true
	}
	// 执行阶段的失败 = 用户代码自身失败, 与环境瞬时无关 → 一律可缓存。
	if r.Stage == "execute" {
		return false
	}
	// 编译/启动/路径解析阶段: 工具链缺失或网络不可达属环境瞬时。
	e := strings.ToLower(r.Error)
	return strings.Contains(e, "timeout") ||
		strings.Contains(e, "not found") ||
		strings.Contains(e, "找不到") ||
		strings.Contains(e, "http error") ||
		strings.Contains(e, "connection") ||
		strings.Contains(e, "busy") ||
		strings.Contains(e, "shutting down")
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

func (f *Forge) forgeGate(code, lang, input string) ForgeGateResult {
	return f.forgeGateSkipCache(code, lang, input, false)
}

func (f *Forge) cacheKey(code, lang, input string) string {
	h := sha256.New()
	h.Write([]byte(code))
	h.Write([]byte{0})
	h.Write([]byte(lang))
	h.Write([]byte{0})
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

func (f *Forge) cacheResult(key string, r ForgeGateResult) {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	if f.cacheMaxSize > 0 && len(f.cache) >= f.cacheMaxSize && len(f.cacheKeys) > 0 {
		// FIFO eviction: remove oldest entry
		oldest := f.cacheKeys[0]
		f.cacheKeys = f.cacheKeys[1:]
		delete(f.cache, oldest)
	}
	r.CachedAt = time.Now().Unix()
	f.cache[key] = r
	f.cacheKeys = append(f.cacheKeys, key)
	f.cacheSaveCounter++
	if f.cacheSaveCounter%50 == 0 {
		go f.saveCacheToDisk()
	}
}

// shouldFallback returns true when a forge error is likely environmental
// (missing tool, timeout) and a fallback language might succeed.
func shouldFallback(r ForgeGateResult) bool {
	if r.OK {
		return false
	}
	// 超时不换语言：同一份工作量换语言重跑会再烧一个超时周期。
	if r.Timeout {
		return false
	}
	errLower := strings.ToLower(r.Error)
	// Timeout or tool-not-found are environmental
	if strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "not found") ||
		strings.Contains(errLower, "找不到") ||
		strings.Contains(errLower, "unsupported language") {
		return true
	}
	return false
}

// pickFallback suggests an alternative language when the primary fails.
func pickFallback(lang string) string {
	switch lang {
	case "sh", "bash":
		return "python" // shell failed → try python
	case "python":
		return "node" // python failed → try node
	case "node", "js":
		return "python"
	default:
		return ""
	}
}

func (f *Forge) supportedLangs() string {
	langs := make([]string, 0, len(铸剑炉_COMPILERS))
	for k := range 铸剑炉_COMPILERS {
		langs = append(langs, k)
	}
	slices.Sort(langs)
	return strings.Join(langs, ", ")
}

func (f *Forge) forgeGateSelfHosted(code, lang string, compiler CompilerDef, input string, start time.Time) ForgeGateResult {

	// gate 启用拦截 (FORGE_GATES_ENABLED 配置; 空 = 全部启用)
	var enabled []string
	if f.cfg != nil {
		enabled = f.cfg.GatesEnabled
	}
	if !gateEnabled(lang, enabled) {
		return ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error:    fmt.Sprintf("gate 未启用: %s (FORGE_GATES_ENABLED 配置), 可用的自托管 gate: %s", lang, strings.Join(gateNames(), ", ")),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	switch lang {
	case "go":
		return f.selfHostedGo(code, compiler, start)
	case "sh":
		return f.selfHostedSh(code, input, start)
	case "math":
		// Auto-wrap plain expressions as JSON for math_gate
		// simplify handles arithmetic, sqrt, trig, symbolics in one pass
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"expr": code, "action": "simplify"})
			code = string(b)
		}
		return f.selfHostedGate("math_gate", code, start)
	case "logic":
		// Auto-wrap plain logic expressions as JSON for logic_gate (Z3 SAT)
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "sat", "code": code})
			code = string(b)
		}
		return f.selfHostedGate("logic_gate", code, start)
	case "knowledge":
		// Auto-wrap plain query as JSON for knowledge_gate (SPARQL)
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "query", "query": code})
			code = string(b)
		}
		return f.selfHostedGate("knowledge_gate", code, start)
	case "regex":
		// Auto-wrap plain pattern as JSON for regex_gate
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "match", "pattern": code})
			code = string(b)
		}
		return f.selfHostedGate("regex_gate", code, start)
	case "chain":
		return f.selfHostedChain(code, input, start)
	case "tcm":
		// Auto-wrap plain text as JSON for tcm_gate (中医认知诊断)
		if !validGateJSON(code) {
			// 药对检索: "查药对 附子 干姜" / "药对：白芍、枳实" / "配伍 桃仁 红花"
			if m := herbPairInputRE.FindStringSubmatch(code); m != nil {
				b, _ := json.Marshal(map[string]string{"type": "herb_pair", "herb1": m[1], "herb2": m[2]})
				code = string(b)
			} else {
				b, _ := json.Marshal(map[string]string{"type": "diagnose", "text": code})
				code = string(b)
			}
		}
		return f.selfHostedGate("tcm_gate", code, start)
	case "browser":
		// Auto-wrap plain text as JSON for browser_gate (浏览器自动化)
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"action": "navigate", "url": code})
			code = string(b)
		}
		return f.selfHostedGate("browser_gate", code, start)
	case "self":
		return f.selfHostedSelf(code, input, start)
	default:
		return ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error:    fmt.Sprintf("self-hosted compiler not implemented: %s", lang),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
}

// herbPairInputRE 识别 "查药对 X Y" 式中文输入（药对/双药/配伍/同现）
var herbPairInputRE = regexp.MustCompile(`(?:查药对|药对|双药|同现药对|配伍)\s*[:：]?\s*([\p{Han}]{1,8})\s*[、,+和与及\s]\s*([\p{Han}]{1,8})`)

// validGateJSON reports whether code is a well-formed single JSON object.
// Text that merely STARTS with '{' but is invalid JSON (e.g. multi-line
// literals with raw newlines inside string values) is treated as raw input
// and gets re-wrapped by the caller so embedded newlines are JSON-escaped.
func validGateJSON(code string) bool {
	t := strings.TrimSpace(code)
	return strings.HasPrefix(t, "{") && json.Valid([]byte(t))
}

// mustHaveOutputGates: 纯判定型 gate(按 baseLang 计), 必须有判定输出。
// 空 stdout 对它们等于「判定缺席」, 不可当成功 —— 否则 gate 二进制异常会被静默吞掉。
var mustHaveOutputGates = map[string]bool{"math": true, "logic": true, "regex": true}

func (f *Forge) selfHostedGate(gateName, code string, start time.Time) ForgeGateResult {
	baseLang := strings.TrimSuffix(gateName, "_gate")
	gatePath := filepath.Join(f.toolsDir, gateName+".exe")
	if runtime.GOOS != "windows" {
		gatePath = filepath.Join(f.toolsDir, gateName)
	}
	isScript := false
	if _, statErr := os.Stat(gatePath); statErr != nil {
		scriptPath := filepath.Join(f.toolsDir, gateName+".py")
		if _, serr := os.Stat(scriptPath); serr == nil {
			gatePath = scriptPath
			isScript = true
		}
	}
	absPath, err := filepath.Abs(gatePath)
	if err != nil {
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "compile",
			Error:    fmt.Sprintf("%s path resolution failed: %v", gateName, err),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "compile",
			Error: func() string {
				suffix := ".exe"
				if runtime.GOOS != "windows" {
					suffix = ""
				}
				return fmt.Sprintf("%s%s not found -- dead boundary unavailable", gateName, suffix)
			}(),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	timeout := deadBoundaryTimeout
	if comp, ok := 铸剑炉_COMPILERS[baseLang]; ok && comp.ExecTimeout > 0 {
		timeout = comp.ExecTimeout
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if isScript {
		cmd = f.newCmd(ctx, "python", absPath, code)
	} else {
		cmd = f.newCmd(ctx, absPath, code)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = runWithTimeout(ctx, cmd)
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = stdout.String()
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "execute",
			Error:    fmt.Sprintf("%s execution failed: %s", gateName, errMsg),
			Stderr:   errMsg,
			Timeout:  isTimeoutErr(err),
			ExitCode: safeExitCode(cmd),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	output := stdout.String()

	// 判定型 gate 必须有判定输出: 空 stdout = 判定缺席, 不可当成功。
	// (旧实现 if/return 两分支逐字相同, 空输出与有输出同样 OK:true —— 判定被静默吞掉)
	if output == "" && mustHaveOutputGates[baseLang] {
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "execute",
			Error:    fmt.Sprintf("%s gate 无输出: 判定缺失(疑似 gate 二进制异常)", gateName),
			Stderr:   stderr.String(),
			ExitCode: safeExitCode(cmd),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK: true, Lang: baseLang, Stage: "done",
		Stdout:   output,
		Duration: time.Since(start).Milliseconds(),
	}
}

func (f *Forge) selfHostedGo(code string, compiler CompilerDef, start time.Time) ForgeGateResult {
	runCode := code
	if !strings.Contains(code, "package main") {
		runCode = fmt.Sprintf(`package main
import "fmt"
func main() {
%s
}`, code)
	}
	tmpDir, err := os.MkdirTemp("", "forge_go_")
	if err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "write",
			Error:    fmt.Sprintf("failed to create temp dir: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	defer os.RemoveAll(tmpDir)
	srcPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(runCode), 0644); err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "write",
			Error:    fmt.Sprintf("failed to write source: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	exePath := filepath.Join(tmpDir, "main"+exeSuffix())
	compileCtx, compileCancel := context.WithTimeout(f.effCtx(), compiler.CompileTimeout)
	defer compileCancel()
	goCmd, goErr := f.findGoCommand()
	if goErr != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "compile",
			Error:    goErr.Error(),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	compileCmd := f.newCmd(compileCtx, goCmd, "build", "-o", exePath, srcPath)
	var compileStderr strings.Builder
	compileCmd.Stderr = &compileStderr
	if err := runWithTimeout(compileCtx, compileCmd); err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "compile",
			Error:    fmt.Sprintf("go build failed: %s", compileStderr.String()),
			Stderr:   compileStderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	execCtx, execCancel := context.WithTimeout(f.effCtx(), compilerTimeout("go"))
	defer execCancel()
	execCmd := f.newCmd(execCtx, exePath)
	var stdout, execStderr strings.Builder
	execCmd.Stdout = &stdout
	execCmd.Stderr = &execStderr
	err = runWithTimeout(execCtx, execCmd)
	if err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "execute",
			Error:    fmt.Sprintf("go run failed: %v — %s", err, execStderr.String()),
			Stderr:   execStderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK: true, Lang: "go", Stage: "done",
		Stdout:   stdout.String(),
		Duration: time.Since(start).Milliseconds(),
	}
}

func (f *Forge) selfHostedSh(code, input string, start time.Time) ForgeGateResult {
	// Windows: pure static "echo <text>" bypasses cmd entirely.
	// cmd /c and .bat both corrupt non-ASCII args on GBK consoles
	// (UTF-8 bytes re-parsed as GBK), so print directly from Go.
	if runtime.GOOS == "windows" {
		if txt, ok := shEchoStaticPattern(code); ok {
			return ForgeGateResult{
				OK: true, Lang: "sh", Stage: "done",
				Stdout:   txt,
				Duration: time.Since(start).Milliseconds(),
			}
		}
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), shGateTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Hybrid approach: simple commands go through Go native exec
		// (CreateProcess, no shell), complex commands use temp .bat file.
		// This eliminates cmd /c outer-quoting which corrupts nested quotes.
		needsShell := strings.ContainsAny(code, "|&<>") ||
			strings.Contains(code, "&&") || strings.Contains(code, "||")

		// Parse command first to detect cmd builtins
		parts := forgeSplitCommand(code)
		if !needsShell && len(parts) > 0 {
			prog := strings.ToLower(parts[0])
			// cmd.exe builtins: not standalone .exe files, must use shell
			switch prog {
			case "echo", "type", "cd", "chdir", "md", "mkdir",
				"rd", "rmdir", "set", "copy", "del", "erase",
				"ren", "rename", "dir", "date", "time", "ver",
				"vol", "cls", "color", "title", "prompt",
				"pushd", "popd", "start", "assoc", "ftype":
				needsShell = true
			}
		}
		if !needsShell && len(parts) > 0 {
			// Simple command: Go native execution via CreateProcess.
			cmd = f.newCmd(ctx, parts[0], parts[1:]...)
			cmd.Dir = f.workDir
		} else if !needsShell {
			return ForgeGateResult{
				OK: false, Lang: "sh", Stage: "parse",
				Error:    "empty shell command",
				Duration: time.Since(start).Milliseconds(),
			}
		} else {
			// Complex command: write to .bat file. No cmd /c quoting layer.
			tmpDir, tmpErr := os.MkdirTemp("", "forge_sh_")
			if tmpErr != nil {
				return ForgeGateResult{
					OK: false, Lang: "sh", Stage: "write",
					Error:    fmt.Sprintf("failed to create temp dir: %v", tmpErr),
					Duration: time.Since(start).Milliseconds(),
				}
			}
			defer os.RemoveAll(tmpDir)
			batPath := filepath.Join(tmpDir, "run.bat")
			batContent := "@echo off\r\nchcp 65001 > nul 2>&1\r\n" + code + "\r\n"
			if writeErr := os.WriteFile(batPath, []byte(batContent), 0644); writeErr != nil {
				return ForgeGateResult{
					OK: false, Lang: "sh", Stage: "write",
					Error:    fmt.Sprintf("failed to write batch file: %v", writeErr),
					Duration: time.Since(start).Milliseconds(),
				}
			}
			cmd = f.newCmd(ctx, "cmd", "/c", batPath)
			cmd.Dir = f.workDir
		}
	} else {
		cmd = f.newCmd(ctx, "sh", "-c", code)
		cmd.Dir = f.workDir
	}
	if input != "" {
		cmd.Env = append(cmd.Env, ForgeInputEnv+"="+input)
		cmd.Stdin = strings.NewReader(input)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := runWithTimeout(ctx, cmd)
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return ForgeGateResult{
			OK: false, Lang: "sh", Stage: "execute",
			Error:    fmt.Sprintf("shell execution failed: %s", errMsg),
			Stderr:   errMsg,
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK: true, Lang: "sh", Stage: "done",
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start).Milliseconds(),
	}
}

// shEchoStaticPattern detects a pure static `echo <text>` (single line,
// no %, !, quotes, redirects, pipes or &) and returns the text to print.
// Returns ok=false for anything more complex so it falls through to cmd.
func shEchoStaticPattern(code string) (string, bool) {
	if strings.Contains(code, "\n") {
		return "", false
	}
	trimmed := strings.TrimSpace(code)
	if len(trimmed) < 6 || !strings.HasPrefix(strings.ToLower(trimmed), "echo ") {
		return "", false
	}
	rest := trimmed[5:]
	if strings.ContainsAny(rest, "%!<>&|^\"") {
		return "", false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "/?") {
		return "", false
	}
	return rest + "\n", true
}

// forgeSplitCommand splits a simple shell command into program + arguments,
// respecting single and double quotes. Pipes, &&, ||, redirects are NOT
// handled — those must use the .bat file path.
func forgeSplitCommand(cmdLine string) []string {
	var parts []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	for i := 0; i < len(cmdLine); i++ {
		ch := cmdLine[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == ' ' && !inSingle && !inDouble:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func (f *Forge) forgeGateInline(code, lang string, compiler CompilerDef, input string, start time.Time) ForgeGateResult {
	timeout := compiler.ExecTimeout
	if timeout == 0 {
		timeout = defaultGateTimeout
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	execArgs := make([]string, len(compiler.Exec))
	copy(execArgs, compiler.Exec)
	hasCodePlaceholder := false
	needsStdin := false
	for i, a := range execArgs {
		if a == "{code}" {
			execArgs[i] = code
			hasCodePlaceholder = true
		}
		if strings.Contains(a, "{forge}") {
			execArgs[i] = strings.ReplaceAll(a, "{forge}", f.workDir)
			if runtime.GOOS != "windows" {
				execArgs[i] = strings.ReplaceAll(execArgs[i], ".exe", "")
			}
		}
		if a == "-" {
			needsStdin = true
		}
	}
	if !hasCodePlaceholder && !needsStdin {
		execArgs = append(execArgs, code)
	}
	if len(execArgs) > 0 {
		exe := execArgs[0]
		args := execArgs[1:]
		cmd = f.newCmd(ctx, exe, args...)
	} else {
		return ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error:    "no exec command configured",
			Duration: time.Since(start).Milliseconds(),
		}
	}
	cmd.Dir = f.workDir
	if input != "" {
		cmd.Env = append(cmd.Env, ForgeInputEnv+"="+input)
		cmd.Stdin = strings.NewReader(input)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if needsStdin {
		cmd.Stdin = strings.NewReader(code)
	} else if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	err := runWithTimeout(ctx, cmd)
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = stdout.String()
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		return ForgeGateResult{
			OK:       false,
			Lang:     lang,
			Stage:    "execute",
			Error:    fmt.Sprintf("%s execution failed: %s", lang, errMsg),
			Stderr:   errMsg,
			Timeout:  isTimeoutErr(err),
			ExitCode: safeExitCode(cmd),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK:       true,
		Lang:     lang,
		Stage:    "done",
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(start).Milliseconds(),
	}
}

func (f *Forge) forgeGateFile(code, lang string, compiler CompilerDef, input string, start time.Time) ForgeGateResult {
	ext := compiler.Ext
	if ext == "" {
		ext = ".code"
	}
	tmpDir, err := os.MkdirTemp("", "forge_gate_")
	if err != nil {
		return ForgeGateResult{OK: false, Lang: lang, Stage: "write",
			Error: fmt.Sprintf("failed to create temp dir: %v", err), Duration: time.Since(start).Milliseconds()}
	}
	defer os.RemoveAll(tmpDir)
	srcPath := filepath.Join(tmpDir, "code"+ext)

	// Ensure UTF-8 BOM-free
	if writeErr := os.WriteFile(srcPath, []byte(code), 0644); writeErr != nil {
		return ForgeGateResult{OK: false, Lang: lang, Stage: "write",
			Error: fmt.Sprintf("failed to write source: %v", writeErr), Duration: time.Since(start).Milliseconds()}
	}

	// Check/lint phase
	if len(compiler.Check) > 0 {
		checkStart := time.Now()
		checkArgs, hasFilePlaceholder := expandArgs(compiler.Check, srcPath, f.workDir, tmpDir)
		if !hasFilePlaceholder {
			checkArgs = append(checkArgs, srcPath)
		}
		checkCtx, checkCancel := context.WithTimeout(f.effCtx(), compiler.CompileTimeout)
		defer checkCancel()
		checkCmd := f.newCmd(checkCtx, checkArgs[0], checkArgs[1:]...)
		checkCmd.Dir = f.workDir
		var checkStderr strings.Builder
		checkCmd.Stderr = &checkStderr
		if checkErr := runWithTimeout(checkCtx, checkCmd); checkErr != nil {
			return ForgeGateResult{OK: false, Lang: lang, Stage: "compile",
				Error:  fmt.Sprintf("%s syntax check failed (%v): %s", lang, checkErr, checkStderr.String()),
				Stderr: checkStderr.String(), Timeout: isTimeoutErr(checkErr), Duration: time.Since(start).Milliseconds()}
		}
		slog.Debug("forge check", "lang", lang, "duration_ms", time.Since(checkStart).Milliseconds())
	}

	// Lint phase
	var lintOutput string
	if len(compiler.Lint) > 0 {
		lintStart := time.Now()
		lintArgs, hasFilePlaceholder := expandArgs(compiler.Lint, srcPath, f.workDir, tmpDir)
		if !hasFilePlaceholder {
			lintArgs = append(lintArgs, srcPath)
		}
		lintCtx, lintCancel := context.WithTimeout(f.effCtx(), compiler.CompileTimeout)
		defer lintCancel()
		lintCmd := f.newCmd(lintCtx, lintArgs[0], lintArgs[1:]...)
		lintCmd.Dir = f.workDir
		var lintStdout, lintStderr strings.Builder
		lintCmd.Stdout = &lintStdout
		lintCmd.Stderr = &lintStderr
		if lintErr := runWithTimeout(lintCtx, lintCmd); lintErr != nil {
			slog.Warn("forge lint warning", "lang", lang, "stderr", lintStderr.String())
		}
		lintOutput = lintStdout.String()
		if lintStderr.Len() > 0 {
			if lintOutput != "" {
				lintOutput += "\n"
			}
			lintOutput += lintStderr.String()
		}
		slog.Debug("forge lint", "lang", lang, "duration_ms", time.Since(lintStart).Milliseconds())
	}

	// Execute phase
	execArgs, hasFilePlaceholder := expandArgs(compiler.Exec, srcPath, f.workDir, tmpDir)
	if !hasFilePlaceholder {
		if len(execArgs) == 0 {
			return ForgeGateResult{OK: false, Lang: lang, Stage: "execute",
				Error: fmt.Sprintf("%s: no exec command configured", lang), Duration: time.Since(start).Milliseconds()}
		}
		execArgs = append(execArgs, srcPath)
	}
	execCtx, execCancel := context.WithTimeout(f.effCtx(), compiler.ExecTimeout)
	defer execCancel()
	cmd := f.newCmd(execCtx, execArgs[0], execArgs[1:]...)
	cmd.Dir = f.workDir
	if input != "" {
		cmd.Env = append(cmd.Env, ForgeInputEnv+"="+input)
		cmd.Stdin = strings.NewReader(input)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	execErr := runWithTimeout(execCtx, cmd)
	if execErr != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = execErr.Error()
		}
		stdOut := stdout.String()
		stdErr := stderr.String()
		// Runtime errors with output are results, not gate failures.
		ok := len(stdOut) > 0 || len(stdErr) > 0
		return ForgeGateResult{OK: ok, Lang: lang, Stage: "execute",
			Error:   fmt.Sprintf("%s execution failed: %s", lang, errMsg),
			Timeout: isTimeoutErr(execErr),
			Stdout:  stdOut, Stderr: stdErr, Lint: lintOutput,
			ExitCode: safeExitCode(cmd), Duration: time.Since(start).Milliseconds()}
	}
	return ForgeGateResult{OK: true, Lang: lang, Stage: "done",
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0,
		Duration: time.Since(start).Milliseconds()}
}

// expandArgs 在参数数组中展开 {file}/{forge}/{dir} 占位符，返回展开后的参数和是否含 {file} 占位符。
func expandArgs(args []string, srcPath, workDir, tmpDir string) ([]string, bool) {
	expanded := make([]string, len(args))
	copy(expanded, args)
	place := false
	for i, a := range expanded {
		if a == "{file}" {
			expanded[i] = srcPath
			place = true
		}
		if strings.Contains(a, "{forge}") {
			expanded[i] = strings.ReplaceAll(a, "{forge}", workDir)
			if runtime.GOOS != "windows" {
				expanded[i] = strings.ReplaceAll(expanded[i], ".exe", "")
			}
		}
		if strings.Contains(a, "{dir}") {
			expanded[i] = strings.ReplaceAll(a, "{dir}", tmpDir)
		}
	}
	return expanded, place
}

// newCmd creates a subprocess command with UTF-8 output forced for all
// child processes. Windows Python otherwise inherits the ANSI code page
// (GBK on zh-CN systems) and emits GBK bytes that the UTF-8 console
// renders as mojibake. Setting these env vars makes every gate's output
// consistently UTF-8.
func (f *Forge) newCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"PYTHONUTF8=1",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	)
	return cmd
}

// safeExitCode returns the exit code of a command, or -1 if ProcessState is nil.
func safeExitCode(cmd *exec.Cmd) int {
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

// findGoCommand 查找 Go 编译器路径。
// 优先级：1) 系统 PATH  2) .forge/go/bin/go  3) 自动下载
func (f *Forge) findGoCommand() (string, error) {
	if goPath, err := exec.LookPath("go"); err == nil {
		return goPath, nil
	}
	localGo := filepath.Join(f.workDir, ".forge", "go", "bin", "go"+exeSuffix())
	if _, err := os.Stat(localGo); err == nil {
		return localGo, nil
	}
	if err := f.downloadGo(); err != nil {
		return "", fmt.Errorf("go toolchain not found and auto-download failed: %w\nInstall Go manually: https://go.dev/dl/", err)
	}
	if _, err := os.Stat(localGo); err == nil {
		return localGo, nil
	}
	return "", errors.New("go toolchain not found. Install Go: https://go.dev/dl/")
}

// downloadGo 从 Go 官网自动下载工具链到 .forge/go/
func (f *Forge) downloadGo() error {
	goVersion := runtime.Version()
	goOS := runtime.GOOS
	goArch := runtime.GOARCH

	// 映射 Go 下载包中的架构名
	archMap := map[string]string{"arm": "armv6l"}
	if mapped, ok := archMap[goArch]; ok {
		goArch = mapped
	}
	ext := "tar.gz"
	if goOS == "windows" {
		ext = "zip"
	}
	filename := fmt.Sprintf("%s.%s-%s.%s", goVersion, goOS, goArch, ext)
	url := "https://go.dev/dl/" + filename
	tmpFile := filepath.Join(os.TempDir(), filename)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	fh, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer fh.Close()
	defer os.Remove(tmpFile)
	if _, err := io.Copy(fh, resp.Body); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	fh.Close()
	goDir := filepath.Join(f.workDir, ".forge")
	os.RemoveAll(filepath.Join(goDir, "go"))
	if goOS == "windows" {
		if err := forgeUnzip(tmpFile, goDir); err != nil {
			return fmt.Errorf("unzip Go: %w", err)
		}
	} else {
		if err := forgeUntarGz(tmpFile, goDir); err != nil {
			return fmt.Errorf("untar Go: %w", err)
		}
	}
	return nil
}

// forgeUnzip 解压 zip 文件到目标目录
func forgeUnzip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		targetPath := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("zip slip detected: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}
		os.MkdirAll(filepath.Dir(targetPath), 0755)
		rc, err := f.Open()
		if err != nil {
			return err
		}
		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// forgeUntarGz 解压 tar.gz 文件到目标目录
func forgeUntarGz(tarGzPath, destDir string) error {
	fh, err := os.Open(tarGzPath)
	if err != nil {
		return err
	}
	defer fh.Close()
	gzReader, err := gzip.NewReader(fh)
	if err != nil {
		return err
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		targetPath := filepath.Join(destDir, header.Name)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar slip detected: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(targetPath, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(targetPath), 0755)
			outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()
		}
	}
	return nil
}

// summarizeOutput generates a brief summary of execution output.
// Long outputs get head+tail truncation with line count.
func summarizeOutput(stdout, stderr string) string {
	var parts []string
	if stdout != "" {
		trimmed := strings.TrimSpace(stdout)
		lines := strings.Split(trimmed, "\n")
		n := len(lines)
		if n <= 6 {
			parts = append(parts, trimmed)
		} else {
			head := strings.Join(lines[:3], "\n")
			tail := strings.Join(lines[n-3:], "\n")
			parts = append(parts, fmt.Sprintf("%s\n... (%d lines total) ...\n%s", head, n, tail))
		}
	}
	if stderr != "" {
		stderrTrimmed := strings.TrimSpace(stderr)
		if len(stderrTrimmed) > 200 {
			stderrTrimmed = stderrTrimmed[:200] + "..."
		}
		parts = append(parts, "[stderr] "+stderrTrimmed)
	}
	return strings.Join(parts, " | ")
}

// saveCacheToDisk persists the in-memory cache to disk using gob encoding.
// cachePersistFormat is the on-disk format for the forge cache.
// It preserves both the map and the FIFO ordering of keys.
type cachePersistFormat struct {
	Entries map[string]ForgeGateResult
	Keys    []string // FIFO insertion order
}

func (f *Forge) saveCacheToDisk() {
	if f.cachePersistFile == "" {
		return
	}
	f.cacheDiskMu.Lock()
	defer f.cacheDiskMu.Unlock()
	f.cacheMu.RLock()
	// Copy under lock to minimize lock time
	data := cachePersistFormat{
		Entries: make(map[string]ForgeGateResult, len(f.cache)),
		Keys:    make([]string, len(f.cacheKeys)),
	}
	for k, v := range f.cache {
		data.Entries[k] = v
	}
	copy(data.Keys, f.cacheKeys)
	f.cacheMu.RUnlock()

	// 原子写: 先写 .tmp 再 rename, 避免崩溃/升级强杀时截断主缓存文件
	tmp := f.cachePersistFile + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		slog.Warn("forge cache save failed", "error", err)
		return
	}
	enc := gob.NewEncoder(file)
	if err := enc.Encode(data); err != nil {
		file.Close()
		os.Remove(tmp)
		slog.Warn("forge cache encode failed", "error", err)
		return
	}
	if err := file.Close(); err != nil {
		os.Remove(tmp)
		slog.Warn("forge cache close failed", "error", err)
		return
	}
	if err := os.Rename(tmp, f.cachePersistFile); err != nil {
		os.Remove(tmp)
		slog.Warn("forge cache rename failed", "error", err)
		return
	}
	slog.Debug("forge cache saved to disk", "entries", len(data.Entries))
}

// loadCacheFromDisk restores the cache from a gob-encoded file on startup.
func (f *Forge) loadCacheFromDisk() {
	if f.cachePersistFile == "" {
		return
	}
	file, err := os.Open(f.cachePersistFile)
	if err != nil {
		return // file doesn't exist yet, that's fine
	}
	defer file.Close()
	dec := gob.NewDecoder(file)

	// Try new format first (cachePersistFormat), fall back to old format (bare map)
	var data cachePersistFormat
	if err := dec.Decode(&data); err != nil {
		// Old format: bare map[string]ForgeGateResult
		file.Seek(0, 0)
		dec = gob.NewDecoder(file)
		var loaded map[string]ForgeGateResult
		if err2 := dec.Decode(&loaded); err2 != nil {
			slog.Warn("forge cache load failed, starting fresh", "error", err2)
			return
		}
		data.Entries = loaded
		data.Keys = make([]string, 0, len(loaded))
		for k := range loaded {
			data.Keys = append(data.Keys, k)
		}
	}
	f.cacheMu.Lock()
	f.cache = data.Entries
	f.cacheKeys = data.Keys
	// Enforce cacheMaxSize: truncate the restored cache to the configured limit
	// (keep the most recent entries per FIFO order).
	if f.cacheMaxSize > 0 && len(f.cacheKeys) > f.cacheMaxSize {
		keep := f.cacheKeys[len(f.cacheKeys)-f.cacheMaxSize:]
		f.cacheKeys = keep
		trimmed := make(map[string]ForgeGateResult, len(keep))
		for _, k := range keep {
			if v, ok := f.cache[k]; ok {
				trimmed[k] = v
			}
		}
		f.cache = trimmed
	}
	f.cacheMu.Unlock()
	slog.Info("forge cache loaded from disk", "entries", len(f.cache))
}

func (f *Forge) formatResult(r ForgeGateResult) string {
	var sb strings.Builder
	emoji := langEmoji(r.Lang)
	langLabel := strings.ToUpper(r.Lang)

	// Build body first, truncate, then prepend header (preserves header budget)
	fullBody := ""
	if r.Stdout != "" {
		fullBody = r.Stdout
	}
	if r.Stderr != "" {
		if fullBody != "" {
			fullBody += "\n--- stderr ---\n"
		}
		fullBody += r.Stderr
	}
	if r.Error != "" && r.Stderr == "" {
		if fullBody != "" {
			fullBody += "\n--- error ---\n"
		}
		fullBody += r.Error
		if r.Lint != "" {
			if fullBody != "" {
				fullBody += "\n--- lint ---\n"
			}
			fullBody += r.Lint
		}
	}
	if r.Diagnostics != "" {
		if fullBody != "" {
			fullBody += "\n"
		}
		fullBody += "--- diagnostics ---\n" + r.Diagnostics
	}
	truncatedBody := truncateOutput(fullBody)
	if r.OK {
		sb.WriteString(fmt.Sprintf("%s --- %s 成功 (%dms, %d lines) ---\n",
			emoji, langLabel, r.Duration, r.CodeLines))
	} else {
		sb.WriteString(fmt.Sprintf("%s --- %s 失败 (exit=%d, %dms, stage=%s) ---\n",
			emoji, langLabel, r.ExitCode, r.Duration, r.Stage))
	}
	sb.WriteString(truncatedBody)
	if r.Summary != "" {
		summaryText := truncateOutput(r.Summary)
		sb.WriteString("\n--- summary ---\n")
		sb.WriteString(summaryText)
	}
	return sb.String()
}

// auditAttempt 记录单次 gate 尝试的失败明细 (仅失败时写, 正常路径零开销)。
// 目的: 区分「首次失败原因」与「最终失败原因」—— 此前 gate_audit 只有 Retries 总数,
// 无法解释「超时却重试了 2 次」这类反常 (20260913 实测 148 条 timeout 记录 retries=2,
// 单次 duration 累计 93s ≈ 3×30s + backoff, 但 retryGate 明写超时不重试)。
func (f *Forge) auditAttempt(lang string, n int, r ForgeGateResult) {
	if r.OK {
		return
	}
	if os.Getenv("FORGE_GATE_AUDIT") == "0" {
		return
	}
	snip := r.Error
	if len(snip) > 120 {
		snip = snip[:120]
	}
	appendAuditJSONL(auditFilePath(f.workDir), map[string]interface{}{
		"event":   "gate_attempt",
		"ts":      time.Now().Format(time.RFC3339Nano),
		"lang":    lang,
		"attempt": n,
		"timeout": r.Timeout,
		"stage":   r.Stage,
		"ms":      r.Duration,
		"err":     snip,
	})
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
	delete(f.cache, cacheKey)
	for i, k := range f.cacheKeys {
		if k == cacheKey {
			f.cacheKeys = append(f.cacheKeys[:i], f.cacheKeys[i+1:]...)
			break
		}
	}
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
		// Clear cache again before next retry
		f.cacheMu.Lock()
		delete(f.cache, cacheKey)
		f.cacheMu.Unlock()
	}
	result.Retries = f.retryMax - 1
	return result
}

func (f *Forge) selfHostedChain(code, input string, start time.Time) ForgeGateResult {
	// Parse chain specification: {"stages":[{"gate":"...","input":{...},"if_verdict":"..."}],"stop_on":"error|first_success|never"}
	var req struct {
		Stages []struct {
			Gate      string          `json:"gate"`
			Input     json.RawMessage `json:"input"`
			IfVerdict string          `json:"if_verdict,omitempty"`
		} `json:"stages"`
		StopOn string `json:"stop_on"`
	}
	if err := json.Unmarshal([]byte(code), &req); err != nil {
		return ForgeGateResult{
			OK: false, Lang: "chain", Stage: "parse",
			Error:    fmt.Sprintf("invalid chain JSON: %v", err),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	if req.StopOn == "" {
		req.StopOn = "error"
	}

	type stageResult struct {
		Index     int    `json:"index"`
		Gate      string `json:"gate"`
		OK        bool   `json:"ok"`
		Stdout    string `json:"stdout,omitempty"`
		Error     string `json:"error,omitempty"`
		LatencyMs int64  `json:"latency_ms"`
	}

	var stages []stageResult
	var prevOutput string // for if_verdict substring matching
	for i, stage := range req.Stages {
		// if_verdict: skip stage if previous output doesn't contain verdict string
		if stage.IfVerdict != "" && i > 0 && !strings.Contains(prevOutput, stage.IfVerdict) {
			continue
		}

		// Extract actual code and optional input from stage's input field
		stageCode := string(stage.Input)
		stageInput := ""

		// If input is a plain JSON string literal (e.g. "fof(...)"), unwrap the quotes
		var plainStr string
		if json.Unmarshal(stage.Input, &plainStr) == nil {
			stageCode = plainStr
		}

		// If input has a "code" field (for external gates like python/node/sh),
		// extract it as the main code and pass remaining fields as the input arg.
		var rawInput map[string]json.RawMessage
		if json.Unmarshal(stage.Input, &rawInput) == nil {
			if codeField, ok := rawInput["code"]; ok {
				var codeStr string
				if json.Unmarshal(codeField, &codeStr) == nil {
					stageCode = codeStr
					delete(rawInput, "code")
					if len(rawInput) > 0 {
						if remaining, err := json.Marshal(rawInput); err == nil {
							stageInput = string(remaining)
						}
					}
				}
			}
		}
		stageStart := time.Now()
		result := f.forgeGateSkipCache(stageCode, stage.Gate, stageInput, false)
		sr := stageResult{
			Index:     i,
			Gate:      stage.Gate,
			OK:        result.OK,
			Stdout:    truncateOutput(result.Stdout),
			Error:     result.Error,
			LatencyMs: time.Since(stageStart).Milliseconds(),
		}
		stages = append(stages, sr)
		prevOutput = result.Stdout
		if !result.OK {
			prevOutput += "\n" + result.Error
		}
		if req.StopOn == "error" && !result.OK {
			break
		}
		if req.StopOn == "first_success" && result.OK {
			break
		}
	}
	allOK := true
	for _, s := range stages {
		if !s.OK {
			allOK = false
			break
		}
	}
	output, _ := json.Marshal(map[string]interface{}{
		"ok":     allOK,
		"stages": stages,
	})
	return ForgeGateResult{
		OK:       allOK,
		Lang:     "chain",
		Stage:    "done",
		Stdout:   string(output),
		Duration: time.Since(start).Milliseconds(),
	}
}

func (f *Forge) selfHostedSelf(code, input string, start time.Time) ForgeGateResult {
	// 产物名恒为 forge_new.exe: 就位统一由 deploySelfExe 负责。
	// 旧版按自身进程名切换目标(build -o forge.exe 直接覆盖生产 exe) =
	// 「没冒烟就先替换」, 正是本轮回补的缺口。
	targetExe := "forge_new.exe"
	action := input
	if action == "" {
		action = "append"
	}

	// restart 动作已废弃：不再触发任何行为，直接返回。
	if action == "restart" {
		return ForgeGateResult{
			OK: true, Lang: "self", Stage: "restart",
			Summary:  "restart 动作已废弃，不再生效。",
			ExitCode: 0,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	srcPath := filepath.Join(f.workDir, "forge.go")
	var backupPath string // self gate 改动前备份路径; go build 失败时自动回滚用

	// Backup forge.go before modification (defense against bad self-modification).
	// Keep at most 10 backups and prune older ones so the directory never grows unbounded.
	if action != "build" {
		backupPath = srcPath + ".bak_self_" + time.Now().Format("20060102_150405.000000000")
		src0, err0 := os.ReadFile(srcPath)
		if err0 != nil {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("self gate: 读取 forge.go 失败, 拒绝无备份改动: %v", err0),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		if err := os.WriteFile(backupPath, src0, 0644); err != nil {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("self gate: 备份失败, 拒绝改动: %v", err),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		pruneSelfBackups(srcPath, 10)
		// 统一快照(源码+记忆+gate+exe) + git 历史点。
		// self gate 强制主人审批, 批准后执行到此处才落快照。
		ts := time.Now().Format("20060102_150405")
		if err := createCheckpoint(f.workDir, ts, "self"); err == nil {
			if h := gitSnapshot(f.workDir, "self"); h != "" {
				logEvent(EvSelfModified, "self-git-snapshot", map[string]string{"git": h})
			}
		}
	}
	switch {
	case action == "build":
		// just rebuild, no source modification
	case strings.HasPrefix(action, "replace:"):
		rest := action[8:]
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    "replace action requires format: replace:old_text:new_text",
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		src, err := os.ReadFile(srcPath)
		if err != nil {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("读取铸剑炉.go: %v", err),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		if !strings.Contains(string(src), parts[0]) {
			prev := []rune(parts[0])
			if len(prev) > 40 {
				prev = prev[:40]
			}
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("replace 未命中: old_text 不存在于 forge.go (前 40 字: %s)", string(prev)),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		newSrc := strings.Replace(string(src), parts[0], parts[1], 1)
		if err := os.WriteFile(srcPath, []byte(newSrc), 0644); err != nil {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("写入铸剑炉.go: %v", err),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		logEvent(EvSelfModified, "replace", map[string]string{"file": "forge.go"})
	default: // "append"
		src, err := os.ReadFile(srcPath)
		if err != nil {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("读取铸剑炉.go: %v", err),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		srcStr := strings.ReplaceAll(string(src), "\r\n", "\n")
		marker := "func ForgeToolSchema() json.RawMessage {"
		idx := strings.LastIndex(srcStr, marker)
		if idx < 0 {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    "标记 'func ForgeToolSchema()' 未在forge.go中找到",
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		if strings.TrimSpace(code) == "" {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    "self gate: code is empty, cannot append to forge.go",
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		if !looksLikeValidGoTopLevel(code) {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("self gate: code does not look like a valid Go top-level declaration: %s", code),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		newSrc := srcStr[:idx] + code + "\n" + srcStr[idx:]
		if err := os.WriteFile(srcPath, []byte(newSrc), 0644); err != nil {
			return ForgeGateResult{
				OK: false, Lang: "self", Stage: "compile",
				Error:    fmt.Sprintf("写入铸剑炉.go: %v", err),
				ExitCode: -1,
				Duration: time.Since(start).Milliseconds(),
			}
		}
		logEvent(EvSelfModified, "append", map[string]string{"file": "forge.go"})
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), 60*time.Second)
	defer cancel()
	goCmd, goErr := f.findGoCommand()
	if goErr != nil {
		return ForgeGateResult{
			OK: false, Lang: "self", Stage: "compile",
			Error:    goErr.Error(),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	targetPath := filepath.Join(f.workDir, targetExe)
	// 清理陈旧产物: 上一轮编译成功但未就位的 forge_new.exe 若留着,
	// 之后被运行会把 exe 换成更旧的版本。清不掉(被占用)则交给 go build 覆盖。
	if _, serr := os.Stat(targetPath); serr == nil {
		stale := targetPath + ".stale_" + time.Now().Format("20060102_150405")
		if rerr := os.Rename(targetPath, stale); rerr == nil {
			os.Remove(stale)
		}
	}
	cmd := f.newCmd(ctx, goCmd, "build", "-o", targetPath, ".")
	cmd.Dir = f.workDir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := runWithTimeout(ctx, cmd); err != nil {
		// 编译失败 → 自动回滚 forge.go 到本次改动前的备份(不留半改状态)。
		note := ""
		if backupPath != "" {
			if b, rerr := os.ReadFile(backupPath); rerr == nil {
				if werr := os.WriteFile(srcPath, b, 0644); werr == nil {
					note = "\n[self gate 已自动回滚 forge.go 到改动前版本]"
				} else {
					note = fmt.Sprintf("\n[回滚失败: %v, 备份: %s]", werr, backupPath)
				}
			} else {
				note = fmt.Sprintf("\n[备份不可读: %v, 备份: %s]", rerr, backupPath)
			}
		}
		return ForgeGateResult{
			OK: false, Lang: "self", Stage: "compile",
			Error:    fmt.Sprintf("go build failed: %s%s", stderr.String(), note),
			Stderr:   stderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	// ── 部署阶段: 编译成功 != 能跑 ──
	// action=build 保持原语义(只编译); FORGE_SELF_NODEPLOY=1 是应急刹车。
	if action == "build" {
		return ForgeGateResult{
			OK: true, Lang: "self", Stage: "done",
			Stdout:   fmt.Sprintf("built %s successfully (build 动作: 不部署)\n%s", targetExe, stdout.String()),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	if os.Getenv("FORGE_SELF_NODEPLOY") == "1" {
		return ForgeGateResult{
			OK: true, Lang: "self", Stage: "done",
			Stdout:   fmt.Sprintf("built %s successfully (FORGE_SELF_NODEPLOY=1: 跳过部署)\n%s", targetExe, stdout.String()),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	outcome, derr := deploySelfExe(f.workDir, targetPath, 10, realSmokeRunner)
	if derr != nil {
		logEvent(EvSelfModified, "self-deploy-failed", map[string]string{
			"err": derr.Error(), "sha256": outcome.SHA256,
		})
		return ForgeGateResult{
			OK: false, Lang: "self", Stage: "deploy",
			Error:    fmt.Sprintf("编译成功但部署失败: %v\n[旧 exe 未被破坏, 源码保留待修]", derr),
			Stdout:   stdout.String(),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	logEvent(EvSelfRestart, "self-deploy", map[string]string{
		"sha256":   outcome.SHA256,
		"backup":   outcome.BackupPath,
		"smoke_ms": strconv.FormatInt(outcome.SmokeMS, 10),
		"exe":      "forge.exe",
	})
	report := fmt.Sprintf("built %s successfully\n冒烟通过(%dms) → 已就位 forge.exe\nsha256=%s\n备份=%s",
		targetExe, outcome.SmokeMS, outcome.SHA256, outcome.BackupPath)
	if stdout.Len() > 0 {
		report += "\n" + stdout.String()
	}
	return ForgeGateResult{
		OK: true, Lang: "self", Stage: "done",
		Stdout:   report,
		Duration: time.Since(start).Milliseconds(),
	}
}

// ────────────────────────────────────────────────────────────────
// self gate 部署阶段: 编译成功之后的「最后一公里」
//
// 背景(20260913): 此前 self gate 只做到 go build -o forge_new.exe,
// 编译成功即返回 OK —— 「编译过了」被当成「能用了」。实际替换/验证/
// 留痕全靠人手写临时脚本, events.jsonl 里 5 天零条部署记录。
// 现补上: 冒烟 → 原子就位 → 失败回滚 → 留痕。
// ────────────────────────────────────────────────────────────────

// selfSmokeTimeout 冒烟超时: 只跑 --version(正常 10-50ms), 15s 是宽松上限。
const selfSmokeTimeout = 15 * time.Second

// smokeRunner 冒烟执行器 (可注入, 便于测试不依赖真实 exe)。
type smokeRunner func(exePath string) (exitCode int, stdout string, runErr error)

// deployOutcome 部署结果, 供报告与测试断言。
type deployOutcome struct {
	Deployed   bool
	BackupPath string
	SHA256     string
	SmokeMS    int64
	FailedPath string // 冒烟失败时产物被改名到的路径
}

// smokeVerdict 冒烟判定 (纯函数):
// 判据 = 正常退出(exitCode==0 且 runErr==nil) 且 输出含 "铸剑炉"。
// 只看退出码不够: 一个「启动即报错但 exit 0」的坏二进制会被放过,
// 所以额外要求版本标识串出现。
func smokeVerdict(exitCode int, stdout string, runErr error) (bool, string) {
	if runErr != nil {
		return false, fmt.Sprintf("无法启动: %v", runErr)
	}
	if exitCode != 0 {
		return false, fmt.Sprintf("退出码 %d != 0", exitCode)
	}
	if !strings.Contains(stdout, "铸剑炉") {
		return false, fmt.Sprintf("输出无版本标识(前 60 字: %s)", headRunes(stdout, 60))
	}
	return true, "ok"
}

// headRunes 截取前 n 个 rune (避免按字节切断中文)。
func headRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}

// realSmokeRunner 真实冒烟: 跑 <exe> --version, 合并捕获 stdout/stderr。
func realSmokeRunner(exePath string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), selfSmokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exePath, "--version")
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := runWithTimeout(ctx, cmd)
	if err == nil {
		return 0, buf.String(), nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), buf.String(), nil
	}
	return -1, buf.String(), err
}

// deploySelfExe 编译成功后的就位: 冒烟 → 备份 → 原子就位 → 失败回滚。
//
// 三个设计要点:
//  1. 冒烟跑「中性名副本」而非产物本身 —— 直接运行 forge_new.exe 会触发
//     runSelfReplace 把产物 rename 走, 反而绕过验证(旧设计无法自证能跑)。
//  2. 备份用 rename(原子, 不复制 11MB), 就位失败立刻 rename 回来 ——
//     任何时刻 forge.exe 要么是旧的要么是新的, 不存在缺失窗口。
//  3. 冒烟失败 → 产物改名 .failed_<ts>, 杜绝陈旧产物之后被误用;
//     源码不回滚(编译已通过, 问题在运行期, 留给下一轮修)。
func deploySelfExe(workDir, builtExe string, keepBackups int, smoke smokeRunner) (deployOutcome, error) {
	var out deployOutcome
	st, statErr := os.Stat(builtExe)
	if statErr != nil {
		return out, fmt.Errorf("产物不存在: %v", statErr)
	}
	if st.IsDir() {
		return out, fmt.Errorf("产物是目录: %s", builtExe)
	}
	if h, herr := sha256File(builtExe); herr == nil {
		out.SHA256 = h
	}
	ts := time.Now().Format("20060102_150405")

	smokePath := filepath.Join(workDir, "forge_smoke_"+ts+".exe")
	if cerr := copyFile(builtExe, smokePath); cerr != nil {
		return out, fmt.Errorf("冒烟副本复制失败: %v", cerr)
	}
	defer os.Remove(smokePath)

	t0 := time.Now()
	code, stdout, runErr := smoke(smokePath)
	out.SmokeMS = time.Since(t0).Milliseconds()
	if ok, why := smokeVerdict(code, stdout, runErr); !ok {
		failed := builtExe + ".failed_" + ts
		if rerr := os.Rename(builtExe, failed); rerr == nil {
			out.FailedPath = failed
		}
		return out, fmt.Errorf("冒烟未通过(%s), 未部署, 旧 exe 保持原样; 产物: %s", why, out.FailedPath)
	}

	cur := filepath.Join(workDir, "forge.exe")
	if filepath.Clean(cur) == filepath.Clean(builtExe) {
		out.Deployed = true
		return out, nil
	}
	if _, serr := os.Stat(cur); serr == nil {
		backup := cur + ".bak_" + ts
		if rerr := os.Rename(cur, backup); rerr != nil {
			return out, fmt.Errorf("备份 forge.exe 失败, 拒绝替换(旧 exe 保持原样): %v", rerr)
		}
		out.BackupPath = backup
	}
	if rerr := os.Rename(builtExe, cur); rerr != nil {
		rb := "无备份可回滚"
		if out.BackupPath != "" {
			if rbErr := os.Rename(out.BackupPath, cur); rbErr == nil {
				rb = "已回滚旧 exe"
				out.BackupPath = ""
			} else {
				rb = fmt.Sprintf("回滚失败(%v), 备份在 %s", rbErr, out.BackupPath)
			}
		}
		return out, fmt.Errorf("就位失败: %v; %s", rerr, rb)
	}
	if _, serr := os.Stat(cur); serr != nil {
		if out.BackupPath != "" {
			os.Rename(out.BackupPath, cur)
			out.BackupPath = ""
		}
		return out, fmt.Errorf("就位后校验失败: %v", serr)
	}
	out.Deployed = true
	if keepBackups > 0 {
		pruneExeBackups(workDir, keepBackups)
	}
	return out, nil
}

func pruneSelfBackups(srcPath string, keep int) {
	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	pattern := base + ".bak_self_*"
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return
	}
	if len(matches) <= keep {
		return
	}
	// Sort by name (timestamp suffix sorts lexicographically) and remove oldest.
	// matches[0] is the oldest because timestamps are zero-padded (20060102_150405.000000000).
	sort.Strings(matches)
	for _, m := range matches[:len(matches)-keep] {
		os.Remove(m)
	}
}

func compilerTimeout(lang string) time.Duration {
	if compiler, ok := 铸剑炉_COMPILERS[lang]; ok {
		if compiler.ExecTimeout > 0 {
			return compiler.ExecTimeout
		}
	}
	return defaultGateTimeout
}

func truncateOutput(s string) string {
	runes := []rune(s)
	if len(runes) <= MaxForgeOutputLength {
		return s
	}
	if len(runes) <= ForgeHeadKeep+ForgeTailKeep {
		return string(runes[:ForgeHeadKeep]) + "\n...[truncated]...\n" + string(runes[len(runes)-ForgeTailKeep:])
	}
	head := string(runes[:ForgeHeadKeep])
	tail := string(runes[len(runes)-ForgeTailKeep:])
	return head + "\n...[truncated]...\n" + tail
}

// forgeCleanupStaleTempDirs removes leftover forge_gate_* and forge_go_* temp directories
// from the system temp directory. These accumulate when forge processes are killed abruptly.
func forgeCleanupStaleTempDirs() {
	tmpDir := os.TempDir()
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return
	}
	now := time.Now()
	minAge := 5 * time.Minute
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "forge_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Skip recently created directories to avoid cleaning up active forge instances
		if now.Sub(info.ModTime()) < minAge {
			continue
		}
		path := filepath.Join(tmpDir, name)
		os.RemoveAll(path)
	}
}

// pythonSignatureRE 是 Python 代码的结构性特征, 用于在「弱信号语言判定」前做排除。
// 判据单一源: Go 误判与 Shell 误判两处修复共用同一正则, 避免两套规则各自腐化。
// 为什么这些形态对 Go/Shell 安全 (逐条核对过):
//   - Go 的 import 是 `import (` 或 `import "path"`, 不匹配行首 `import <标识符>`
//   - Go 的输出是 fmt.Println(, 不含 print(
//   - Shell 里 `import x` / `from x import y` 都不是合法命令
var pythonSignatureRE = regexp.MustCompile(`(?m)^\s*(?:def|class)\s+\w+|(?m)^\s*(?:import|from)\s+\w+|print\s*\(|__name__`)

// goTopLevelRE 匹配行首的 Go 顶层声明 (含方法接收者 `func (r T) Name()`)。
// 用行首锚定取代旧版的全文本 `\s+func`: 闭包 `x := func()` 不在行首,
// 本就不构成「这是 Go 代码」的证据。
var goTopLevelRE = regexp.MustCompile(`(?m)^func\s`)

// hasGoPackageDecl 判断首个有效行 (跳过空行与注释行) 是否为 package 声明。
// 强信号: package 子句必须位于文件最前, 是 Go 语法的硬约束。
func hasGoPackageDecl(code string) bool {
	for _, ln := range strings.Split(code, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		return strings.HasPrefix(t, "package ")
	}
	return false
}

func forgeDetectLang(code, hint string) string {
	code = strings.TrimSpace(code)

	// Go 判定分两级信号:
	//   强信号 = 首个有效行是 package 声明 (Go 语法硬约束, 别的语言无法满足)
	//   弱信号 = 行首 func 声明, 但必须先排除 Python 特征
	// 旧实现用裸 `(?:^|\s)func\s+\w+\s*\(` 全文本匹配 → Python 代码任何位置出现
	// "func xxx(" (注释/字符串/文档) 即被判 go, 且该分支优先于下方全部 Python 判定。
	// 实测 gate_audit 68 条 "main.go:1:1: expected 'package'" 全是此类误判, 且
	// 100% 来自 lang 省略(自动检测): 模型没写 lang, 检测器把 Python 判成 Go。
	if hasGoPackageDecl(code) {
		return "go"
	}
	if !pythonSignatureRE.MatchString(code) && goTopLevelRE.MatchString(code) {
		return "go"
	}

	// Shell/Bash detection — check shebang and shell keywords
	if strings.HasPrefix(code, "#!") {
		if strings.Contains(code, "bash") || strings.Contains(code, "sh") {
			return "sh"
		}
		if strings.Contains(code, "python") {
			return "python"
		}
		if strings.Contains(code, "node") {
			return "node"
		}
	}
	// Shell-like patterns: standalone commands at line starts. A follow-up
	// check rejects Python assignments that merely use a shell keyword as a
	// variable name (e.g. `echo = 5`, `cat = "x"`, `cd = "/tmp"`).
	// NOTE: Go regexp (RE2) does not support lookahead, so the `not followed
	// by =` check is done with FindStringIndex + TrimLeft instead of (?!\s*=).
	// 判定前先排除 Python 特征: 行首 `import os` / `from x import y` 在 sh 里不是
	// 合法命令, 实测 "'import' is not recognized" 23 条即 Python 代码被判 sh。
	shellCmdRE := regexp.MustCompile(`(?m)^\s*(?:echo|export|source|unset|alias|chmod|chown|mkdir|rm|cp|mv|ls|cat|grep|awk|sed|cd|pwd|exit)\b`)
	if !pythonSignatureRE.MatchString(code) {
		if loc := shellCmdRE.FindStringIndex(code); loc != nil {
			after := strings.TrimLeft(code[loc[1]:], " \t")
			// 排除两种「词形相同但语义不同」的形态:
			//   `cat = "x"` → 变量赋值 (= 开头)
			//   `exit(0)`   → 函数调用 (( 开头); shell 命令后不会紧跟括号
			// 实测: `import sys\nexit(0)` 被判 sh, sh gate 失败 3 次后由 fallback
			// 转 python 才成功 —— 用户看到「成功」, 代价是 3.1s 白烧 (audit 里
			// lang=sh/ok=true/dur=3111ms)。这类「成功但绕路」的缺陷只有审计能看见。
			if !strings.HasPrefix(after, "=") && !strings.HasPrefix(after, "(") {
				return "sh"
			}
		}
	}
	if strings.Contains(code, "import ") && (strings.Contains(code, "def ") || strings.Contains(code, "print(")) {
		return "python"
	}
	if strings.Contains(code, "console.log") || strings.Contains(code, "const ") {
		return "node"
	}

	// Python is the most common fallback
	if strings.Contains(code, "def ") || strings.Contains(code, "print(") ||
		strings.Contains(code, "import ") || strings.Contains(code, "class ") {
		return "python"
	}

	// 无编译器特征 → 语义 gate 路由 (math/logic)
	// 编译器优先已在前序完成; 此处只认"纯表达式", 防误伤 python 代码。
	if gate := detectSemanticGate(code); gate != "" {
		return gate
	}
	if hint != "" {
		return hint
	}
	return "python"
}

// detectSemanticGate: 纯数学/逻辑表达式 → 语义 gate。
// 设计: 只在前序编译器检测全部未命中时被调用; 含任何代码关键字立即放弃 (保持 python 默认)。
// 目标: 激活 gate_audit 中长期零使用的 math/logic —— 模型省略 lang 时自动路由。
func detectSemanticGate(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	// 明显代码/非表达式特征 → 不路由 (落 python 默认)
	codeKw := []string{"import ", "def ", "print(", "class ", "return ", "for ", "while ",
		"if ", "function", "var ", "let ", "const ", "http", "#", "```", ";", "{", "}", "\n"}
	for _, kw := range codeKw {
		if strings.Contains(code, kw) {
			return ""
		}
	}
	// 逻辑表达式: 逻辑连接词 (z3/命题逻辑常见记号)
	logicOps := []string{"=>", "<=>", "∀", "∃", "iff", "&", "|"}
	for _, op := range logicOps {
		if strings.Contains(code, op) {
			return "logic"
		}
	}
	// 数学表达式: 含数字 + 运算符; "=" 仅当等式(同时含运算)才路由, 纯赋值不路由
	mathRe := regexp.MustCompile(`^[\d\s\+\-\*\/\^\(\)\.\=\w]{1,200}$`)
	if mathRe.MatchString(code) {
		hasDigit := regexp.MustCompile(`\d`).MatchString(code)
		hasOp := strings.ContainsAny(code, "+-*/^")
		if hasDigit && hasOp {
			return "math"
		}
	}
	return ""
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// Ping returns "pong" with nanosecond timestamp for health checks.
func (f *Forge) Ping() string {
	return fmt.Sprintf("pong @ %d", time.Now().UnixNano())
}

func (f *Forge) Stats() map[string]interface{} {
	return map[string]interface{}{
		"builds":     f.statBuilds.Load(),
		"cache_hits": f.statCacheHits.Load(),
		"errors":     f.statErrors.Load(),
		"cache_size": len(f.cacheKeys),
		"in_flight":  len(f.sem),
	}
}

// looksLikeValidGoTopLevel checks code is plausibly valid at Go package level.
func looksLikeValidGoTopLevel(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	validStarts := []string{"func ", "type ", "var ", "const ", "import ", "package "}
	for _, prefix := range validStarts {
		if strings.HasPrefix(code, prefix) {
			return true
		}
	}
	if strings.HasPrefix(code, "//") || strings.HasPrefix(code, "/*") {
		return true
	}
	return false
}

func ForgeToolSchema() json.RawMessage {
	schema := map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        ForgeToolName,
			"description": ForgeToolDescription,
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"description": "运行 (写代码、编译执行、销毁 —— 你唯一需要的操作)",
					},
					"code": map[string]interface{}{
						"type":        "string",
						"description": "要执行的源代码。复杂逻辑推荐Python。Windows文件I/O用encoding='utf-8',errors='replace'。读取: open(path).read()。列目录: os.listdir()或os.walk()。搜索: re.findall()。运行命令: subprocess.run()。编辑: 读取、str.replace、写回。",
					},
					"input": map[string]interface{}{
						"type":        "string",
						"description": "可选的JSON字符串，作为argv[1]传给脚本。",
					},
					"lang": map[string]interface{}{
						"type":        "string",
						"description": "语言(可省略, 自动检测, 默认python, 90%场景无需指定)。python=默认代码执行; sh=shell命令(bash→sh); go=Go代码; node=JavaScript; math=数值/符号计算; logic=逻辑证明(含量词必走它); regex=正则验证; chain=多步编排; knowledge=知识查询(SPARQL); tcm=中医药药对(触发词: 查药对X Y/药对：A、B/配伍); browser=网页浏览(直连不走代理); self=源码自修改(需主人审批)。拿不准就省略lang, 自动检测默认python。",
						"enum":        []string{"go", "sh", "math", "logic", "knowledge", "regex", "chain", "python", "node", "self", "tcm", "browser"},
					},
				},
				"required": []string{"action", "code"},
			},
		},
	}
	b, _ := json.Marshal(schema)
	return b
}
