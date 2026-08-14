package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ─── Constants ──────────────────────────────────────────────

// ─── System prompt ──────────────────────────────────────────

const systemPrompt = `你是 铸剑炉，通用数字智能体。你拥有一个多语言编译器沙箱（铸剑炉），可以写代码、编译执行、销毁。通过它你能完成数字世界的一切任务——编码、系统管理、文件处理、数据分析、网络操作、自动化等。

<critical_rules>
1. 你只有 forge（铸剑炉）一个工具，调用时工具名用 "forge"。不要尝试调用 bash、edit、view、grep、ls、glob、write 或任何训练数据中的其他工具——它们不存在，调用必定失败。

2. 用中文思考和输出。所有分析、解释、判断一律用中文表达。你的推理过程（reasoning）必须用中文，最终输出也必须用中文。这是硬性要求——中文是你的唯一语言。

3. 直接行动，不解释不询问。代码是唯一的行动方式。长任务拆成多个30秒内可完成的子步骤执行，避免超时。遇到未知信息用代码探查，不猜测。

4. eprover语言接收TPTP格式的一阶逻辑问题。fof(名称, conjecture, 公式). fof(名称, axiom, 公式). 量词: ![X]全称, ?[X]存在. 连接词: =>, <=>, &, |, ~.

5. chain(lang="chain")编排多gate顺序执行: {"stages":[{"gate":"eprover|logic|math|...","input":{...},"if_verdict":"theorem|unsat|counter_sat(可选,仅当前一阶段verdict匹配时执行)"}],"stop_on":"error|first_success|never"}。用于conditionally chain多个推理步骤减少LLM往返。

6. 涉及数学计算、数值验证、等式推导、公式化简的问题，禁止直接输出结果。必须通过 forge 写代码计算验证后才能输出。用 lang="math" 或 lang="python" 执行实际计算，代码输出作为答案依据。

7. 🔥 生成与执行分离——这是硬规则，不是建议。当你需要调用 forge 工具时，禁止在同一个响应中附带任何文字解释、分析、序言或结语。工具调用响应必须只有工具调用，零文字。分析、总结、解释一律放在工具结果返回后的下一个响应中。违反此规则会直接导致系统运行异常——附带的文字会被丢弃。
8. 🔄 专家路由——按任务类型强制召唤专家，禁止"凭记忆口算/编造"：
   - 算数/统计/价格/比例/数值比较 → 必须 forge 实际计算（lang="math" 或 "python"），禁止直接给数字
   - 逻辑判断/条件推理/真伪 → 优先 logic gate 验证
   - 定理/数学证明 → eprover
   - 文件操作(整理/改名/移动/分类/搜索/统计) → 必须 python 写代码实际执行
   - 数据处理/表格/报表/转换 → 必须 python 写代码实际处理
   - 写代码/改代码/修bug → 必须编译执行验证通过才算完成
   - 知识/概念查询 → knowledge gate
   判据：凡有"确定性答案或确定性操作"的任务一律走专家执行；只有纯解释/讨论/答疑类问题才允许直接回答。
9. regex(lang="regex")验证正则表达式: 直接裸写 pattern，或用 JSON {"type":"match","pattern":"...","positive":[...],"negative":[...]}。注意语义是"整串完全匹配"(fullmatch)，不是"包含匹配"——pattern [A-Z]\d{3} 对字符串 "B456" 匹配，但对 "order B456 ok" 不匹配(整串不匹配)。positive 列表的每个字符串必须被 pattern 整串匹配，negative 列表的每个必须整串不匹配。flags 可用 i/m/s。

10. 📋 复杂任务进度管理——多步骤任务(≥3步)必须:
    - 开工前: 输出"📋 执行计划: 1.xxx 2.xxx 3.xxx"（步骤清单）
    - 每完成一步: 输出"✅ 完成步骤N: xxx | ⏳ 剩余: 步骤M,..."（简短）
    - 全部完成: 输出"🎉 全部完成: N步 / 失败0"

11. 🔐 计划审批——高风险/不可逆/大规模任务(修改源码、批量文件操作、部署、删除、推送远端)必须:
    第一步: 输出"📝 计划书"，必须含4栏(五境约束):
      ①目标【正境·可测量】: 指标当前值A→目标值B；不可测量的目标必须改写为可测量形式
      ②步骤【正境→反境→合境】: 先剥表象定边界→再拆因果链找根因→最后组平衡方案
      ③风险与回滚【合境·可回滚】: 方案必须可回滚(备份/撤销路径)；不能回滚的方案自动标记高风险
      ④验证方法【证据先于声称】: 附具体验证步骤与通过标准
    第二步: 等待主人明确批准("批准"/"可以"/"干")后才动手
    未批准前禁止执行任何写操作

12. 🔍 完成验证铁律（"证据先于声称"）——任何"成功/完成/修复/通过/搞定"的声称，都必须附带实际运行的验证证据：
    - 写代码/改代码 → 声称完成前必须有编译或测试的实际输出（错误、警告、通过用例数）
    - 计算/数据/文件操作 → 必须有代码实际运行的输出结果（数值、行数、文件清单）
    - 声称完成的任务，把验证输出贴在回答里；拿不出证据就不算完成
    - 验证失败或输出异常 → 如实报告失败与原因，绝不伪装成功
    - 禁止"应该能过/我很有信心/逻辑上没问题"这类无证据表述当作完成依据

13. 💾 记忆写入纪律——更新 memory.json 必须原子替换: 先写 memory.json.tmp 再 rename 到 memory.json（直接覆盖主文件=写半截崩溃即记忆丢失）。程序化路径优先走 memory_store.go 的 SaveMemory（自动保留上一版为 .bak）；通过 forge 临时脚本写记忆时也必须 tmp+rename。
</critical_rules>

你是纯粹的工具使用者，不是聊天助手。直接干活。


`

