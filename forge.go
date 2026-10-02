package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

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

	// DroppedBytes / OutputTruncated 记录捕获层(limitedWriter)丢弃的字节数。
	// 动机: 采集量无上限 —— DSec 论文实测 agent 跑 `yes` 累积几十 GB;
	// 本机实测 64 MiB 输出下无限制 Builder 堆 +64.1 MiB / Buffer +128.0 MiB。
	// 丢弃发生在捕获层, 因此截断后的结果仍携带「曾经溢出」这一事实。
	DroppedBytes    int  `json:"dropped_bytes,omitempty"`
	OutputTruncated bool `json:"output_truncated,omitempty"`

	// GateRejected 标记「死程序主动拒绝该输入」, 与「执行失败」是两回事:
	// 前者是判定结果(输入不合法, 重跑无意义), 后者是环境/代码问题。
	// 动机(20260927 实测): math gate 对 "hello world" 返回 ok:true +
	// "d*e*h*l**3*o**2*r*w"(隐式乘法把自然语言切碎), 是 fail-open 且审计不可见。
	// gate 侧补齐拒绝权后, 这里必须把它提取成类型化字段, 否则审计仍数不出来。
	GateRejected bool   `json:"gate_rejected,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`

	// EnvFailure 标记「环境性失败」(工具链缺失/gate 二进制缺失/语言不支持) ——
	// 与「代码自身失败」对立。唯一用途: 决定是否换语言重跑(shouldFallback)
	// 与是否写缓存(isTransientError)。
	// 动机(20260927): 旧实现靠错误文本嗅探判环境性, 实测三重失效 ——
	//  ①反向误判: 用户代码报错含 "not found" 即被判环境失败, 触发无意义换语言;
	//  ②正向漏判: 真实产生点文案是中文「不支持的语言: xx」, 与英文匹配永不命中,
	//    该分支从未生效过;
	//  ③双口径: 同文件 isTransientError 已改类型化, shouldFallback 漏改。
	// 判据必须由产生点打标, 而非消费点猜文本。
	EnvFailure bool `json:"env_failure,omitempty"`
}

type Forge struct {
	workDir  string
	toolsDir string
	ctx      context.Context

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
	// cacheDiskWG 追踪在飞的异步落盘 goroutine, Shutdown 必须等其排空。
	// 不等则: 测试侧 t.TempDir 清理与 goroutine 竞态(实测复现率 4/100);
	// 生产侧退出时缓存可能未落盘(下次冷启动全量 miss)或被旧快照覆盖。
	cacheDiskWG sync.WaitGroup
	// cacheDiskClosed 由 Shutdown 置位, 此后不再启动新的异步落盘。
	// 读写均在 cacheMu 保护下, 消除 "查标志→Add" 与 "置标志→Wait" 的 TOCTOU。
	cacheDiskClosed bool
	retryMax        int
	retryBackoff    time.Duration
	cfg             *Config // needed by self-hosted gates

	// Runtime statistics (visible via Stats())
	statBuilds    atomic.Int64
	statCacheHits atomic.Int64
	statErrors    atomic.Int64
}

const (
	ForgeToolName        = "forge"
	ForgeToolDescription = "铸剑炉: 写代码，自动编译执行，用完销毁。唯一的工具。"
	ForgeToolsDir        = ".forge/forge-tools"
	ForgeTempDir         = ".forge-temp"
	MaxForgeOutputLength = 8000
	ForgeHeadKeep        = 6000
	ForgeTailKeep        = 2000
)

const ForgeInputEnv = "铸剑炉_INPUT"

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
)

// selfAppendMarker 是 append 动作的插入锚点: 新代码插在该声明之前。
// 提为常量是因为它同时被 selfApplyAppend(找插入点)与 locateSelfSource(定位待改
// 文件)使用 —— 两处必须用同一个键, 否则"定位到哪"与"改到哪"会脱节
// (定位 A 文件却改写 B 文件, 且备份也是 B 的)。
const selfAppendMarker = "func ForgeToolSchema() json.RawMessage {"

//
// ⚠️ 本常量必须与 ForgeToolSchema 声明【同文件】: locateSelfSource 用该字符串
// 在全包搜文件, 若常量住在别的文件, 就会有两个文件含该子串 -> 定位歧义 ->
// self gate 的 append 动作被拒绝执行(实测踩过)。

var (
	ErrTooBusy      = errors.New("forge满载")
	ErrShuttingDown = errors.New("forge正在关闭")

	// ErrCancelled: 用户按 Ctrl+C 取消了本次执行 (与程序关停 ErrShuttingDown 区分)。
	ErrCancelled = errors.New("本次执行已被用户取消")
)

