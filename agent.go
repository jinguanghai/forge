package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// ─── AgentRunner ────────────────────────────────────────────

type AgentRunner struct {
	cfg   *Config
	llm   *LLMClient
	forge *Forge

	history []ChatMessage
	stats   *SessionStats

	ctx             context.Context
	cancel          context.CancelFunc
	mu              sync.Mutex
	runCancelMu     sync.Mutex
	runCancel       context.CancelFunc
	lastOutMu       sync.Mutex
	lastOut         string
	compactCooldown int // 历史压缩冷却计数 (六西格玛立项20260815)

	// v3.1 (DSH project 机制): 记忆/待办/档案索引 diff 追加基线 (前缀缓存恒定)
	lastMemText    string // 上次注入的记忆锚点文本 (diff 基线)
	lastActiveTask string // 上次注入的 active_task
	lastFoldedText string // 上次注入的折叠索引
	headLen        int    // 固定头部消息数 (system+记忆+折叠)

	// v3.2 (DSH RuntimeContextProjection): 固定头首值缓存 —— 只构建一次并复用,
	// 此后 memory/折叠索引变化一律走 syncDynamicTails 尾部 diff, 决不重写固定头,
	// 使 system+固定头 前缀跨会话/跨对话逐字节恒定 → DeepSeek 前缀缓存命中率最大化。
	headCached        bool
	initialMemText    string
	initialFoldedText string

	SaveCheckpoint bool // 是否保存检查点 (单次查询=false, 避免污染主会话)

	// streamedCode: 本轮工具代码已由 toolCodeStreamer 流式打印过的全文。
	// 与 displayToolCode 的去重判据 —— 流式已完整显示过同一段代码时不再整块重打。
	streamedCode string
}

func NewAgentRunner(cfg *Config) (*AgentRunner, error) {
	ctx, cancel := context.WithCancel(context.Background())

	f := NewForge(cfg.WorkDir, cfg)
	l := NewLLMClient(cfg)

	a := &AgentRunner{
		cfg:            cfg,
		llm:            l,
		forge:          f,
		stats:          &SessionStats{StartTime: time.Now()},
		ctx:            ctx,
		cancel:         cancel,
		SaveCheckpoint: true,
	}
	// v3.1: 固定头部 = [system 恒定版] + [记忆锚点] + [折叠索引] (DSH project 机制)
	// 必须写入 a.history! 否则首轮请求无 system (前缀缺失, 全量 miss)。
	// 实测: NewAgentRunner 只算 headLen 不赋值 → 单次查询/新会话请求 messages=[user] 无 system,
	// 缓存前缀每次不同 → 每轮全 miss (40-60k/轮)。checkpoint 也无 system → 恢复依赖重建。
	head := a.buildFixedHead()
	a.headLen = len(head)
	a.history = append([]ChatMessage{}, head...)
	// v3.2: 基线 = 固定头首值缓存 (buildFixedHead 已构建) → syncDynamicTails 据此 diff 追加变化。
	a.lastMemText = a.initialMemText
	a.lastActiveTask = currentActiveTask(cfg.WorkDir)
	a.lastFoldedText = a.initialFoldedText
	return a, nil
}

func (a *AgentRunner) Shutdown() {
	// 正常收尾留痕: 非正常退出检测以此为准 (见 exit_watch.go)。
	// 这里是三条正常退出路径的唯一汇聚点: main 的 defer / 信号处理 / 窗口关闭回调。
	// 被强杀时本行不会执行 —— 那正是下次启动要报出来的事实。
	markCleanExit()
	a.cancel()
	a.forge.Shutdown()
	a.llm.Shutdown()
}

func (a *AgentRunner) Context() context.Context {
	return a.ctx
}

// CancelCurrent cancels only the in-flight RunStream (per-run context),
// leaving the agent usable for subsequent runs.
func (a *AgentRunner) CancelCurrent() {
	a.runCancelMu.Lock()
	defer a.runCancelMu.Unlock()
	if a.runCancel != nil {
		a.runCancel()
	}
}

// LastOutput returns the most recent tool output (for the /last command).
func (a *AgentRunner) LastOutput() (string, bool) {
	a.lastOutMu.Lock()
	defer a.lastOutMu.Unlock()
	return a.lastOut, a.lastOut != ""
}

// ─── RunStream: main entry ─────────────────────────────────

func (a *AgentRunner) RunStream(input string) (err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Panic recovery: prevent a single panic from taking down the agent
	defer func() {
		if err == nil && a.cfg != nil && a.SaveCheckpoint {
			if ckErr := a.saveCheckpoint(a.cfg.WorkDir); ckErr != nil {
				fmt.Fprintf(os.Stderr, "%s 检查点保存失败: %v\n", color(ansi.yellow, "⚡"), ckErr)
			}
		}
	}()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("agent panic recovered: %v", r)
			fmt.Fprintf(os.Stderr, "%s PANIC RECOVERED: %v\n", color(ansi.red, "💥"), r)
		}
	}()

	// Per-run context: Ctrl+C cancels only the current task, not the agent.
	runCtx, runCancel := context.WithCancel(a.ctx)
	a.runCancelMu.Lock()
	a.runCancel = runCancel
	a.runCancelMu.Unlock()
	defer func() {
		a.runCancelMu.Lock()
		a.runCancel = nil
		a.runCancelMu.Unlock()
		runCancel()
	}()

	// B3 批5: 一轮任务的装配 / 状态 / 主循环整体迁出 (agent_stream_setup.go)。
	// 本函数只保留必须挂在自身栈上的骨架 —— 四个 defer(解锁 / 检查点保存 /
	// panic 恢复 / runCancel 清理)依赖本函数的生命周期, 不能在子函数里注册
	// (子函数一返回即触发), 故不外迁。
	rs, err := a.newRunState(input, runCtx)
	if err != nil {
		return err
	}
	if turnErr := rs.runTurns(); turnErr != nil {
		return turnErr
	}
	// runTurns 返回 nil = 任务正常完成 (收尾已落 history / trim / stats)。
	// 防死循环的四道安全网(循环拦截 / 连续失败 / 未知工具 / 上下文取消)
	// 与主循环语义, 见 runTurns 注释 (agent_stream_setup.go)。
	return nil
}