// ─── memory.json 注入稳定化 (DeepSeek 前缀缓存守卫, 六西格玛 Improve #2) ──
// DeepSeek 缓存按 token 前缀完全匹配: system 里任何一处不同, 之后全部失效。
// memStableOrder 把"几乎不变"的锚点放在前, "易变"字段 (last_updated/active_task)
// 沉底——即使它们变化, 前面的稳定块仍能在服务端公共前缀检测中命中。
// 重新序列化输出与文件格式无关 (仅由值决定) → 文件被外部改写格式也不断前缀。
// memStableOrder v2.2: 只含锚点字段(恒常驻 system, 守护 DeepSeek 前缀缓存)。
// key_findings 已改为 BM25 动态召回(RecallMemory, 注入用户轮次); folded_memory 改为精简索引。
var memStableOrder = []string{
	"identity", "role", "language", "working_dir", "architecture", "gates",
	"environment", "hardcoded_paths", "defense", "user_principle", "self_governance",
	"axioms", "last_updated", "active_task",
}

// reorderMemoryJSON 按 memStableOrder 重排顶层字段并重新序列化 (紧凑+确定性);
// 未知字段按字典序追加尾部; 解析失败原样返回 (不破坏注入)。
func reorderMemoryJSON(data []byte) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return data
	}
	inOrder := func(k string) bool {
		for _, s := range memStableOrder {
			if s == k {
				return true
			}
		}
		return false
	}
	var extra []string
	for k := range m {
		if !inOrder(k) {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	var sb strings.Builder
	sb.WriteByte('{')
	first := true
	for _, k := range append(append([]string{}, memStableOrder...), extra...) {
		v, ok := m[k]
		if !ok {
			continue
		}
		if !first {
			sb.WriteByte(',')
		}
		first = false
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(v)
		if err != nil {
			continue
		}
		sb.Write(kb)
		sb.WriteByte(':')
		sb.Write(vb)
	}
	sb.WriteByte('}')
	return []byte(sb.String())
}

// currentSystemHash / lastSystemHash: system 前缀指纹 (SHA-256 前16位 hex)。
// 每次请求前比对二者, 不同 → 前缀断裂告警 (六西格玛 Control 守卫)。
var (
	currentSystemHash string
	lastSystemHash    string
)

// buildSystemPrompt v2.2: 只注入锚点字段 + 折叠索引精简版。
// key_findings(6739 token) 从 system 移出 → BM25 动态召回进用户轮次(RecallMemory),
// 使 system 前缀更小更稳; 召回内容每轮不同也不会破坏 DeepSeek 前缀缓存。
func buildSystemPrompt(workDir string) string {
	memoryJSON := ""
	if data, _, err := LoadMemory(workDir); err == nil {
		// 剔除动态区(key_findings / folded_memory) → 只保留锚点
		memoryJSON = string(reorderMemoryJSON(stripDynamicMemory(data)))
	}

	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString(fmt.Sprintf("\n\nWork directory: %s", workDir))
	if memoryJSON != "" {
		sb.WriteString("\n\n<memory_context>\n")
		sb.WriteString(memoryJSON)
		sb.WriteString("\n</memory_context>")
		sb.WriteString("\n以上记忆上下文包含持久化信息。当事实变化时通过铸剑炉更新它。")
	}
	if folded := compactFoldedIndex(workDir); folded != "" {
		sb.WriteString("\n\n<folded_archive_index>\n")
		sb.WriteString(folded)
		sb.WriteString("</folded_archive_index>\n输入「展开<名称>」预览摘要或「深入<名称>」读全文。")
	}
	s := sb.String()
	sum := sha256.Sum256([]byte(s))
	currentSystemHash = hex.EncodeToString(sum[:8])
	return s
}

// stripDynamicMemory 从 memory.json 字节中剔除 key_findings 与 folded_memory
// (二者分别由 RecallMemory 动态召回 / compactFoldedIndex 精简注入)。
func stripDynamicMemory(data []byte) []byte {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return data
	}
	delete(m, "key_findings")
	delete(m, "folded_memory")
	out, err := json.Marshal(m)
	if err != nil {
		return data
	}
	return out
}