func NewForge(workDir string, cfg *Config) *Forge {

	// Clean up stale temp directories from previous runs
	forgeCleanupStaleTempDirs()
	ctx, cancel := context.WithCancel(context.Background())
	f := &Forge{
		workDir:          workDir,
		toolsDir:         filepath.Join(workDir, ForgeToolsDir),
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
	// 审批取证目录预创建: 卫生判据把「目录不存在」判为 fail-closed(与「存在但为空」语义不同),
	// 而该目录只在审批时才写入 —— 不预创建则「从未审批过」期间判据常红。
	// 空态由 hygiene_forge_manifest.json 的 allow_empty_self + allow_empty_dirs 豁免。
	os.MkdirAll(approvalEvidenceDir(workDir), 0755)
	f.loadCacheFromDisk()
	return f
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
						"description": "语言(可省略, 自动检测, 默认python, 90%场景无需指定)。python=默认代码执行; go=Go代码; node=JavaScript; math=数值/符号计算; logic=逻辑证明(含量词必走它); regex=正则验证; chain=多步编排; knowledge=知识查询(SPARQL); tcm=中医药药对(触发词: 查药对X Y/药对：A、B/配伍); browser=网页浏览(直连不走代理); self=源码自修改(需主人审批)。拿不准就省略lang, 自动检测默认python。注意: sh gate 已退役(实测失败率48.2%), shell 命令请用 python 的 subprocess 承接, 不要指定 lang=sh。",
						"enum":        铸剑炉_GATES,
					},
				},
				"required": []string{"action", "code"},
			},
		},
	}
	b, _ := json.Marshal(schema)
	return b
}

