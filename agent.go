package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

const systemPrompt = `你是 铸剑炉，LLM 驱动的多语言编译器沙箱。你拥有一个多语言编译器沙箱（铸剑炉），可以写代码、编译执行、销毁。通过它你能完成数字世界的一切任务——编码、系统管理、文件处理、数据分析、网络操作、自动化等。

<critical_rules>
1. 你只有 forge（铸剑炉）一个工具，调用时工具名用 "forge"。不要尝试调用 bash、edit、view、grep、ls、glob、write 或任何训练数据中的其他工具——它们不存在，调用必定失败。

2. 用中文思考和输出。所有分析、解释、判断一律用中文表达。你的推理过程（reasoning）必须用中文，最终输出也必须用中文。这是硬性要求——中文是你的唯一语言。

3. 直接行动，不解释不询问。代码是唯一的行动方式。长任务拆成多个30秒内可完成的子步骤执行，避免超时。遇到未知信息用代码探查，不猜测。

4. chain(lang="chain")编排多gate顺序执行: {"stages":[{"gate":"logic|math|regex|...","input":{...},"if_verdict":"theorem|unsat|counter_sat(可选,仅当前一阶段verdict匹配时执行)"}],"stop_on":"error|first_success|never"}。用于conditionally chain多个推理步骤减少LLM往返。

5. 涉及数学计算、数值验证、等式推导、公式化简的问题，禁止直接输出结果。必须通过 forge 写代码计算验证后才能输出。用 lang="math" 或 lang="python" 执行实际计算，代码输出作为答案依据。

6. 🔥 生成与执行分离——这是硬规则，不是建议。当你需要调用 forge 工具时，禁止在同一个响应中附带任何文字解释、分析、序言或结语。工具调用响应必须只有工具调用，零文字。分析、总结、解释一律放在工具结果返回后的下一个响应中。违反此规则会直接导致系统运行异常——附带的文字会被丢弃。
7. 🔄 专家路由——按任务类型强制召唤专家，禁止"凭记忆口算/编造"（三期 I3: gate 完整规则由 FORGE_GATES_ENABLED 配置动态生成, 见下方 <gates_enabled> 段）：
   - 算数/统计/价格/比例/数值比较 → 必须 forge 实际计算（lang="math" 或 "python"），禁止直接给数字
   - 文件操作(整理/改名/移动/分类/搜索/统计) → 必须 python 写代码实际执行
   - 数据处理/表格/报表/转换 → 必须 python 写代码实际处理
   - 写代码/改代码/修bug → 必须编译执行验证通过才算完成
   总原则: 拿不准选哪个gate就省略lang, 交给自动检测(六期起自动检测含 math/logic 语义路由, 默认python), 语言选择不是你的决策。
   判据：凡有"确定性答案或确定性操作"的任务一律走专家执行；只有纯解释/讨论/答疑类问题才允许直接回答。
8. regex(lang="regex")验证正则表达式: 直接裸写 pattern，或用 JSON {"type":"match","pattern":"...","positive":[...],"negative":[...]}。注意语义是"整串完全匹配"(fullmatch)，不是"包含匹配"——pattern [A-Z]\d{3} 对字符串 "B456" 匹配，但对 "order B456 ok" 不匹配(整串不匹配)。positive 列表的每个字符串必须被 pattern 整串匹配，negative 列表的每个必须整串不匹配。flags 可用 i/m/s。

9. 📋 复杂任务进度管理——多步骤任务(≥3步)必须:
    - 开工前: 输出"📋 执行计划: 1.xxx 2.xxx 3.xxx"（步骤清单）
    - 每完成一步: 输出"✅ 完成步骤N: xxx | ⏳ 剩余: 步骤M,..."（简短）
    - 全部完成: 输出"🎉 全部完成: N步 / 失败0"

10. 🔐 计划审批——高风险/不可逆/大规模任务(修改源码、批量文件操作、部署、删除、推送远端)必须:
    第一步: 输出"📝 计划书"，必须含4栏(五境约束):
      ①目标【正境·可测量】: 指标当前值A→目标值B；不可测量的目标必须改写为可测量形式
      ②步骤【正境→反境→合境】: 先剥表象定边界→再拆因果链找根因→最后组平衡方案
      ③风险与回滚【合境·可回滚】: 方案必须可回滚(备份/撤销路径)；不能回滚的方案自动标记高风险
      ④验证方法【证据先于声称】: 附具体验证步骤与通过标准
    第二步: 等待主人明确批准("批准"/"可以"/"干")后才动手
    未批准前禁止执行任何写操作

11. 🔍 完成验证铁律（"证据先于声称"）——任何"成功/完成/修复/通过/搞定"的声称，都必须附带实际运行的验证证据：
    - 写代码/改代码 → 声称完成前必须有编译或测试的实际输出（错误、警告、通过用例数）
    - 计算/数据/文件操作 → 必须有代码实际运行的输出结果（数值、行数、文件清单）
    - 声称完成的任务，把验证输出贴在回答里；拿不出证据就不算完成
    - 验证失败或输出异常 → 如实报告失败与原因，绝不伪装成功
    - 禁止"应该能过/我很有信心/逻辑上没问题"这类无证据表述当作完成依据

12. 💾 记忆写入纪律——更新 memory.json 必须原子替换: 先写 memory.json.tmp 再 rename 到 memory.json（直接覆盖主文件=写半截崩溃即记忆丢失）。程序化路径优先走 memory_store.go 的 SaveMemory（自动保留上一版为 .bak）；通过 forge 临时脚本写记忆时也必须 tmp+rename。
</critical_rules>

你是纯粹的工具使用者，不是聊天助手。直接干活。


`