// compactFoldedIndex 折叠档案精简索引: 每项一行(名称—摘要+状态), 供 LLM 建议展开。
func compactFoldedIndex(workDir string) string {
	items, err := foldedItems(workDir)
	if err != nil || len(items) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, it := range items {
		mark := "✅"
		if it.Status == "doing" {
			mark = "🔵"
		} else if it.Status == "shelved" {
			mark = "⏸"
		}
		summary := truncateCN(it.Summary, 40)
		sb.WriteString(fmt.Sprintf("  %s %s — %s\n", mark, it.Name, summary))
	}
	return sb.String()
}

// ─── ANSI helpers ───────────────────────────────────────────

var ansi = struct {
	reset, bold, dim, red, green, yellow, blue, magenta, cyan, white string
}{
	reset: "\033[0m", bold: "\033[1m", dim: "\033[2m",
	red: "\033[31m", green: "\033[32m", yellow: "\033[33m",
	blue: "\033[34m", magenta: "\033[35m", cyan: "\033[36m", white: "\033[37m",
}

// ─── 会话检查点 (DMAIC I2, 借鉴 langgraph Checkpoint): ───
// 每轮任务正常结束后把对话历史落盘 .forge/checkpoint.json。
// 崩溃/重启后主人输入"恢复"即可续接上次会话上下文。
func (a *AgentRunner) saveCheckpoint(workDir string) error {
	if workDir == "" {
		return nil
	}
	dir := filepath.Join(workDir, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	cp := struct {
		SavedAt string        `json:"saved_at"`
		History []ChatMessage `json:"history"`
	}{SavedAt: time.Now().Format("2006-01-02 15:04:05"), History: a.history}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "checkpoint.json.tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "checkpoint.json")) // 原子替换防半截
}

// loadCheckpoint 读取上次会话检查点, 返回 (历史, 保存时间, 错误)
func loadCheckpoint(workDir string) ([]ChatMessage, string, error) {
	if workDir == "" {
		return nil, "", fmt.Errorf("no workdir")
	}
	data, err := os.ReadFile(filepath.Join(workDir, ".forge", "checkpoint.json"))
	if err != nil {
		return nil, "", err
	}
	var cp struct {
		SavedAt string        `json:"saved_at"`
		History []ChatMessage `json:"history"`
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, "", err
	}
	return cp.History, cp.SavedAt, nil
}

// RestoreHistory 从检查点恢复历史（带锁, 线程安全）
func (a *AgentRunner) RestoreHistory(history []ChatMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = history
}

func color(c, s string) string { return c + s + ansi.reset }
func bold(s string) string     { return ansi.bold + s + ansi.reset }
func dim(s string) string      { return ansi.dim + s + ansi.reset }

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ─── Spinner ─────────────────────────────────────────────

type spinner struct {
	stopCh chan struct{}
	doneCh chan struct{}
	msg    string
}

// startSpinner renders an animated indicator on stderr until stop() is called.
func startSpinner(msg string) *spinner {
	s := &spinner{stopCh: make(chan struct{}), doneCh: make(chan struct{}), msg: msg}
	go func() {
		idx := 0
		for {
			select {
			case <-s.stopCh:
				fmt.Fprint(os.Stderr, "\r\033[K")
				close(s.doneCh)
				return
			case <-time.After(100 * time.Millisecond):
				fmt.Fprintf(os.Stderr, "\r%s %s", color(ansi.dim, spinnerFrames[idx%len(spinnerFrames)]), s.msg)
				idx++
			}
		}
	}()
	return s
}

func (s *spinner) stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	<-s.doneCh
}

// ─── Session stats ─────────────────────────────────────────

type SessionStats struct {
	mu          sync.RWMutex
	StartTime   time.Time
	Turns       int
	TotalMs     int64
	TotalTokens int64
	TotalToolMs int64
	ToolOK      int
	ToolFail    int
	LastModel   string
}

func (s *SessionStats) String() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	elapsed := time.Since(s.StartTime).Round(time.Second)
	return fmt.Sprintf(
		"turns:%d | tokens:%d | tools:%d✓/%d✗ | total:%v | elapsed:%v | model:%s",
		s.Turns, s.TotalTokens, s.ToolOK, s.ToolFail,
		time.Duration(s.TotalMs)*time.Millisecond, elapsed, s.LastModel,
	)
}

