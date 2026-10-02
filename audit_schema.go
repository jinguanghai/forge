package main

// audit_schema.go — 审计流 schema 单一数据源 (20260930)。
//
// 根因: gate_audit.jsonl 是"审计总账", 混装 7 类事件 (gate 调用 / gate 重试明细 /
// nosword 系列 / compact 系列)。"什么才算一条 gate 记录"此前由 4 个消费者各自实现,
// 实测同一份数据得出 3 个不同数字 (report_cn 28773 / scorecard.go 28967 /
// forge_watchdog 31895), 判据漂移且无单一数据源 —— 谁改了都无人发现。
//
// 修复三层:
//   ① 写入侧: gate 主记录显式带 event="gate", 消除"字段缺失即语义"的隐式约定
//      (缺失检测天然脆弱: gate_attempt 同时带 lang 与 event, 旧判据抓不到它)。
//   ② 本表: 登记全部合法 event 及其必填字段, 写入侧 / 读取侧 / 哨兵共用一份契约。
//   ③ 读取侧: 四消费者统一调用 auditIsGateEvent, 判据不再各写一份。

// auditSchemaEpoch schema 契约生效时刻 (RFC3339)。
//
// 存量记录 (含 20260930 迁移前旧进程写下的) 已由一次性脚本补 event="gate";
// 哨兵对 ts 早于本时刻的缺 event 记录按"过渡期遗留"计数提示, 不报红 ——
// 否则运行中的旧进程每写一条就把哨兵染红, 红牌会失效。
// ts 晚于本时刻仍缺 event = 新代码上线后仍写脏数据, 必须报红。
const auditSchemaEpoch = "2026-10-01T00:00:00+08:00"

// auditEventSpec 一类审计事件的契约。
type auditEventSpec struct {
	Name     string   // event 字段值; gate 主记录为 "gate"
	Required []string // 必填字段 (ts 由 audit_ts_sentinel_test.go 单独管, 此处不重复)
	Writer   string   // 唯一写入点 (文件:函数), 供 AST 哨兵核对
}

// auditEvents 全部合法审计事件 —— 单一数据源。
// 新增事件必须先在此登记, 否则 audit_schema_test.go 报红。
var auditEvents = []auditEventSpec{
	{
		Name:     "gate",
		Required: []string{"lang", "ok", "duration_ms", "code_len", "input_len", "retries"},
		Writer:   "forge_audit.go:auditGate",
	},
	{
		Name:     "gate_attempt",
		Required: []string{"lang", "attempt", "ms", "stage"},
		Writer:   "forge_audit.go:auditAttempt",
	},
	{
		Name:     "nosword",
		Required: []string{"anchors", "fresh", "skipped", "corrected", "completed", "exprs"},
		Writer:   "agent_audit.go:nswAudit",
	},
	{
		Name:     "nosword_probe",
		Required: []string{"enabled", "rounds", "anchors", "fresh", "source"},
		Writer:   "agent_audit.go:nswProbeAudit",
	},
	{
		Name: "nosword_expr",
		// saved 为后加字段(15 条历史记录无), 故不入必填 —— Required 只列
		// 事件诞生起即存在的字段, 否则历史数据会被判违规。
		Required: []string{"enabled", "marks", "rejected"},
		Writer:   "nosword_expr.go:nswExprAudit",
	},
	{
		Name:     "compact",
		Required: []string{"compressed_msgs", "summary_len", "before_tokens", "after_tokens", "saved_tokens", "saved_pct"},
		Writer:   "agent_trim.go:compactHistory",
	},
	{
		Name:     "compact_failed",
		Required: []string{"err", "est_tokens"},
		Writer:   "agent_trim.go:compactHistory",
	},
}

// auditEventNames 返回合法 event 名集合 (哨兵与消费者共用)。
func auditEventNames() map[string]bool {
	m := make(map[string]bool, len(auditEvents))
	for _, e := range auditEvents {
		m[e.Name] = true
	}
	return m
}

// auditEventRequired 返回某 event 的必填字段集合; 未登记返回 nil。
func auditEventRequired(name string) map[string]bool {
	for _, e := range auditEvents {
		if e.Name == name {
			m := make(map[string]bool, len(e.Required))
			for _, f := range e.Required {
				m[f] = true
			}
			return m
		}
	}
	return nil
}

// auditGateEvent 是 gate 主调用的 event 值。
const auditGateEvent = "gate"

// auditIsGateEvent 判定一条审计记录是否为 gate 主调用 —— 四消费者唯一判据。
//
// Python 侧 (forge_watchdog.py / report_cn.py / quality_report.py) 必须镜像
// 同一语义 (ev == "gate"), 由 audit_schema_test.go 的 TestAuditPythonConsumersMirrorGateJudgement 钉住。
func auditIsGateEvent(ev string) bool {
	return ev == auditGateEvent
}