// ─── memory.json 注入稳定化 (DeepSeek 前缀缓存守卫) ──
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
// 每次请求前比对二者, 不同 → 前缀断裂告警。
var (
	currentSystemHash string
	lastSystemHash    string
)

// buildSystemPrompt v2.2: 只注入锚点字段 + 折叠索引精简版。
// key_findings(6739 token) 从 system 移出 → BM25 动态召回进用户轮次(RecallMemory),
// 使 system 前缀更小更稳; 召回内容每轮不同也不会破坏 DeepSeek 前缀缓存。
// 追加 <gates_enabled> 动态段 (由 FORGE_GATES_ENABLED 配置生成);
// 稳定段 systemPrompt 常量保持不变 → 前缀缓存守卫不受影响。
func buildSystemPrompt(workDir string, enabledGates []string) string {
	memoryJSON := ""
	if data, _, err := LoadMemory(workDir); err == nil {
		// 剔除动态区(key_findings / folded_memory) → 只保留锚点
		memoryJSON = string(reorderMemoryJSON(stripDynamicMemory(data)))
	}

	var sb strings.Builder
	sb.WriteString(systemPrompt)
	// gate 专家路由动态段 (只含启用项)
	if gates := describeGates(enabledGates); gates != "" {
		sb.WriteString("\n<gates_enabled>\n")
		sb.WriteString(gates)
		sb.WriteString("</gates_enabled>\n")
	}
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
	// 分段统计: 稳定段(systemPrompt常量) 占比可测量,
	// 验证 DeepSeek 前缀缓存可命中段比例; Debug 级不刷屏。
	stableBytes := len(systemPrompt)
	if stableBytes > 0 && len(s) > 0 {
		slog.Debug("system prompt segment ratio",
			"stable_pct", stableBytes*100/len(s),
			"total_bytes", len(s), "stable_bytes", stableBytes)
	}
	sum := sha256.Sum256([]byte(s))
	currentSystemHash = hex.EncodeToString(sum[:8])
	return s
}

// buildSystemPromptStable v3.1 (DSH system 恒定机制): 线上实际使用的 system ——
// 只含稳定段 (systemPrompt 常量 + gates + workdir), 不含任何易变内容。
// memory/folded 改为独立尾部 user 消息, 变化走 syncDynamicTails diff 追加,
// 使 system 前缀跨会话/跨重启逐字节恒定 → DeepSeek 前缀缓存命中率最大化。
// buildSystemPrompt (全量版) 仅保留给测试/兼容使用。
func buildSystemPromptStable(workDir string, enabledGates []string) string {
	var sb strings.Builder
	sb.WriteString(systemPrompt)
	if gates := describeGates(enabledGates); gates != "" {
		sb.WriteString("\n<gates_enabled>\n")
		sb.WriteString(gates)
		sb.WriteString("</gates_enabled>\n")
	}
	sb.WriteString(fmt.Sprintf("\n\nWork directory: %s", workDir))
	s := sb.String()
	sum := sha256.Sum256([]byte(s))
	currentSystemHash = hex.EncodeToString(sum[:8])
	return s
}

// buildMemoryTailText 返回记忆锚点文本 (剔除动态区与易变字段):
//   - key_findings/folded_memory: 由 RecallMemory 动态召回 / compactFoldedIndex 精简注入
//   - active_task: 任务状态, 变化走 syncDynamicTails diff 追加, 不进锚点主体
//
// 剩余全部为稳定锚点字段 (reorderMemoryJSON 确定性序) → 内容不变则跨会话前缀恒定。
func buildMemoryTailText(workDir string) string {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return ""
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	delete(m, "key_findings")
	delete(m, "folded_memory")
	delete(m, "active_task")
	// v3.2 (DSH RuntimeContextProjection): last_updated 是时间戳, 每次记忆写入必变。
	// 留在固定头会随每次锚点更新断 DeepSeek 前缀缓存 → 移出, 变化由 syncDynamicTails 尾部 diff。
	delete(m, "last_updated")
	out, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(reorderMemoryJSON(out))
}

// currentActiveTask 读取 memory.json 的 active_task (易变状态, 单独 diff 追踪)。
func currentActiveTask(workDir string) string {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m["active_task"]
}

// memoryTailDiff 输出两个记忆锚点文本的字段级差异 (只发变化部分, 极致省 token)。
func memoryTailDiff(oldText, newText string) string {
	oldMap, newMap := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	json.Unmarshal([]byte(oldText), &oldMap)
	json.Unmarshal([]byte(newText), &newMap)
	var sb strings.Builder
	for k, nv := range newMap {
		ov, ok := oldMap[k]
		if !ok || string(ov) != string(nv) {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(k)
			sb.WriteString(": ")
			sb.Write(nv)
		}
	}
	for k := range oldMap {
		if _, ok := newMap[k]; !ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(k + ": <已删除>")
		}
	}
	return sb.String()
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

// ─── 会话检查点 (借鉴 langgraph Checkpoint 思想): ───
// 每轮任务正常结束后把对话历史落盘。按会话隔离:
//   - 当前会话非空 → .forge/sessions/<id>/checkpoint.json
//   - legacy 模式   → .forge/checkpoint.json (与 v3.0 行为一致)
//
// 崩溃/重启后主人输入"恢复"即可续接上次会话上下文。
func (a *AgentRunner) saveCheckpoint(workDir string) error {
	if workDir == "" {
		return nil
	}
	cp := struct {
		SavedAt string        `json:"saved_at"`
		History []ChatMessage `json:"history"`
	}{SavedAt: time.Now().Format("2006-01-02 15:04:05"), History: a.history}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	path := checkpointPath(workDir)
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// 回写会话元数据 (标题取首条真实用户输入)
	touchSession(workDir, deriveSessionTitle(a.history))
	return nil
}