func (s *SessionStats) addTurn(d time.Duration) {
	s.mu.Lock()
	s.Turns++
	s.TotalMs += d.Milliseconds()
	s.mu.Unlock()
}

func (s *SessionStats) setModel(m string) {
	s.mu.Lock()
	s.LastModel = m
	s.mu.Unlock()
}

func (s *SessionStats) addToken(n int64) {
	s.mu.Lock()
	s.TotalTokens += n
	s.mu.Unlock()
}

func (s *SessionStats) addToolOK(d time.Duration) {
	s.mu.Lock()
	s.ToolOK++
	s.TotalToolMs += d.Milliseconds()
	s.mu.Unlock()
}

func (s *SessionStats) addToolFail(d time.Duration) {
	s.mu.Lock()
	s.ToolFail++
	s.TotalToolMs += d.Milliseconds()
	s.mu.Unlock()
}

// ─── AgentRunner ────────────────────────────────────────────

type AgentRunner struct {
	cfg   *Config
	llm   *LLMClient
	forge *Forge

	history []ChatMessage
	stats   *SessionStats

	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	runCancelMu sync.Mutex
	runCancel   context.CancelFunc
	lastOutMu   sync.Mutex
	lastOut     string
}

func NewAgentRunner(cfg *Config) (*AgentRunner, error) {
	ctx, cancel := context.WithCancel(context.Background())

	f := NewForge(cfg.WorkDir, cfg)
	l := NewLLMClient(cfg)

	a := &AgentRunner{
		cfg:    cfg,
		llm:    l,
		forge:  f,
		stats:  &SessionStats{StartTime: time.Now()},
		ctx:    ctx,
		cancel: cancel,
		history: []ChatMessage{
			{Role: "system", Content: buildSystemPrompt(cfg.WorkDir)},
		},
	}
	return a, nil
}

