package main

// gate_registry.go — gate 注册表 + 配置化组合
//
// 目的: 消除"新增/禁用 gate 需改两处(Go 代码 + system prompt 专家路由)"的耦合。
//   - 每个自托管 gate 在这里注册一份描述 (单点记录)
//   - 专家路由 prompt 由 describeGates() 从注册表动态生成 (不再手写)
//   - FORGE_GATES_ENABLED 配置可禁用 gate (空 = 全部启用)
//
// 范围: 只注册"自托管 gate"(math/logic/regex/knowledge/tcm/browser/self/chain);
// 普通编译器(go/python/sh/node)走 铸剑炉_COMPILERS, 不在此表。
// 已裁剪 eprover/repair/system/deno/rust/tcc (冗余或降级)。

import "strings"

// GateDef 一个自托管 gate 的注册信息
type GateDef struct {
	Name        string // lang 名 (与 forgeGateSelfHosted 的 case 一致)
	Description string // 专家路由 prompt 中的规则描述
}

// gateRegistry 全部自托管 gate (单点记录, 新增 gate 只需加一行)
// Description 即专家路由 prompt 规则 (动态生成, 与 FORGE_GATES_ENABLED 联动)
var gateRegistry = []GateDef{
	{
		Name:        "math",
		Description: "算数/统计/价格/比例/数值比较/等式推导/公式化简 → 必须实际计算 (lang=\"math\"), 禁止直接给数字; 代码输出作为答案依据",
	},
	{
		Name:        "logic",
		Description: "逻辑判断/条件推理/真伪/定理证明 → logic gate 验证 (z3 SAT)",
	},
	{
		Name:        "regex",
		Description: "正则验证 → regex gate; 语义是整串完全匹配(fullmatch)非包含匹配; 格式: 裸 pattern 或 JSON {\"type\":\"match\",\"pattern\":\"...\",\"positive\":[...],\"negative\":[...]}; flags 可用 i/m/s",
	},
	{
		Name:        "knowledge",
		Description: "知识/概念查询 → knowledge gate (SPARQL 查询)",
	},
	{
		Name:        "tcm",
		Description: "中医药/药对查询 → tcm gate (触发词: \"查药对 X Y\" / \"药对：A、B\" / \"配伍 A B\")",
	},
	{
		Name:        "browser",
		Description: "网页浏览/抓取 → browser gate (直连, 不走代理)",
	},
	{
		Name:        "self",
		Description: "源码自修改/热替换 → self gate (replace:旧:新 / append; 需主人审批, 自动快照; 编译通过后自动冒烟→就位 forge.exe, 冒烟失败自动回滚)",
	},
	{
		Name:        "chain",
		Description: "多 gate 顺序编排 → chain gate (lang=\"chain\", JSON stages 条件执行, 减少 LLM 往返)",
	},
}

// gateNames 返回全部注册 gate 名 (用于校验配置)
func gateNames() []string {
	out := make([]string, 0, len(gateRegistry))
	for _, g := range gateRegistry {
		out = append(out, g.Name)
	}
	return out
}

// gateEnabled 判断 gate 是否启用: enabled 列表为空 = 全部启用。
// 未知名字 (不在注册表) 视为启用 (向后兼容, 不误伤)。
func gateEnabled(name string, enabled []string) bool {
	if len(enabled) == 0 {
		return true
	}
	for _, e := range enabled {
		if e == name {
			return true
		}
	}
	// 不在注册表的 lang (如编译器) 不受配置影响
	for _, n := range gateNames() {
		if n == name {
			return false
		}
	}
	return true
}

// describeGates 动态生成专家路由 prompt 段 (只含启用项)。
// 返回的文本用于替换 system prompt 中手写的专家路由规则。
func describeGates(enabled []string) string {
	var sb strings.Builder
	for _, g := range gateRegistry {
		if !gateEnabled(g.Name, enabled) {
			continue
		}
		sb.WriteString("   - ")
		sb.WriteString(g.Description)
		sb.WriteString("\n")
	}
	return sb.String()
}