func (f *Forge) Shutdown() {
	// ① 停止启动新的异步落盘。与 cacheResult 在 cacheMu 上互斥, 消除
	//    "检查标志 → Add" 与 "置标志 → Wait" 之间的 TOCTOU。
	f.cacheMu.Lock()
	f.cacheDiskClosed = true
	f.cacheMu.Unlock()
	// ② 等在飞的异步落盘排空。不等待的后果实测:
	//    测试侧 t.TempDir 清理报 "The directory is not empty"(复现率 4/100);
	//    生产侧退出时缓存可能未落盘, 或被更早的快照覆盖。
	f.cacheDiskWG.Wait()
	// ③ 再写一次最终快照: 必晚于所有异步写, 保证落盘的是最新状态。
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
	// approvalWait 单独计量人工审批等待：它发生在 buildStart 之后，会被 duration_ms
	// 一并计入(实测 410 条超长记录里 100 条是等主人按 y，最长 6.4 小时)。
	// duration_ms 语义保持不变(仍是用户感知总时长)，审批等待另记 approval_wait_ms，
	// 统计侧用 duration_ms - approval_wait_ms 还原净执行耗时。
	var approvalWait time.Duration
	if danger || lang == "self" {
		if kind == "" {
			kind = "自改"
			hit = "self gate"
		}
		approveStart := time.Now()
		approved := f.confirmDangerous(code, kind, hit)
		approvalWait = time.Since(approveStart)
		if !approved {
			logGuardEvent(f.workDir, "medium", kind, hit, "deny", "代码层审批拒绝", code)
			return fmt.Sprintf("--- ⛔ 操作被主人拒绝 ---\n危险操作 [%s] 命中「%s」\n主人未批准, 代码未执行。请调整方案(改用更安全的方式, 或先向主人说明用途取得批准)。\n--- 结束 ---", kind, hit), nil, nil
		}
		logGuardEvent(f.workDir, "medium", kind, hit, "allow", "代码层审批通过", code)
	}

	var result ForgeGateResult
	netHint := netEgressHint(code)
	defer func() {
		f.auditGate(lang, langOmitted, fallbackUsed, buildStart, approvalWait, len(code), len(input), netHint, &result)
	}()

	// ─── sh gate 退役 (20261001, 六西格玛 DMAIC 改善项) ───
	// 实测: sh 是全炉唯一异常源 —— 失败率 48.2% (DPMO 482301, σ=1.54, 全炉最低档),
	// 占全炉 fallback 100% (13/13), 累计白耗 1809s; 自动检测判入的失败率高达 82.1%。
	// 根因(反境): forge_lang.go 的 shellCmdRE 把"疑似 shell 形态"一律路由到 sh, 而 sh
	// 在 Windows 上执行的是 cmd.exe —— 名实不符, 52.3% 缺陷是 Unix 命令打给 cmd。
	// 处置(合境): 不执行, 当场拒绝并附 python 替代示例 (拒绝 > 猜测: 拒绝可判定且当场
	// 截断, 猜测要烧一整轮重试)。检测层仍返回 "sh" 作哨兵值, 便于审计区分请求来源。
	if lang == "sh" || lang == "bash" {
		result = ForgeGateResult{OK: false, Lang: "sh", Stage: "retired", Error: shGateRetiredText}
		return fmt.Sprintf("--- ⛔ sh gate 已退役 [retired] ---\n%s\n--- 结束 ---", shGateRetiredText), &result, nil
	}

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
//
// 委托模式 (FORGE_DELEGATE, 见 approval_delegate.go): 关闭时本函数行为与引入前
// 逐字节一致; 开启时不阻塞等键盘 —— 由 confirmByDelegate 定夺, 自动放行的事件标
// mode="auto", 与人工批准可区分。
func (f *Forge) confirmDangerous(code, kind, hit string) bool {
	fmt.Fprintf(os.Stderr, "\n%s 危险操作检测 [%s] 命中「%s」\n", color(ansi.yellow, "🛡"), kind, hit)
	fmt.Fprintf(os.Stderr, "  代码: %s\n", dim(summarizeCode(code)))
	if extra := approvalExtraText(code, hit); extra != "" {
		fmt.Fprint(os.Stderr, color(ansi.yellow, extra))
	}
	// 取证: 先落盘再提问 —— B 路(拦住靠不住, 审得清兜底)的第一块砖。
	notice, fp, codePath := approvalEvidenceNotice(f.workDir, code)
	fmt.Fprint(os.Stderr, color(ansi.yellow, notice))

	// ─── 不可逆自毁类: 直接拒绝, 不进入任何批准通道 (力度 B, 20261002 事故修复) ───
	// 事故前这条路径会落到委托直通档被自动放行, 炉体遂自杀成功。现连"人工按 y"的
	// 入口都不给: 外部强杀跳过全部收尾(审计/记忆/自替换备份)且无正当用途, 而炉子
	// 已有受控重启通道 (self gate / runSelfReplace)。被拒的动作同样先落取证留痕。
	if isIrreversibleKind(kind) {
		if codePath != "" {
			if recErr, pruned := recordApprovalEvidence(f.workDir, kind, hit, "deny", modeRejected, code, codePath); recErr != nil {
				fmt.Fprintf(os.Stderr, "%s 审批索引写入失败: %v\n", color(ansi.yellow, "⚠"), recErr)
			} else if pruned > 0 {
				fmt.Fprintf(os.Stderr, "  🗑 取证轮转: 移除最旧 %d 份 (保留 %d)\n", pruned, approvalKeepMax)
			}
		}
		fmt.Fprint(os.Stderr, color(ansi.yellow, irreversibleRefusalText(kind, hit)))
		logGuardEvent(f.workDir, "medium", kind, hit, "deny", "不可逆自毁类直接拒绝", code)
		logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "verdict": "deny", "sha256": fp, "mode": modeRejected})
		return false
	}

	// 委托模式: 自动放行时 decided=true, 跳过键盘等待 (不阻塞是这套机制的全部意义)。
	// kind 必须传进去: 放行决策看类别, 不看档位 (20261002 事故根因)。
	mode := approvalDelegateMode()
	allowed, decided := confirmByDelegate(mode, kind, fp)
	if !decided {
		// 自愈: 即便 readLine 因异常路径留下 raw 残留, 也确保 (y/N) 处能敲键能回显。
		// 改控制台模式这种事本不该发生, 但发生过 (20261002 实测 03 次卡死),
		// 而主人没有任何手段自修, 只能重启 forge — 此处兜底即根治。
		ensureCookedForApproval()
		fmt.Fprintf(os.Stderr, "%s 批准执行? (y/N): ", color(ansi.yellow, "⚠"))
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			line = ""
		}
		allowed = parseApproval(line)
	}
	verdict := "deny"
	recMode := modeHuman
	if allowed {
		verdict = "allow"
	}
	if decided {
		recMode = modeAuto
	}
	// 全文落盘成功才写索引 —— 索引里 file="" 的行就是悬空引用。
	if codePath != "" {
		if recErr, pruned := recordApprovalEvidence(f.workDir, kind, hit, verdict, recMode, code, codePath); recErr != nil {
			fmt.Fprintf(os.Stderr, "%s 审批索引写入失败: %v\n", color(ansi.yellow, "⚠"), recErr)
		} else if pruned > 0 {
			fmt.Fprintf(os.Stderr, "  🗑 取证轮转: 移除最旧 %d 份 (保留 %d)\n", pruned, approvalKeepMax)
		}
	}
	if allowed {
		data := map[string]string{"hit": hit, "verdict": verdict, "sha256": fp}
		if decided {
			// 自动放行必须可区分: 事后审计要能分清「主人按了 y」与「委托代批」。
			data["mode"] = "auto"
		}
		logEvent(EvApproved, kind, data)
	} else {
		logEvent(EvGuardBlocked, kind, map[string]string{"hit": hit, "verdict": verdict, "sha256": fp})
	}
	return allowed
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