func (a *AgentRunner) Shutdown() {
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
		if err == nil && a.cfg != nil {
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

	select {
	case <-runCtx.Done():
		return runCtx.Err()
	default:
	}

	turnStart := time.Now()
	logEvent(EvTurnStarted, input, nil)
	// 躯壳自检: 每轮增量扫描错误事件, 新问题超阈值才 1 句话提示
	maybePrintHealthHint(a.cfg.WorkDir)

	// Build messages: system + history + current user message
	messages := make([]ChatMessage, len(a.history)+1)
	copy(messages, a.history)
	// 跨 user turn 清理 reasoning_content (deepseek-harness §1.3 规则3):
	// 见 stripCrossTurnReasoning 注释。仅清理无 tool_calls 的 assistant,
	// 工具循环内 (messages 局部变量) 不受影响。
	cleaned := stripCrossTurnReasoning(messages[:len(a.history)])
	copy(messages, cleaned)
	// v2.2: BM25 动态召回既往经验注入当前用户轮次(不进 system → 缓存前缀恒定)。
	userContent := input
	if a.cfg != nil {
		if recalled, _ := RecallMemory(a.cfg.WorkDir, input, 5); recalled != "" {
			userContent = recalled + "\n" + input
		}
	}
	messages[len(a.history)] = ChatMessage{Role: "user", Content: userContent}

	tools := []json.RawMessage{ForgeToolSchema()}

	assistantContent := strings.Builder{}
	consecutiveFails := 0
	unknownTools := 0 // hallucinated tool-name calls (must terminate eventually)

	// Track repeated tool calls to detect loops
	callHistory := make(map[string]int) // hash -> count
	const maxRepeatedCalls = 3

	// Goal anchor: inject the original task into every tool result so the LLM
	// never loses sight of what it was asked to do, even after trimHistory
	// removes the original user message from the context window.
	taskAnchor := truncateAnchor(input)

	var reasoningBuf strings.Builder

	// ── 动态模型路由 ──
	// 简单任务→flash(便宜)，复杂任务→pro(强)。失败时自动升级：
	// flash 连续失败 1 次后升级到 pro 重试，防止 flash 能力不足导致任务失败。
	curModel := pickModel(a.cfg, input)
	a.stats.setModel(curModel)
	escalated := false // 是否已从 flash 升级到 pro

	// 回合数限制已取消：无限循环，直到任务完成、连续失败中止或上下文取消。
	for turn := 0; ; turn++ {
		select {
		case <-runCtx.Done():
			return runCtx.Err()
		default:
		}

		toolCallAccum := []ToolCall{}
		assistantContent.Reset()
		reasoningBuf.Reset()

		// Streaming call — spinner spins until the first token arrives.
		spin := startSpinner("铸剑炉思考中…")
		ch := a.llm.ChatCompletionStream(runCtx, messages, tools, curModel)
		streamErr := a.processStream(ch, &assistantContent, &reasoningBuf, &toolCallAccum, a.cfg.ShowReasoning, spin.stop)
		spin.stop()

		if streamErr != nil {
			// 失败升级: 当前是 flash 且未升级过 → 升级到 pro 重试一次
			if !escalated && curModel == a.cfg.ModelFlash && a.cfg.ModelPro != "" && a.cfg.ModelPro != a.cfg.ModelFlash {
				escalated = true
				curModel = a.cfg.ModelPro
				a.stats.setModel(curModel)
				continue // 重进循环, 用 pro 重新请求
			}
			// Avoid consecutive user messages: if a retry re-enters with the same
			// input, appending again would produce user,user — some APIs reject
			// that with a 400.
			if len(a.history) == 0 || a.history[len(a.history)-1].Role != "user" || a.history[len(a.history)-1].Content != input {
				a.history = append(a.history, ChatMessage{Role: "user", Content: input})
			}
			a.trimHistory()
			if errors.Is(streamErr, context.Canceled) {
				return context.Canceled
			}
			logEvent(EvError, "LLM error", nil)
			return fmt.Errorf("LLM error: %w", streamErr)
		}

		// No tool calls -> final response
		if len(toolCallAccum) == 0 {
			asst := extractAssistantText(&assistantContent, &reasoningBuf)
			a.history = append(a.history,
				ChatMessage{Role: "user", Content: input},
				ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoningBuf.String()},
			)
			a.trimHistory()
			a.stats.addTurn(time.Since(turnStart))
			return nil
		}

		// Filter out unmerged stream fragments: a raw delta slice (which happens when
		// finish_reason was stop/length instead of tool_calls) contains per-chunk entries
		// with empty ID or empty function name. Treating those as independent tool calls
		// makes the agent execute partial arguments and produces tool responses whose
		// tool_call_id cannot be matched back to assistant.tool_calls → API 400.
		toolCallAccum = filterValidToolCalls(toolCallAccum)
		if len(toolCallAccum) == 0 {
			// All fragments were invalid — treat as a plain (non-tool) response
			asst := extractAssistantText(&assistantContent, &reasoningBuf)
			a.history = append(a.history,
				ChatMessage{Role: "user", Content: input},
				ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoningBuf.String()},
			)
			a.trimHistory()
			a.stats.addTurn(time.Since(turnStart))
			return nil
		}

		// Add assistant message with tool calls (text stripped — 生成与执行分离原则)
		// Per axiom 2: LLM text alongside tool calls is premature analysis that crowds out
		// the tool call itself. Strip it. The LLM will analyze AFTER seeing the result.
		messages = append(messages, ChatMessage{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: reasoningBuf.String(),
			ToolCalls:        toolCallAccum,
		})

		// Execute each tool call
		for _, tc := range toolCallAccum {
			if tc.Function.Name != ForgeToolName {
				// Unknown tool -> return error to LLM. A single hallucinated tool
				// name is not a code failure, but a model that keeps inventing tools
				// must terminate: otherwise the agent loops forever.
				unknownTools++
				errMsg := buildUnknownToolMsg(tc.Function.Name)
				messages = append(messages, ChatMessage{
					Role: "tool", ToolCallID: tc.ID, Content: errMsg,
				})
				maxFails := effectiveMaxFails(a.cfg.MaxConsecutiveFails)
				if abort, _ := abortUnknownTools(unknownTools, maxFails); abort {
					a.history = append(a.history, ChatMessage{Role: "user", Content: input})
					a.trimHistory()
					logEvent(EvError, "未知工具幻觉", map[string]int{"count": unknownTools})
					return fmt.Errorf("连续 %d 次调用未知工具（工具幻觉），已中止", unknownTools)
				}
				continue
			}

			// Parse params
			params, parseErr := parseForgeParams(tc.Function.Arguments)
			if parseErr != nil {
				messages = append(messages, ChatMessage{
					Role: "tool", ToolCallID: tc.ID,
					Content: fmt.Sprintf("参数解析失败: %v。请检查 JSON 格式。有效的参数: action(必需), code(必需), lang(可选), input(可选)。", parseErr),
				})
				// Don't increment — JSON formatting glitch, not a code logic failure
				continue
			}

			// Detect repeated calls (loop detection)
			callHash := hashCall(params.Code, params.Lang, params.Input)
			blocked, callCount := checkRepeatedCall(callHash, callHistory, maxRepeatedCalls)
			if blocked {
				messages = append(messages, ChatMessage{
					Role: "tool", ToolCallID: tc.ID,
					Content: fmt.Sprintf("重复调用检测: 相同的代码已执行 %d 次。请改变策略。", callCount),
				})
				consecutiveFails++
				continue
			}

			// Execute — display code with syntax highlighting (anti-hallucination)
			displayToolCode(params.Code, params.Lang)
			fmt.Fprintf(os.Stderr, "  %s %s\n", dim("⚙"), dim("执行中…"))
			toolStart := time.Now()
			logEvent(EvToolCalled, params.Lang, map[string]string{"code": truncateCN(params.Code, 300), "lang": params.Lang})
			output, result, execErr := a.forge.Build(params.Code, params.Lang, params.Input)
			toolDuration := time.Since(toolStart)
			logEvent(EvToolResult, params.Lang, map[string]interface{}{"ok": execErr == nil && result != nil && result.OK, "duration_ms": toolDuration.Milliseconds()})

			if execErr != nil || (result != nil && !result.OK) {
				// Classify failure severity. Transient errors (timeouts, tool-not-found,
				// parse failures) get a lighter penalty — they're often environmental.
				isTransient := classifyTransient(result)
				if isTransient {
					consecutiveFails += 0 // don't penalize environmental failures
				} else {
					consecutiveFails++
				}
				a.stats.addToolFail(toolDuration)

				errDetail := ""
				if result != nil && result.Error != "" {
					errDetail = result.Error
				} else if execErr != nil {
					errDetail = execErr.Error()
				}

				stage := "?"
				if result != nil {
					stage = result.Stage
				}

				maxFails := effectiveMaxFails(a.cfg.MaxConsecutiveFails)
				if shouldAbortOnFails(consecutiveFails, maxFails) {
					fmt.Fprintf(os.Stderr, "  %s %d consecutive failures (non-transient), aborting\n",
						color(ansi.red, "✗"), consecutiveFails)
					a.history = append(a.history, ChatMessage{Role: "user", Content: input})
					a.trimHistory()
					logEvent(EvError, "连续失败中止", map[string]int{"count": consecutiveFails})
					return fmt.Errorf("连续 %d 次铸剑炉调用失败（非瞬时错误），已中止", consecutiveFails)
				}

				if len(errDetail) > 150 {
					errDetail = errDetail[:150] + "..."
				}
				fmt.Fprintf(os.Stderr, "  %s %s %s · %s\n",
					langEmoji(params.Lang),
					color(ansi.red, "✗ FAIL"),
					color(ansi.yellow, fmt.Sprintf("[%s]", stage)),
					dim(fmt.Sprintf("%dms", toolDuration.Milliseconds())))

				if errDetail != "" {
					fmt.Fprintf(os.Stderr, "  %s %s\n", dim("  ->"), color(ansi.red, errDetail))
				}
			} else {
				consecutiveFails = 0
				a.stats.addToolOK(toolDuration)

				outLen := 0
				if result != nil {
					outLen = len(result.Stdout)
				}
				fmt.Fprintf(os.Stderr, "  %s %s %s · %s · %s\n",
					langEmoji(params.Lang),
					color(ansi.green, "✓ OK"),
					dim(fmt.Sprintf("%s", strings.ToUpper(params.Lang))),
					dim(fmt.Sprintf("%dms", toolDuration.Milliseconds())),
					dim(formatSize(outLen)))

				// Show output lines (anti-hallucination: user sees actual compiler output)
				outStr := strings.TrimSpace(output)
				if outStr != "" {
					outLines := strings.Split(outStr, "\n")
					showOut := 8
					if len(outLines) < showOut {
						showOut = len(outLines)
					}
					for i := 0; i < showOut; i++ {
						ol := outLines[i]
						if len(ol) > 120 {
							ol = ol[:120] + "..."
						}
						fmt.Fprintf(os.Stderr, "  %s %s\n", dim("│"), ol)
					}
					if len(outLines) > showOut {
						fmt.Fprintf(os.Stderr, "  %s %s\n", dim("╰─"), dim(fmt.Sprintf("... +%d more lines", len(outLines)-showOut)))
					}
				}
			}
			fmt.Fprintf(os.Stderr, "\n")

			// Remember last tool output for /last
			a.lastOutMu.Lock()
			a.lastOut = output
			a.lastOutMu.Unlock()

			// Goal anchor
			warnedOutput := goalAnchor(output, taskAnchor, turn)

			messages = append(messages, ChatMessage{
				Role: "tool", ToolCallID: tc.ID, Content: warnedOutput,
			})
		}

		// Trim accumulated turn messages: a long task can grow `messages` without
		// bound (assistant+tool pairs per turn). Drop the oldest COMPLETE turns so
		// the assistant(tool_calls) ↔ tool response pairing is never broken.
		// messages[0] (system / history head) is preserved.
		if len(messages) > 60 {
			messages = trimTurnMessages(messages, 60)
		}
	}

	// 回合数限制已取消：for{...} 永不因轮数退出，因此此函数无正常返回路径。
	// 真正的安全网仍然保留：
	//   1. consecutiveFails — 连续铸剑炉调用失败中止 (默认5次)
	//   2. callHistory      — 重复调用检测 (相同代码最多3次)
	//   3. a.ctx.Done()     — 上下文取消 (Ctrl+C / 超时)
}

