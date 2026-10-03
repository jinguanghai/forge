package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// auditDiagLimit 审计里诊断字段 (diag) 的字节上限。
//
// 动机 (实测 20261003): diagnostics —— 结构化 JSON 或超时诊断文本 —— 此前只进
// 渲染层回灌给 LLM, 审计侧只有 snipErr(Error, 100) 的通用错误串。实测 862 条失败
// gate 记录里, 超时类只留下 "python execution failed: context deadline exceeded"
// (49 字符, 无卡点/无部分输出), 事后无法回答"它当时卡在哪"; 编译错误类靠 snipErr
// 的尾部保留侥幸留下 SyntaxError, 属偶然非设计。
// 512 字节 ≈ 5-8 条编译器诊断, 覆盖绝大多数失败; 超出则截断并记 diag_len 留痕,
// 消费者据此判断 diag 是否完整(截断后不再是合法 JSON, 只作摘要读)。
const auditDiagLimit = 512

// netEgressPatterns 出站网络特征表。
//
// 背景: DSec 论文用 eBPF 给每个沙箱做域名白名单 (pypi 放行 / npm 拒绝), 堵
// 「从非预期渠道拿答案」。铸剑炉全仓库零网络策略, 但也没有任何数据说明风险面
// 有多大 —— 硬上白名单等于凭想象造防御。故先埋点: 每次 gate 调用记下代码里
// 是否出现网络特征, 积累分布后再决定要不要建策略。
//
// 保守设计: 只匹配明确的导入/调用形态, 不做宽泛子串匹配(误报会污染决策数据)。
var netEgressPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"py-socket", regexp.MustCompile(`\b(import\s+socket|from\s+socket\s+import)`)},
	{"py-requests", regexp.MustCompile(`\b(import\s+requests|from\s+requests\s+import)`)},
	{"py-urllib", regexp.MustCompile(`\b(urllib\.request|urllib\.urlopen|from\s+urllib)`)},
	{"py-http", regexp.MustCompile(`\b(import\s+httpx|import\s+aiohttp|from\s+http\.client\s+import|import\s+http\.client)`)},
	{"node-net", regexp.MustCompile(`require\(['"](https?|net|dgram)['"]\)|from\s+['"]node:(https?|net)['"]|\bfetch\(`)},
	{"go-net", regexp.MustCompile(`net/http|net\.Dial|\bhttp\.Get\b`)},
	{"sh-net", regexp.MustCompile(`\b(curl|wget|ncat|ssh|scp|rsync)\b`)},
}

// auditWriteMu 串行化 gate_audit.jsonl 的追加写入。
// 审计文件只保留这一条写入路径 —— 此前 forge 侧用实例 auditMu、agent 侧完全无锁,
// 同一文件的同类写入两套规则 (结构上不一致: 改一处漏一处)。
var auditWriteMu sync.Mutex