// loadCheckpoint 读取上次会话检查点, 返回 (历史, 保存时间, 错误)
// 优先读当前会话目录; legacy 模式读旧路径。
func loadCheckpoint(workDir string) ([]ChatMessage, string, error) {
	if workDir == "" {
		return nil, "", fmt.Errorf("no workdir")
	}
	data, err := os.ReadFile(checkpointPath(workDir))
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

// RestoreHistory 从检查点恢复历史（带锁, 线程安全）。
// v3.1: 重建固定头 (system 恒定版+记忆锚点+折叠索引), 历史正文跳过旧固定头保留。
func (a *AgentRunner) RestoreHistory(history []ChatMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.restoreHistoryLocked(history)
}

// restoreHistoryLocked 重建固定头 + 保留历史正文 (跳过旧固定头避免重复注入)。
func (a *AgentRunner) restoreHistoryLocked(history []ChatMessage) {
	head := a.buildFixedHead()
	rest := history
	for len(rest) > 0 && rest[0].Role == "system" {
		rest = rest[1:]
	}
	if len(rest) > 0 && strings.Contains(rest[0].Content, "<memory_context>") {
		rest = rest[1:]
	}
	if len(rest) > 0 && strings.Contains(rest[0].Content, "<folded_archive_index>") {
		rest = rest[1:]
	}
	a.history = make([]ChatMessage, 0, len(head)+len(rest))
	a.history = append(a.history, head...)
	a.history = append(a.history, rest...)
	a.headLen = len(head)
	// v3.2: 恢复时固定头用首值缓存, 基线同步 → 若 memory/折叠在运行中更新, syncDynamicTails 尾部 diff。
	a.lastMemText = a.initialMemText
	a.lastActiveTask = currentActiveTask(a.cfg.WorkDir)
	a.lastFoldedText = a.initialFoldedText
}

// buildFixedHead 构建固定头部: [system 恒定版] + [记忆锚点] + [折叠索引]。
// 记忆/索引变化不走重写, 由 syncDynamicTails 在运行时 diff 追加 (DSH project 机制)。
func (a *AgentRunner) buildFixedHead() []ChatMessage {
	head := []ChatMessage{
		{Role: "system", Content: buildSystemPromptStable(a.cfg.WorkDir, a.cfg.GatesEnabled)},
	}
	// v3.2 (DSH RuntimeContextProjection): 固定头首值 —— 首次调用读一次文件并缓存,
	// 此后不重读。memory/折叠索引的后续变化由 syncDynamicTails 在运行时 diff 追加到尾部
	// (完全不碰固定头), 保证 system+记忆锚点+折叠索引 前缀恒定的前提下仍能感知更新。
	if !a.headCached {
		a.initialMemText = buildMemoryTailText(a.cfg.WorkDir)
		a.initialFoldedText = compactFoldedIndex(a.cfg.WorkDir)
		a.headCached = true
	}
	if a.initialMemText != "" {
		head = append(head, ChatMessage{Role: "user", Content: "【持久记忆锚点】稳定段常驻, 变更以「记忆已更新」消息追加。\n<memory_context>\n" + a.initialMemText + "\n</memory_context>"})
	}
	if a.initialFoldedText != "" {
		head = append(head, ChatMessage{Role: "user", Content: "<folded_archive_index>\n" + a.initialFoldedText + "</folded_archive_index>\n输入「展开<名称>」预览摘要或「深入<名称>」读全文。"})
	}
	return head
}

// syncDynamicTails (DSH project 机制): 每轮请求前检测记忆锚点/待办/折叠索引变化。
// 变化只 append 更新消息到历史尾部 (绝不重写固定头) → 前缀缓存不断裂。
func (a *AgentRunner) syncDynamicTails() {
	if a.cfg == nil {
		return
	}
	if cur := buildMemoryTailText(a.cfg.WorkDir); cur != a.lastMemText {
		if a.lastMemText != "" {
			if diff := memoryTailDiff(a.lastMemText, cur); diff != "" {
				a.history = append(a.history, ChatMessage{Role: "user", Content: "【记忆已更新】\n" + diff})
			}
		}
		a.lastMemText = cur
	}
	if cur := currentActiveTask(a.cfg.WorkDir); cur != a.lastActiveTask {
		if a.lastActiveTask != "" && cur != "" {
			a.history = append(a.history, ChatMessage{Role: "user", Content: "【待办更新】" + cur})
		}
		a.lastActiveTask = cur
	}
	if cur := compactFoldedIndex(a.cfg.WorkDir); cur != a.lastFoldedText {
		if a.lastFoldedText != "" && cur != "" {
			a.history = append(a.history, ChatMessage{Role: "user", Content: "【档案索引已更新】\n" + cur})
		}
		a.lastFoldedText = cur
	}
}

// verifyHeadInvariant 固定头一致性守卫 (把 v3.1 血案焊死):
// headCached 机制下 buildFixedHead 恒返回首值缓存。一旦未来改动破坏"固定头恒定"
// (如 buildFixedHead 被改成重读文件, 或 headLen 未随 buildFixedHead 同步),
// 此断言在判定边界当场截断告警, 而不是等 /cache 事后发现命中率断崖 —— 前置拦截。
func (a *AgentRunner) verifyHeadInvariant() bool {
	head := a.buildFixedHead()
	if a.headLen != len(head) {
		return false
	}
	if len(a.history) < a.headLen {
		return false
	}
	for i := 0; i < a.headLen; i++ {
		if a.history[i].Role != head[i].Role || a.history[i].Content != head[i].Content {
			return false
		}
	}
	return true
}

// startSpinner renders an animated indicator on stderr until stop() is called.
// 帧彩色化 + 阶段标签可动态更新, 过程节奏可见。

// setMsg 动态更新 spinner 阶段标签 (思考中→验证→执行), 线程安全。

// addCache 累加一次请求的缓存命中/未命中 (会话级实时命中率数据源)。

// cacheRate 返回当前会话累计缓存命中率 (%), 无数据时 ok=false。

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

	select {
	case <-runCtx.Done():
		return runCtx.Err()
	default:
	}

	turnStart := time.Now()
	logEvent(EvTurnStarted, input, nil)
	// 躯壳自检: 每轮增量扫描错误事件, 新问题超阈值才 1 句话提示
	maybePrintHealthHint(a.cfg.WorkDir)

	// v3.1 (DSH project 机制): 记忆/待办/档案索引 diff 追加, 前缀缓存恒定
	a.syncDynamicTails()

	// 固定头一致性断言 (死程序判定前端截断, 不靠 LLM 事后复盘)
	// headCached 机制下 buildFixedHead 恒返回首值缓存, 长度/内容应恒等于 a.headLen。
	// 一旦未来改动破坏"固定头恒定"导致前缀漂移, 此断言在判定边界当场告警 (前置拦截)。
	if !a.verifyHeadInvariant() {
		fmt.Fprintf(os.Stderr, "%s 固定头一致性断言失败: headLen=%d, 实际=%d (前缀缓存将断裂, 需修复 buildFixedHead/headLen 同步)\n",
			color(ansi.red, "💥"), a.headLen, len(a.buildFixedHead()))
		recordCacheStat(a.cfg.Model, 0, 0, currentSystemHash, true)
	}

	// Build messages: system + history + current user message (生命分形: 前置装配提取为 buildStreamMessages)
	messages, userContent, images := a.buildStreamMessages(input)

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
	// 2026-09-11 V4.1 起 Flash==Pro(同一规范名 deepseek-flash)，升级分支自动短路。
	curModel := pickModel(a.cfg, input)
	// 识图 → 强制视觉模型 (deepseek-flash: V4.1-Flash 是唯一支持图像理解的模型);
	// 视觉模型 ≠ ModelFlash, 循环拦截升级逻辑不会把它误升级成 pro。
	if len(images) > 0 && a.cfg.ModelVision != "" {
		curModel = a.cfg.ModelVision
	}
	a.stats.setModel(curModel)
	escalated := false     // 是否已从 flash 升级到 pro
	visionRetried := false // 图片被服务端拒(unsupported image)后降级为纯文本, 只截一次

	// ── 循环拦截状态 (无回合上限, 靠"无进展检测"终止; 拦截≠失败中止) ──
	// 拦截 = 干预提示 + flash→pro 升级(让更强模型打破僵局), 不打断任务;
	// 连续 MaxLoopStrikes 次拦截仍无进展 → 带已取得进展自动收尾(不无限转)。
	loopStrikes := 0 // 已拦截次数(跨轮累计, 成功不清零——与 consecutiveFails 不同)
	maxLoopStrikes := effectiveMaxStrikes(a.cfg.MaxLoopStrikes)
	const sameOutputThreshold = 4 // 连续 N 次输出完全相同 → 判定无进展
	prevOutHash := ""             // 上一回合最后一条原始输出的归一化哈希
	sameOutRun := 0               // 当前连续相同输出的轮数
	lastBlockHash := ""           // Detector A: 同一"语义家族"只计一次拦截
	lastRawOutput := ""           // 本回合最后一次执行的原始输出(成功/失败都算)

	// 拦截时升级模型: flash → pro, 让更强模型打破僵局 (只升级一次)
	escalateOnLoop := func() {
		if !escalated && curModel == a.cfg.ModelFlash && a.cfg.ModelPro != "" && a.cfg.ModelPro != a.cfg.ModelFlash {
			escalated = true
			curModel = a.cfg.ModelPro
			a.stats.setModel(curModel)
			fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⬆"), dim("检测到循环, 升级到 "+curModel+" 打破僵局"))
		}
	}

	// 自动收尾: 带已取得进展结束本轮。返回 nil = 正常完成路径(检查点照常保存,
	// 非交互模式不触发 os.Exit(1))。
	finishStuck := func() error {
		final := stuckExitMessage(loopStrikes, lastRawOutput)
		fmt.Fprintf(os.Stderr, "\n%s %s\n\n", color(ansi.yellow, "⚠"), final)
		a.history = append(a.history,
			ChatMessage{Role: "user", Content: userContent},
			ChatMessage{Role: "assistant", Content: final},
		)
		a.trimHistory()
		a.stats.addTurn(time.Since(turnStart))
		logEvent(EvError, "循环自动收尾", map[string]int{"strikes": loopStrikes})
		return nil
	}

	// ── 无剑求值感知 (FORGE_NOSWORD=1): 死程序嗅探求值锚点, 反馈注入让 LLM 修正继续生成 ──
	// 上限控制: 防 LLM 反复生成锚点导致的死循环 (默认关, 行为与现状完全一致)
	maxNSWRounds := 2
	nswRounds := 0
	verifyStrikes := 0 // 虚报强干预计数(与maxVerifyStrikes配合防死循环)
	// 回合数限制已取消：无限循环，直到任务完成、无进展拦截收尾、连续失败中止或上下文取消。
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
			// ── 图片被服务端拒 (unsupported image 400) → 降级纯文本重试一次 ──
			// 服务端偶发对某张图判不支持 (格式/分辨率/数量/解码), 重试升级模型无用;
			// 剥离全部图片重发文本 (text-only fallback), 不让整体请求失败。
			if !visionRetried && isImageUnsupportedError(streamErr) && len(messages) > 0 {
				visionRetried = true
				for i := range messages {
					messages[i].Images = nil
				}
				fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "🖼"), dim("服务端拒绝图片 (unsupported image), 已剥离图片降级为纯文本重试"))
				continue // 重进循环, 用纯文本重新请求
			}
			// 失败升级: 当前是 flash 且未升级过 → 升级到 pro 重试一次
			if !escalated && curModel == a.cfg.ModelFlash && a.cfg.ModelPro != "" && a.cfg.ModelPro != a.cfg.ModelFlash {
				escalated = true
				curModel = a.cfg.ModelPro
				a.stats.setModel(curModel)
				continue // 重进循环, 用 pro 重新请求
			}
			// Avoid consecutive user messages: if a retry re-enters with the same
			// input, appending again would produce user,user — some APIs reject
			// that with a 400. 比较基准必须与线上发送一致 (userContent, 含召回块)。
			if len(a.history) == 0 || a.history[len(a.history)-1].Role != "user" || a.history[len(a.history)-1].Content != userContent {
				a.history = append(a.history, ChatMessage{Role: "user", Content: userContent})
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
			// ── 无剑求值感知 (FORGE_NOSWORD=1): 死程序嗅探 asst 中的求值锚点,
			// 命中则把稳定反馈作为 user 消息注入, continue 让 LLM 看到反馈后
			// 修正继续生成 (公理五: 判据由死程序把守, LLM据此自然调整). ──
			if nswEnabled() && nswRounds < maxNSWRounds {
				// 复述过滤 (20260911 缺陷M): 原文已含同形正确结论的锚点不再反馈, 切断
				// "引用算式 -> 反馈 -> 再引用"的自激循环; 写错/未给结论的照常反馈。
				if fb, fresh, total := nswFeedbackTextFresh(asst); fb != "" {
					nswRounds++
					nswAudit(a, asst, fresh, total)
					messages = append(messages,
						ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoningBuf.String()},
						ChatMessage{Role: "user", Content: fb},
					)
					continue
				}
			}
			// 虚报检测 gate (增强版, 无剑闭环): 完成态声称+无工具证据 -> 注入真实状态证据, 强制模型修正
			if verifyClaimEnabled() && verifyStrikes < maxVerifyStrikes && detectUnverifiedClaim(asst, messages) {
				verifyStrikes++
				messages = append(messages,
					ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoningBuf.String()},
					ChatMessage{Role: "user", Content: verifyInterventionMsg(runVerification())},
				)
				continue
			}
			a.history = append(a.history,
				ChatMessage{Role: "user", Content: userContent},
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
			// 虚报检测 gate (增强版, 无剑闭环): 完成态声称+无工具证据 -> 注入真实状态证据, 强制模型修正
			if verifyClaimEnabled() && verifyStrikes < maxVerifyStrikes && detectUnverifiedClaim(asst, messages) {
				verifyStrikes++
				messages = append(messages,
					ChatMessage{Role: "assistant", Content: asst, ReasoningContent: reasoningBuf.String()},
					ChatMessage{Role: "user", Content: verifyInterventionMsg(runVerification())},
				)
				continue
			}
			a.history = append(a.history,
				ChatMessage{Role: "user", Content: userContent},
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
					a.history = append(a.history, ChatMessage{Role: "user", Content: userContent})
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

			// Detect repeated calls (loop detection, 语义归一化哈希)
			callHash := hashCall(params.Code, params.Lang, params.Input)
			blocked, callCount := checkRepeatedCall(callHash, callHistory, maxRepeatedCalls)
			if blocked {
				messages = append(messages, ChatMessage{
					Role: "tool", ToolCallID: tc.ID,
					Content: repeatReminder(callCount, params.Code),
				})
				// 按"语义家族"计拦截(同一归一化哈希只计一次), 避免升级 pro 后
				// 的验证性重跑被连续计分误杀。拦截≠失败中止, 只是干预。
				if callHash != lastBlockHash {
					lastBlockHash = callHash
					loopStrikes++
					escalateOnLoop()
					fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⚠"), dim(fmt.Sprintf("重复调用拦截 %d/%d, 已要求换策略", loopStrikes, maxLoopStrikes)))
					if loopStrikes >= maxLoopStrikes {
						return finishStuck()
					}
				}
				continue
			}

			// Execute — display code with syntax highlighting (anti-hallucination)
			displayToolCode(params.Code, params.Lang)
			fmt.Fprintf(os.Stderr, "  %s %s\n", dim("⚙"), dim("执行中…"))
			toolStart := time.Now()
			logEvent(EvToolCalled, params.Lang, map[string]string{"code": truncateCN(params.Code, 300), "lang": params.Lang})
			// 把本次 run 的 ctx 交给 Forge: 第一次 Ctrl+C 即可中断正在执行的工具
			a.forge.SetRunCtx(runCtx)
			output, result, execErr := a.forge.Build(params.Code, params.Lang, params.Input)
			a.forge.SetRunCtx(nil)
			// 用户按 Ctrl+C 取消了本次 run: 立即结束, 不计为工具失败
			// (否则取消会被当成失败计分, 进而误判"连续失败中止")
			if cerr := runCtx.Err(); cerr != nil {
				fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⏹"), dim("已取消本次执行"))
				return cerr
			}
			toolDuration := time.Since(toolStart)
			lastRawOutput = output // 供无进展检测: 成功与失败的输出都算
			toolOK := execErr == nil && result != nil && result.OK && result.ExitCode == 0
			logEvent(EvToolResult, params.Lang, map[string]interface{}{"ok": toolOK, "duration_ms": toolDuration.Milliseconds()})

			// 失败判定含 ExitCode: "打印后非零退出"(OK=true 但退出码非0)是真实运行
			// 失败, 必须计分——否则模型反复执行同样失败的代码永不触发中止(旧漏洞)。
			if execErr != nil || (result != nil && (!result.OK || result.ExitCode != 0)) {
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
					a.history = append(a.history, ChatMessage{Role: "user", Content: userContent})
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
						if r := []rune(ol); len(r) > 120 {
							ol = string(r[:120]) + "..."
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
			// 大输出落盘引用: 超阈值(12000 rune)时头尾摘要+全文落盘, 否则走 pruneToolOutput
			warnedOutput := goalAnchorRef(output, taskAnchor, turn, a.cfg.WorkDir)

			messages = append(messages, ChatMessage{
				Role: "tool", ToolCallID: tc.ID, Content: warnedOutput,
			})
			// ── 工具结果识图接续器 (browser 截图落盘后模型需要"看到"图) ──
			// 工具执行产出图片文件(如 browser gate screenshot 落盘 .png)后, 模型拿到
			// 的是文本路径, 看不到图。复用 detectToolImages 从工具输出提取真实存在的
			// 图片 → base64 挂一条带 Images 的 user 辅助消息 → 切视觉模型。图片仅在
			// 当轮 messages 内存 (Images json:"-" 不落盘 history), 重放/缓存/压缩安全。
			toolImgs := detectToolImages(output, a.cfg.WorkDir)
			if len(toolImgs) > 0 && visionCapable(a.cfg) {
				imgMsg := ChatMessage{
					Role:    "user",
					Content: "工具结果包含图片文件, 已作为截图注入, 请结合截图进行分析:",
					Images:  toolImgs,
				}
				messages = append(messages, imgMsg)
				if a.cfg.ModelVision != "" {
					curModel = a.cfg.ModelVision
					a.stats.setModel(curModel)
				}
				fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.blue, "🖼"), dim(fmt.Sprintf("工具截图识图 %d 张 → %s", len(toolImgs), a.cfg.ModelVision)))
			}

		}

		// ── 无进展检测 (Detector B): 连续相同输出(不同代码但结果不变) → 拦截干预 ──
		// 拦截只是提示换策略 + 升级模型, 不中止任务; 连续多次仍无进展才自动收尾。
		if lastRawOutput != "" {
			outHash := hashOutput(lastRawOutput)
			if outHash == prevOutHash {
				sameOutRun++
			} else {
				sameOutRun = 1
				prevOutHash = outHash
			}
			if sameOutRun >= sameOutputThreshold {
				sameOutRun = 0 // 拦截后重新累计
				loopStrikes++
				escalateOnLoop()
				fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⚠"), dim(fmt.Sprintf("无进展拦截 %d/%d (输出连续%d次无变化), 已要求换策略", loopStrikes, maxLoopStrikes, sameOutputThreshold)))
				if loopStrikes >= maxLoopStrikes {
					return finishStuck()
				}
				// 干预指令以 user 消息注入 messages, 下一轮模型必须回应(换策略或总结)
				messages = append(messages, ChatMessage{Role: "user", Content: stuckInterventionMsg(loopStrikes)})
			}
		}

		// Trim accumulated turn messages: a long task can grow `messages` without
		// bound (assistant+tool pairs per turn). Drop the oldest COMPLETE turns so
		// the assistant(tool_calls) ↔ tool response pairing is never broken.
		// messages[0] (system / history head) is preserved.
		// Trim: 阈值从 60 提到 300 (缓存修复 20260826)。
		// 旧阈值 60 在工具密集任务(单轮多次 forge 调用)中极易触发, 而
		// trimTurnMessages 从头部删最早 tool 轮 → 固定头之后的前缀断裂 →
		// 后续所有 API 全量 miss (实证: cache hit 恒等于固定头长度)。
		// 单轮内 tool 轮几乎不可能超 300 次, 提高阈值从根本上避免触发。
		// 真正超长的历史由 trimHistory(尾部删, 缓存友好)与 maybeCompact 兜底。
		if len(messages) > 300 {
			messages = trimTurnMessages(messages, 300)
		}
	}

	// 回合数限制已取消：for{...} 永不因轮数退出, 任务可无限推进直到完成。
	// 防"死循环"的安全网:
	//   1. 循环拦截(Detector A/B) — 语义归一化重复调用 + 连续相同输出, 拦截 N 次
	//      (默认4)仍无进展 → 带已取得进展自动收尾, 不无限转;
	//   2. consecutiveFails — 连续铸剑炉调用失败中止 (默认5次, 非瞬态);
	//   3. unknownTools     — 未知工具幻觉中止;
	//   4. a.ctx.Done()     — 上下文取消 (Ctrl+C / 超时)。
	// 真正的任务永远跑到完成; 只有确认无进展/失败/幻觉/取消时才终止。
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
	renderer := newStreamRenderer()
	reasonRenderer := newReasoningRenderer()
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
				fmt.Fprint(os.Stderr, reasonRenderer.feed(ev.Content))
			}

		case "content":
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
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
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
			}
			fmt.Println()
			a.stats.addToken(tokenCount)
			return nil

		case "cache_usage":
			// 会话级实时缓存命中率累计 (状态栏数据源)
			a.stats.addCache(ev.CacheHit, ev.CacheMiss)

		case "done":
			// Flush any remaining renderer state
			fmt.Print(renderer.flush())
			if close := reasonRenderer.close(); close != "" {
				fmt.Fprint(os.Stderr, close)
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

// maybeCompact 历史压缩:
// 在 trimHistory 硬裁剪之前调用 (每 turn 至多一次)。触发条件:
//
//	① 开关开启 (AGENT_COMPACT_ENABLED, 默认1)
//	② 历史 token 估算 ≥ AGENT_COMPACT_TOKEN_THRESHOLD (默认30000)
//	③ 距上次压缩 ≥ AGENT_COMPACT_MIN_TURNS 轮 (冷却防抖, 防前缀频繁变化)
//
// 动作: 取 system 之后的最旧段 (前1/3, 夹在4~12条), 调 LLM 摘要成 ≤400 字,
//
//	以 【会话摘要】 特殊消息放头部 (system 后), 丢弃被压缩段。
//
// 失败兜底: 摘要出错/为空 → 不阻塞, 直接走原裁剪。
func (a *AgentRunner) maybeCompact() {
	if a.cfg == nil || !a.cfg.CompactEnabled {
		return
	}
	if a.compactCooldown > 0 {
		a.compactCooldown--
		return
	}
	if len(a.history) < 8 {
		return // 太短不值得压 (system + <7 条)
	}
	est := estimateTokens(a.history)
	if est < a.cfg.CompactTokenThreshold {
		return
	}
	// 最旧段: 固定头之后前 1/3, 夹在 4~12 条之间
	head := a.headLen // 跳过固定头 (system+记忆+折叠), v3.1 记忆不得被压缩掉
	if head < 1 {
		head = 1
	}
	rest := len(a.history) - head
	n := rest / 3
	if n < 4 {
		n = 4
	}
	if n > 12 {
		n = 12
	}
	if head+n >= len(a.history) {
		n = len(a.history) - head - 1
	}
	if n < 2 {
		return
	}
	// 配对安全选段 + system 前缀传给摘要器 (前缀缓存命中前置)
	start, end := completePairs(a.history, head, head+n)
	oldest := append([]ChatMessage{a.history[0]}, a.history[start:end]...) // [0]=system 保持前缀
	beforeTokens := estimateTokens(a.history)
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	summary, err := a.llm.Summarize(ctx, oldest, 400)
	if err != nil || strings.TrimSpace(summary) == "" {
		appendAuditLine(a, map[string]interface{}{
			"event": "compact_failed", "err": fmt.Sprint(err), "est_tokens": beforeTokens,
		})
		return // 降级: 不阻塞, 走原裁剪
	}
	summaryMsg := ChatMessage{Role: "user", Content: "【会话摘要】" + strings.TrimSpace(summary)}
	restMsgs := append([]ChatMessage{}, a.history[end:]...)
	newHist := make([]ChatMessage, 0, len(restMsgs)+head+1)
	newHist = append(newHist, a.history[:head]...) // 固定头整体保留 (缓存友好)
	newHist = append(newHist, summaryMsg)
	newHist = append(newHist, restMsgs...)
	afterTokens := estimateTokens(newHist)
	a.history = newHist
	a.compactCooldown = a.cfg.CompactMinTurns
	appendAuditLine(a, map[string]interface{}{
		"event":           "compact",
		"compressed_msgs": n,
		"summary_len":     utf8.RuneCountInString(summary),
		"before_tokens":   beforeTokens,
		"after_tokens":    afterTokens,
		"saved_tokens":    beforeTokens - afterTokens,
		"saved_pct":       fmt.Sprintf("%.1f%%", float64(beforeTokens-afterTokens)/float64(beforeTokens)*100),
	})
}

// appendAuditLine 追加一行 JSON 到 gate_audit.jsonl (agent 侧审计)。
// 与 forge 侧 auditGate 共用开关 FORGE_GATE_AUDIT=0; 写失败静默 (不阻断主流程)。
func appendAuditLine(a *AgentRunner, entry map[string]interface{}) {
	if os.Getenv("FORGE_GATE_AUDIT") == "0" {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	dir := a.cfg.WorkDir
	if dir == "" {
		dir = "."
	}
	fh, err := os.OpenFile(filepath.Join(dir, "gate_audit.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer fh.Close()
	fh.Write(append(data, '\n'))
}

// nswAudit 无剑埋点 (20260911): 让"触发几次 / 其中多少是空转"可度量。
// 此前 gate_audit.jsonl 20637 行里零条 nsw 记录 -> 触发率、空转率完全不可测,
// 谈扩语法只能盲扩(无法回答"木剑是在拦幻觉还是在自己制造开销")。
// corrected = 原文写错被纠正 (无剑的核心价值); completed = 原文只写算式未给结论。
// 被复述过滤掉的纯冗余只进 skipped 计数。
func nswAudit(a *AgentRunner, asst string, fresh []nswAnchor, total int) {
	if len(fresh) == 0 {
		return
	}
	corrected, completed := 0, 0
	exprs := make([]string, 0, 8)
	for _, an := range fresh {
		if nswClassifyAnchor(asst, an) == "corrected" {
			corrected++
		} else {
			completed++
		}
		if len(exprs) < 8 {
			exprs = append(exprs, an.expr+"="+an.val)
		}
	}
	appendAuditLine(a, map[string]interface{}{
		"event":     "nosword",
		"ts":        time.Now().Format(time.RFC3339Nano),
		"anchors":   total,
		"fresh":     len(fresh),
		"skipped":   total - len(fresh),
		"corrected": corrected,
		"completed": completed,
		"exprs":     exprs,
	})
}

func (a *AgentRunner) trimHistory() {
	a.maybeCompact() // 六西格玛立项20260815: 硬裁剪前先尝试摘要压缩 (背景保留+token节省)
	maxHist := a.cfg.MaxHistoryMessages
	if maxHist <= 0 {
		maxHist = 40
	}

	// 缓存友好裁剪: 从尾部成对删除完整轮次 [user, assistant],
	// 保持头部前缀 [system, u1, a1, ...] 稳定 —— DeepSeek 前缀缓存按 token 前缀
	// 完全匹配, 旧实现从头部删 history[1] 会使前缀在 system 之后断裂 → 后续请求
	// 全量 miss (实测: 前缀断裂命中率 0%)。
	headLen := a.headLen
	if headLen < 1 {
		headLen = 1 // 至少保护 system
	}
	for len(a.history) > maxHist && len(a.history) > headLen {
		n := len(a.history)
		if n <= headLen {
			break // 只剩固定头 (system+记忆+折叠), 不再删
		}
		// 尾部完整轮次 [user, assistant] 成对删
		if a.history[n-1].Role == "assistant" && n-2 >= headLen && a.history[n-2].Role == "user" {
			a.history = a.history[:n-2]
			continue
		}
		// 尾部孤立 user (失败/中止路径 append 的) 或异常形态: 删单条。
		// 孤立 user 前无配对 assistant, 删掉不破坏 API 的 user→assistant 配对。
		if n-1 >= headLen {
			a.history = a.history[:n-1]
		}
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
	// 循环检测用"语义等价"哈希: 先归一化(去注释/空白), 防模型每轮改个
	// 注释或换行就绕过重复检测。lang/input 保持原样参与哈希。
	h := sha256.New()
	h.Write([]byte(normalizeCode(code)))
	h.Write([]byte{0})
	h.Write([]byte(lang))
	h.Write([]byte{0})
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

// hashOutput 归一化输出哈希: 仅去首尾空白, 用于"连续 N 次输出无变化"的无进展检测。
func hashOutput(s string) string {
	h := sha256.New()
	h.Write([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h.Sum(nil))
}

// ─── Tool execution display (anti-hallucination) ───────────────

// displayToolCode prints the source code being executed with syntax highlighting.
// This lets the user verify that the tool was actually called, preventing LLM hallucination.

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
// same (semantically normalized) code has been executed more than maxRepeated
// times (loop detection). It returns the updated counter and whether the
// threshold was exceeded.
func checkRepeatedCall(callHash string, callHistory map[string]int, maxRepeated int) (bool, int) {
	callHistory[callHash]++
	return callHistory[callHash] > maxRepeated, callHistory[callHash]
}

// ─── 生命分形单元: buildStreamMessages ──────────────────────
// 每个 agent 生命周期都始于"装配消息":
//
//	输入 input ──→ 清理历史 ──→ 召回记忆 ──→ 挂识图 ──→ 输出 messages/userContent/images
//
// 与 RunStream 主循环共享同一"输入→处理→输出"模式 (自相似小循环, 分形层级的一级)。
func (a *AgentRunner) buildStreamMessages(input string) ([]ChatMessage, string, []ImagePart) {
	messages := make([]ChatMessage, len(a.history)+1)
	copy(messages, a.history)
	// 跨 user turn 清理 reasoning_content (deepseek-harness §1.3 规则3):
	// 见 stripCrossTurnReasoning 注释。仅清理无 tool_calls 的 assistant,
	// 工具循环内 (messages 局部变量) 不受影响。
	cleaned := stripCrossTurnReasoning(messages[:len(a.history)])
	copy(messages, cleaned)
	// v2.2: BM25 动态召回既往经验注入当前用户轮次(不进 system → 缓存前缀恒定)。
	// v3.0 缓存铁律 (deepseek-harness 回放逐字节一致): userContent 是"线上真实发送
	// 的用户轮次"。历史落盘必须原样存 userContent —— 若存 plain input, 下一轮回放
	// 的历史 [user(input)] 与上一轮线上 [user(recalled+input)] 首 token 即不同,
	// 导致 system 之后的全部历史前缀断裂 → 跨轮/跨重启全量 miss (实测: 命中恒等于
	// system 长度, 历史永不命中)。wire ≡ f(history) 后, 回放天然命中。
	userContent := input
	if a.cfg != nil {
		if recalled, _ := RecallMemory(a.cfg.WorkDir, input, 5); recalled != "" {
			userContent = recalled + "\n" + input
		}
	}
	userMsg := ChatMessage{Role: "user", Content: userContent}
	// ── 识图 (vision): 输入含图片路径 → 读文件 base64 挂当轮请求 ──
	// 图片只在当轮内存 (json:"-" 不落盘 history → 重放/压缩路径安全)。
	images, _ := detectImages(input, a.cfg.WorkDir)
	if len(images) > 0 {
		// 负能力门控 (DSH): 无视觉模型 (ModelVision 空) → 禁止挂图, 降级 text-only,
		// 避免把图发给非视觉模型被服务端 400。有视觉模型才挂图。
		if visionCapable(a.cfg) {
			userMsg.Images = images
			fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.blue, "🖼"), dim(fmt.Sprintf("识图 %d 张 → %s", len(images), a.cfg.ModelVision)))
		} else {
			fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "🖼"), dim(fmt.Sprintf("检出 %d 张图但未配置视觉模型, 已降级为纯文本", len(images))))
		}
	}
	messages[len(a.history)] = userMsg
	return messages, userContent, images
}