// ─── Stream processing ─────────────────────────────────────

func (a *AgentRunner) processStream(
	ch <-chan StreamEvent,
	contentBuf *strings.Builder,
	reasoningBuf *strings.Builder,
	toolCalls *[]ToolCall,
	showReasoning bool,
	onFirstEvent func(),
) error {
	var tokenCount int64
	inReasoning := false
	renderer := newStreamRenderer()
	firstEvent := true

	for ev := range ch {
		if firstEvent {
			firstEvent = false
			if onFirstEvent != nil {
				onFirstEvent()
			}
		}
		switch ev.Type {
		case "error":
			return ev.Error

		case "reasoning":
			reasoningBuf.WriteString(ev.Content)
			if showReasoning {
				if !inReasoning {
					fmt.Fprint(os.Stderr, color(ansi.magenta, "\n🧠 "))
					inReasoning = true
				}
				fmt.Fprint(os.Stderr, color(ansi.dim, ev.Content))
			}

		case "content":
			if inReasoning {
				fmt.Fprint(os.Stderr, ansi.reset+"\n\n")
				inReasoning = false
			}
			// Use the stream renderer for syntax highlighting
			fmt.Print(renderer.feed(ev.Content))
			contentBuf.WriteString(ev.Content)
			tokenCount += int64(utf8.RuneCountInString(ev.Content))

		case "tool_call_delta":
			// Accumulated silently during streaming
			*toolCalls = append(*toolCalls, ev.ToolCalls...)

		case "tool_call_done":
			// Replace accumulated deltas with merged result
			*toolCalls = ev.ToolCalls
			// Flush any remaining renderer state
			fmt.Print(renderer.flush())
			if inReasoning {
				fmt.Fprint(os.Stderr, ansi.reset+"\n")
				inReasoning = false
			}
			fmt.Println()
			a.stats.addToken(tokenCount)
			return nil

		case "done":
			// Flush any remaining renderer state
			fmt.Print(renderer.flush())
			if inReasoning {
				fmt.Fprint(os.Stderr, ansi.reset+"\n")
				inReasoning = false
			}
			fmt.Println()
			a.stats.addToken(tokenCount)
			if ev.Truncated {
				fmt.Fprintf(os.Stderr, "%s⚠️ 输出被截断: 达到 max_tokens=%d 上限, 回复不完整%s\n",
					ansi.yellow, a.cfg.MaxTokens, ansi.reset)
				if ev.ReasoningTokens > 0 {
					fmt.Fprintf(os.Stderr, "%s   (其中推理消耗 %d tokens; 需要完整输出可设 LLM_MAX_TOKENS 调大)%s\n",
						ansi.dim, ev.ReasoningTokens, ansi.reset)
				}
			}
			return nil
		}
	}

	fmt.Print(renderer.flush())
	a.stats.addToken(tokenCount)
	return nil
}