// auditFilePath 返回审计文件 (gate_audit.jsonl) 的写入路径。
//
// 优先级: 有效工作目录 (非空且非 ".") → 用它; 否则回退 FORGE_AUDIT_PATH。
// 回退分支是给测试用的隔离出口: 测试进程的 WorkDir 常为 "" 或 "." (= 仓库根),
// 直接写入会把假数据混进生产审计日志 —— 实测一次全量测试注入 33 行
// (28 条 gate + 4 条 compact + 1 条 compact_failed), 直接污染
// "gate 失败率/耗时" 与 "压缩成功率" 的统计基础 (治理决策的度量输入)。
// 测试侧由 audit_isolation_test.go 的 TestMain 把该 env 指向临时目录。
func auditFilePath(dir string) string {
	// 测试进程防污染 (20260930): 目标目录指向"生产根"(当前工作目录=仓库根) 时,
	// 强制走隔离路径。此前隔离只覆盖"WorkDir 为空/点"的回退分支, 而跑真实 gate
	// 的测试 (TestForgeGateHost_*) 显式传仓库根, 绕过隔离把假记录写进生产审计 ——
	// 实测一次全量测试注入数十条带 event 的假记录 (含 approval_wait_ms 等构造值),
	// 直接污染 gate 失败率/耗时/重试率的统计基础。
	//
	// 判据收窄到"生产根"而非"一切路径": 用 t.TempDir() 的测试写自己的临时目录
	// 属合法行为, 一刀切会把它们全部打断 (实测 4 个用例红)。
	if inTestProcess() {
		if p := os.Getenv("FORGE_AUDIT_PATH"); p != "" && isProdRoot(dir) {
			return p
		}
	}
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

// netEgressHint 返回命中的网络特征名(逗号分隔), 无命中返回空串。
func netEgressHint(code string) string {
	var hits []string
	for _, p := range netEgressPatterns {
		if p.re.MatchString(code) {
			hits = append(hits, p.name)
		}
	}
	return strings.Join(hits, ",")
}

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

// appendAuditDiag 把诊断写入审计条目。
//
// 抽成单一实现供 auditGate / auditAttempt 共用: 两处各写一份 = 口径必然漂移
// (此处记 diag_len, 彼处不记), 且字段名会分叉。
// 空诊断不落字段 —— 否则字段恒真, "多少失败带定位信息"这个量就量不出来。
func appendAuditDiag(entry map[string]interface{}, diag string) {
	if diag == "" {
		return
	}
	entry["diag"] = snipErr(diag, auditDiagLimit)
	if len(diag) > auditDiagLimit {
		entry["diag_len"] = len(diag)
	}
}

// auditGate appends one JSON line per Build call to gate_audit.jsonl.
// Best-effort only: failures are silent so auditing never blocks the main path.
// Disable with FORGE_GATE_AUDIT=0.
func (f *Forge) auditGate(lang string, omitted bool, fallbackUsed bool, start time.Time, approvalWait time.Duration, codeLen, inputLen int, netHint string, res *ForgeGateResult) {
	if os.Getenv("FORGE_GATE_AUDIT") == "0" {
		return
	}
	entry := map[string]interface{}{
		// 显式 event: 消除"字段缺失即语义"的隐式约定 (audit_schema.go 单一数据源)。
		// 旧约定靠"没有 event 就是 gate"判定, 而 gate_attempt 同时带 lang 与 event,
		// 缺失检测抓不到它 —— 实测 scorecard 因此多收 194 条重试明细。
		"event":        auditGateEvent,
		"ts":           time.Now().Format(time.RFC3339Nano),
		"lang":         lang,
		"lang_omitted": omitted,
		"fallback":     fallbackUsed,
		"ok":           res != nil && res.OK,
		"duration_ms":  time.Since(start).Milliseconds(),
		"code_len":     codeLen,
		"input_len":    inputLen,
	}
	// A3 前向埋点: 出站网络特征。铸剑炉当前无网络策略, 先用数据量化风险面
	// (DSec 用 eBPF 按域名做 per-sandbox 白名单, 但那是 160 节点集群的解法)。
	if netHint != "" {
		entry["net_hint"] = netHint
	}
	// 仅在有审批等待(>=1ms)时落字段：其余记录保持原样，既有统计口径不受影响。
	if ms := approvalWait.Milliseconds(); ms > 0 {
		entry["approval_wait_ms"] = ms
	}
	// 贴边率领先指标 (P1-4a, 20261002): 净耗时 ≥ 预算 80% 即贴边。
	//
	// 为什么需要: 失败率/白耗/P95 全是滞后指标, 贴边是"下一次超时"的前兆 ——
	// 实测贴边区(20-30s)有 606 条且 100% 成功(全是侥幸), 而贴边率 W36 2.18%
	// → W40 5.12% 翻倍时, 失败率还没动。预算与执行同源(compilerTimeout),
	// 不另设阈值 —— 两套阈值必然漂移(审计说贴边而执行说没超)。
	// 缓存命中不算: 命中不消耗预算, 且 cache_hit 已单独标注。
	if res == nil || res.CachedAt <= 0 {
		if net := time.Since(start) - approvalWait; net > 0 {
			if budget := compilerTimeout(lang); budget > 0 && net*5 >= budget*4 {
				entry["near_budget"] = true
				entry["net_ms"] = net.Milliseconds()
			}
		}
	}
	if res != nil {
		entry["retries"] = res.Retries
		// 缓存命中此前在审计里不可见(命中与真跑都是 ok=true), 命中率无从度量。
		if res.CachedAt > 0 {
			entry["cache_hit"] = true
		}
		if res.OutputTruncated {
			entry["dropped_bytes"] = res.DroppedBytes
		}
		// 主动拒绝必须单独计数: 它是 ok=false, 会被 IFR-1「失败率」统计吞掉,
		// 而「输入被拒」与「闸门坏了」是两种完全不同的信号。
		if res.GateRejected {
			entry["gate_rejected"] = true
			reason := res.RejectReason
			if len(reason) > 100 {
				reason = reason[:100]
			}
			entry["reject_reason"] = reason
		}
		if !res.OK {
			entry["stage"] = res.Stage
			entry["err_snip"] = snipErr(res.Error, 100)
			// timeout 必须落在 gate 主事件流: 此前只有 gate_attempt 带该字段, 统计
			// 超时率须 join 两张表, 主审计流自身量不出超时(近 14 天 435/859 失败是
			// 超时, 却无法从主事件流直接聚合)。
			entry["timeout"] = res.Timeout
			// 诊断留证: err_snip 说"发生了什么", diag 说"错在哪一行" —— 二者并存
			// 而非替换。超时类 Error 是固定串, 只有 diag 带卡点与部分输出。
			appendAuditDiag(entry, res.Diagnostics)
		}
	}
	appendAuditJSONL(auditFilePath(f.workDir), entry)
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
	snip := snipErr(r.Error, 120)
	entry := map[string]interface{}{
		"event":   "gate_attempt",
		"ts":      time.Now().Format(time.RFC3339Nano),
		"lang":    lang,
		"attempt": n,
		"timeout": r.Timeout,
		"stage":   r.Stage,
		"ms":      r.Duration,
		"err":     snip,
	}
	// 诊断留证(同 auditGate): 首次失败的诊断只出现在这里 —— 主记录是最终结果,
	// 分不出"第一次错在哪、改完错在哪"(这正是 gate_attempt 存在的动机)。
	appendAuditDiag(entry, r.Diagnostics)
	appendAuditJSONL(auditFilePath(f.workDir), entry)
}

// snipErr 压缩错误文本, 保留「首行摘要 + 尾部诊断」而非单纯头部截断。
//
// 动机(20260927 实测): 编译器错误的前缀固定且冗长 ——
// "go build failed: # command-line-arguments" + 60 字符临时路径,
// 真正的诊断("main.go:123:9: undefined: foo")落在最后一行。
// 头部截断 120 字符会把诊断切成 "found 'impo" —— 留痕了, 但留的是无用部分,
// 根因不可分析。故头部只保留首行摘要(≤1/3 预算), 其余预算全给尾部,
// 并从行边界起切, 避免留下半行。
func snipErr(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	const sep = "..."
	head := s
	if i := strings.IndexByte(s, '\n'); i > 0 {
		head = s[:i]
	}
	if hb := limit / 3; len(head) > hb {
		head = safeHeadBytes(head, hb)
	}
	tb := limit - len(head) - len(sep)
	if tb <= 0 {
		return head
	}
	tail := safeTailBytes(s, tb)
	// 从行边界起, 避免以半行开头
	if i := strings.IndexByte(tail, '\n'); i >= 0 && i+1 < len(tail) {
		tail = tail[i+1:]
	}
	return head + sep + tail
}

// safeHeadBytes 取前 n 字节, 不切断 UTF-8 序列。
func safeHeadBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// safeTailBytes 取后 n 字节, 不切断 UTF-8 序列。
func safeTailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := len(s) - n
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return s[i:]
}

// inTestProcess 判定当前进程是否为 go test 二进制 (测试二进制会注册 -test.v flag,
// 生产二进制不会)。
func inTestProcess() bool {
	return flag.Lookup("test.v") != nil
}

// isProdRoot 判定 dir 是否指向生产根 (go test 的工作目录 = 包目录 = 仓库根)。
// 空串与 "." 在 auditFilePath 语义里同样落到仓库根, 故一并视为生产根。
func isProdRoot(dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(abs), filepath.Clean(wd))
}
