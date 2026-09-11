package main

// memory_sync_test.go — 五期 DMAIC I2: memory.json gate 字段同步工具
//
// 用法: go test -run TestSyncMemoryGates -v
// 作用: 用 SaveMemory (原子写 + .bak) 把 memory.json 的 gate 相关字段
//       从 18 面同步为 12 面。这是"牵一发动全身"的记忆层。
// 注意: 该测试会改写真实 memory.json —— SaveMemory 自动保留上一版为 .bak。

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestSyncMemoryGates(t *testing.T) {
	if os.Getenv("FORGE_SYNC_MEMORY") == "" {
		t.Skip("设置 FORGE_SYNC_MEMORY=1 才执行记忆同步")
	}
	wd := "."
	data, recovered, err := LoadMemory(wd)
	if err != nil {
		t.Fatalf("LoadMemory: %v", err)
	}
	if recovered {
		t.Log("注意: 从 .bak 恢复的数据")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("memory.json 解析失败: %v", err)
	}

	// 1. gates 字段: 18 → 12
	const newGates = "python/go/sh/node/math/logic/regex/knowledge/tcm/browser/chain/self"
	m["gates"] = newGates

	// 2. role / architecture 里的 "18"
	if role, ok := m["role"].(string); ok {
		m["role"] = strings.ReplaceAll(role, "18种Gate", "12种Gate")
	}
	if arch, ok := m["architecture"].(string); ok {
		m["architecture"] = strings.ReplaceAll(arch, "18gate", "12gate")
	}

	// 3. key_findings: 更新/删除 eprover 过时条目
	if rawKFs, ok := m["key_findings"].([]interface{}); ok {
		var out []interface{}
		for _, kf := range rawKFs {
			item, ok := kf.(map[string]interface{})
			if !ok {
				out = append(out, kf)
				continue
			}
			title, _ := item["title"].(string)
			switch {
			case strings.Contains(title, "gate实际行为"):
				item["title"] = "gate实际行为(与critical_rules不符)"
				item["content"] = "四期裁剪后保留: math/logic/regex/knowledge 由 gate 二进制判定; 拿不准省略 lang 走 python; eprover/repair/system/deno/rust/tcc 已移除"
				out = append(out, item)
			case strings.Contains(title, "eprover"):
				t.Logf("删除过时条目: %s", title) // eprover 已裁剪
			default:
				out = append(out, item)
			}
		}
		m["key_findings"] = out
	}

	// 4. self_governance: 防回潮纪律 (五期 C 阶段)
	if sg, ok := m["self_governance"].(map[string]interface{}); ok {
		sg["gate_discipline"] = "gate 面变更必须同步五处(五期): ①代码(COMPILERS/switch/自动检测) ②prompt(gate_registry+专家路由) ③记忆(memory.json gates字段) ④文档(README) ⑤发布包(dsh-forge-gates index.js/package.json); 连续30天零使用且无降级价值 → 列入裁剪候选"
		// 六期 I4: 缓存纪律 (实测 cache_stats 6220条: 总命中率87.5%, 前缀变更230次→命中率掉到57-69%)
		sg["cache_discipline"] = "DeepSeek 前缀缓存纪律(六期I4): 改 system prompt / memory 锚点 = 重置缓存, 新前缀首次请求全量 miss, 命中率需3天左右爬回95%+; 因此: ①集中改动(一天内做完大改) ②避免频繁改 memory 锚点字段 ③改完不要立刻评估命中率(等3天) ④实测: 稳定日98%+ vs 改动日57-69%, 平均87.5%"
	}

	// 5. 双 agent 互救 (互救手册): DSH 挂了由铸剑炉修
	m["rescue"] = "双agent互救: 完整手册 D:\\forge\\_RESCUE_双agent互救手册.md。DSH 救援: 启动 D:\\启动 dsh 网页版.bat 或 cd /d D:\\deepseek-harness && node apps\\cli\\lib\\bin.js web --port 3080; 未就绪看 dsh_server.log/startup_test*.log; 修复=pnpm run build → pnpm install → 杀3080占用; 验证=netstat :3080 LISTENING。铸剑炉自崩自救: rollback.cmd 回滚最新快照 → git revert auto 提交 → go build。铁律: 任一方升级前先确认另一方活着"

	nb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := SaveMemory(wd, nb); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	// 验证
	verify, _, err := LoadMemory(wd)
	if err != nil {
		t.Fatalf("verify load: %v", err)
	}
	var vm map[string]interface{}
	if err := json.Unmarshal(verify, &vm); err != nil {
		t.Fatalf("verify parse: %v", err)
	}
	if vm["gates"] != newGates {
		t.Fatalf("gates 未同步: %v", vm["gates"])
	}
	if role, _ := vm["role"].(string); strings.Contains(role, "18") {
		t.Fatalf("role 仍含 18: %s", role)
	}
	if arch, _ := vm["architecture"].(string); strings.Contains(arch, "18") {
		t.Fatalf("architecture 仍含 18: %s", arch)
	}
	for _, kf := range vm["key_findings"].([]interface{}) {
		item := kf.(map[string]interface{})
		if strings.Contains(item["title"].(string), "eprover") {
			t.Fatalf("key_findings 仍含 eprover: %s", item["title"])
		}
	}
	t.Log("✅ memory.json gate 字段已同步为 12 面 (旧版在 memory.json.bak)")
}