// ─── History management ─────────────────────────────────────

func (a *AgentRunner) trimHistory() {
	maxHist := a.cfg.MaxHistoryMessages
	if maxHist <= 0 {
		maxHist = 40
	}

	// 缓存友好裁剪 (六西格玛 P1): 从尾部成对删除完整轮次 [user, assistant],
	// 保持头部前缀 [system, u1, a1, ...] 稳定 —— DeepSeek 前缀缓存按 token 前缀
	// 完全匹配, 旧实现从头部删 history[1] 会使前缀在 system 之后断裂 → 后续请求
	// 全量 miss (实测: 前缀断裂命中率 0%)。
	for len(a.history) > maxHist {
		n := len(a.history)
		if n < 2 {
			break // 只剩 system (或不足一轮), 不再删
		}
		// 尾部完整轮次 [user, assistant] 成对删
		if a.history[n-1].Role == "assistant" && a.history[n-2].Role == "user" {
			a.history = a.history[:n-2]
			continue
		}
		// 尾部孤立 user (失败/中止路径 append 的) 或异常形态: 删单条。
		// 孤立 user 前无配对 assistant, 删掉不破坏 API 的 user→assistant 配对。
		a.history = a.history[:n-1]
	}
}

// trimTurnMessages drops the oldest complete tool-call turns from msgs until
// len(msgs) <= maxTotal. It never breaks the assistant(tool_calls) ↔ tool
// response pairing, and never removes msgs[0] (system/history head).
func trimTurnMessages(msgs []ChatMessage, maxTotal int) []ChatMessage {
	for len(msgs) > maxTotal {
		cutStart, cutEnd := -1, -1
		for i := 1; i < len(msgs); i++ {
			if msgs[i].Role == "assistant" && len(msgs[i].ToolCalls) > 0 {
				cutStart = i
				cutEnd = i + 1
				ids := make(map[string]bool, len(msgs[i].ToolCalls))
				for _, tc := range msgs[i].ToolCalls {
					ids[tc.ID] = true
				}
				for j := i + 1; j < len(msgs); j++ {
					if msgs[j].Role == "tool" && ids[msgs[j].ToolCallID] && j+1 > cutEnd {
						cutEnd = j + 1
					}
				}
				break
			}
		}
		if cutStart < 0 {
			break // nothing safe to trim
		}
		msgs = append(msgs[:cutStart], msgs[cutEnd:]...)
	}
	return msgs
}

