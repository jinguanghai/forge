// ══════════════════════════════════════════════════════════════
// agent_memory.go — system 提示词与记忆锚点的装配 (稳定段/动态段分离)。
// 本文件承载 "LLM 每次请求看到什么" —— 前缀恒定机制的全部逻辑在此。
// ══════════════════════════════════════════════════════════════

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	if nswExprEnabled() {
		// 表达式化强制 (20260920 无剑二期): 固定约束段, 逐字节恒定 → 不打断前缀缓存
		sb.WriteString(nswExprConstraint)
	}
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
	// 剔除动态字段 (清单唯一真相源: anchor_guard.go dynamicMemoryFields)
	for _, f := range dynamicMemoryFields {
		delete(m, f)
	}
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