// ─── Tool call helpers ─────────────────────────────────────

func parseForgeParams(argsJSON string) (ForgeParams, error) {
	var p ForgeParams
	if err := json.Unmarshal([]byte(argsJSON), &p); err != nil {
		return p, fmt.Errorf("invalid JSON: %w", err)
	}
	if p.Code == "" {
		return p, fmt.Errorf("code field is required")
	}
	return p, nil
}

func hashCall(code, lang, input string) string {
	// Use SHA256 same as forge.go cacheKey for loop detection
	h := sha256.New()
	h.Write([]byte(code))
	h.Write([]byte{0})
	h.Write([]byte(lang))
	h.Write([]byte{0})
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

// Placeholder - actual implementation at top uses crypto/sha256

// ─── Display helpers ───────────────────────────────────────

func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// ─── Tool execution display (anti-hallucination) ───────────────

// displayToolCode prints the source code being executed with syntax highlighting.
// This lets the user verify that the tool was actually called, preventing LLM hallucination.
func displayToolCode(code, lang string) {
	emoji := langEmoji(lang)
	langLabel := strings.ToUpper(lang)
	if lang == "" {
		langLabel = "CODE"
	}

	// Top border
	fmt.Fprintf(os.Stderr, "  %s %s\n", emoji, color(ansi.cyan, "─── "+langLabel+" "+strings.Repeat("─", max(0, 40-len(langLabel)))))

	codeLines := strings.Split(code, "\n")
	maxShow := 30
	showLines := codeLines
	truncated := false
	if len(codeLines) > maxShow {
		showLines = codeLines[:maxShow]
		truncated = true
	}

	for _, cl := range showLines {
		// Apply syntax highlighting
		highlighted := highlightLine(cl, lang)
		fmt.Fprintf(os.Stderr, "  %s %s\n", dim("│"), highlighted)
	}
	if truncated {
		fmt.Fprintf(os.Stderr, "  %s %s\n", dim("│"), dim(fmt.Sprintf("... +%d more lines", len(codeLines)-maxShow)))
	}
}

// extractAssistantText 收尾兜底: 正文为空时依次回退到 reasoning 与占位符。
// 两处收尾块共用(No tool calls 分支 + 流片段过滤后空分支), 消除重复逻辑。
func extractAssistantText(asst, reason *strings.Builder) string {
	s := asst.String()
	if strings.TrimSpace(s) == "" && strings.TrimSpace(reason.String()) != "" {
		s = reason.String() // 模型仅输出 reasoning，兜底为正文避免 API 400
	}
	if strings.TrimSpace(s) == "" {
		s = "[empty response]"
	}
	return s
}

// buildUnknownToolMsg returns the error message fed back to the LLM when it
// invokes a tool other than forge (hallucinated tool names). A single
// hallucinated tool name is not a code failure, but a model that keeps
// inventing tools must terminate: otherwise the agent loops forever.
func buildUnknownToolMsg(name string) string {
	return fmt.Sprintf("未知工具: %s。你只有 forge（铸剑炉）一个工具。请用 forge。", name)
}

// checkRepeatedCall increments the call-hash counter and reports whether the
// same code has been executed more than maxRepeated times (loop detection).
// It returns the updated counter and whether the threshold was exceeded.
func checkRepeatedCall(callHash string, callHistory map[string]int, maxRepeated int) (bool, int) {
	callHistory[callHash]++
	return callHistory[callHash] > maxRepeated, callHistory[callHash]
}

// classifyTransient reports whether a forge Build failure is environmental
// (timeout, missing tool, exit-status) rather than a genuine code bug.
// Non-transient failures count toward consecutive-fail aborts; transient ones
// are not penalized because they are often environmental.
func classifyTransient(result *ForgeGateResult) bool {
	if result == nil {
		return false
	}
	stage := result.Stage
	errLower := strings.ToLower(result.Error)
	// compile/check stage failures are real code bugs in the LLM output → count fully.
	// execute stage timeouts or missing tools are environmental → no penalty.
	if stage == "execute" && (strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "not found") ||
		strings.Contains(errLower, "找不到") ||
		strings.Contains(errLower, "exit status")) {
		return true
	}
	if stage == "compile" && strings.Contains(errLower, "not found") {
		return true
	}
	return false
}
