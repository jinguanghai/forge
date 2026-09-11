package main

// bench_test.go — 六期 DMAIC I2: 铸剑炉工具链能力基准
//
// 目的: 给铸剑炉一个"可重复的能力基线" (回应"没有评判标准")。
// 范围: 工具链确定性能力 (gate/编译器), 不调 LLM —— 免费、快、可重复。
// 用法: go test -run TestBench -v
// 输出: 成功率 / 总耗时 / 估算成本 / 每任务明细 → bench/report_YYYYMMDD.md
//
// 20 任务: 5 数学(math gate) + 5 文件(python) + 5 代码(go/python) + 5 逻辑/正则/编排

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// benchCase 一个基准任务
type benchCase struct {
	name  string
	cate  string
	lang  string
	code  string
	check func(stdout string) bool
}

// benchResult 单任务结果
type benchResult struct {
	name string
	cate string
	ok   bool
	ms   int64
	note string
}

func benchCases() []benchCase {
	return []benchCase{
		// ── 数学 (math gate: 确定性计算) ──
		{"2+2", "math", "math", "2+2", func(s string) bool { return strings.Contains(s, "4") }},
		{"3*(4+5)", "math", "math", "3*(4+5)", func(s string) bool { return strings.Contains(s, "27") }},
		{"2^10", "math", "math", "2^10", func(s string) bool { return strings.Contains(s, "1024") }},
		{"(7-2)*6", "math", "math", "(7-2)*6", func(s string) bool { return strings.Contains(s, "30") }},
		{"1/2+1/2", "math", "math", "1/2+1/2", func(s string) bool { return strings.Contains(s, "1") }},

		// ── 文件操作 (python) ──
		{"写读文件", "file", "python", "open('/tmp/bf.txt','w').write('ok')\nprint(open('/tmp/bf.txt').read())", func(s string) bool { return strings.Contains(s, "ok") }},
		{"列目录", "file", "python", "import os\nprint(len(os.listdir('.')) >= 0)", func(s string) bool { return strings.Contains(s, "True") }},
		{"建目录", "file", "python", "import os\nos.makedirs('/tmp/bf_dir', exist_ok=True)\nprint(os.path.isdir('/tmp/bf_dir'))", func(s string) bool { return strings.Contains(s, "True") }},
		{"JSON 解析", "file", "python", "import json\nd=json.loads('{\"a\":1}')\nprint(d['a'])", func(s string) bool { return strings.Contains(s, "1") }},
		{"统计行数", "file", "python", "open('/tmp/bf_l.txt','w').write('a\\nb\\nc\\n')\nprint(len(open('/tmp/bf_l.txt').readlines()))", func(s string) bool { return strings.Contains(s, "3") }},

		// ── 代码生成 (python/go) ──
		{"python hello", "code", "python", "print('hello')", func(s string) bool { return strings.Contains(s, "hello") }},
		{"python 求和", "code", "python", "print(sum(range(1,11)))", func(s string) bool { return strings.Contains(s, "55") }},
		{"python 反转", "code", "python", "print('abc'[::-1])", func(s string) bool { return strings.Contains(s, "cba") }},
		{"python 排序", "code", "python", "print(sorted([3,1,2]))", func(s string) bool { return strings.Contains(s, "[1, 2, 3]") }},
		{"go 编译运行", "code", "go", "package main\nimport \"fmt\"\nfunc main(){fmt.Println(21*2)}", func(s string) bool { return strings.Contains(s, "42") }},

		// ── 逻辑 / 正则 / 编排 ──
		{"logic sat", "logic", "logic", "a & b", func(s string) bool { return strings.Contains(s, "sat") }},
		{"logic unsat", "logic", "logic", "a & ~a", func(s string) bool { return strings.Contains(s, "unsat") || strings.Contains(s, "不可满足") }},
		{"regex 匹配", "regex", "regex", `{"type":"match","pattern":"[A-Z]\\d{3}","positive":["B456"]}`, func(s string) bool { return strings.Contains(s, "match") || strings.Contains(s, "true") }},
		{"regex 负例", "regex", "regex", `{"type":"match","pattern":"[A-Z]\\d{3}","positive":[],"negative":["B45"]}`, func(s string) bool { return strings.Contains(s, "pass") || strings.Contains(s, "true") }},
		{"chain 编排", "chain", "chain", `{"stages":[{"gate":"math","input":{"expr":"2+2"}}]}`, func(s string) bool { return strings.Contains(s, "4") }},
	}
}

// runBench 跑全部任务, 返回结果列表
func runBench(f *Forge, cases []benchCase) []benchResult {
	results := make([]benchResult, 0, len(cases))
	for _, c := range cases {
		start := time.Now()
		out, res, err := f.Build(c.code, c.lang, "")
		elapsed := time.Since(start)
		r := benchResult{name: c.name, cate: c.cate, ms: elapsed.Milliseconds()}
		if err != nil || res == nil || !res.OK {
			r.ok = false
			r.note = "gate 失败: " + firstLine(res, err)
		} else if c.check(out) {
			r.ok = true
		} else {
			r.ok = false
			r.note = "输出不符: " + firstLineOf(out)
		}
		results = append(results, r)
	}
	return results
}

func firstLine(res *ForgeGateResult, err error) string {
	if err != nil {
		return err.Error()[:min(60, len(err.Error()))]
	}
	if res != nil && res.Error != "" {
		return res.Error[:min(60, len(res.Error))]
	}
	return "unknown"
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// TestBench 基准入口: 输出报告到 bench/report_YYYYMMDD.md
func TestBench(t *testing.T) {
	wd := "." // 真实 workdir: gate 二进制在 ./.forge/forge-tools (t.TempDir 会找不到 gate)
	cfg := DefaultConfig()
	cfg.WorkDir = wd
	cfg.RetryMax = 0
	f := NewForge(wd, cfg)
	defer f.Shutdown()

	start := time.Now()
	results := runBench(f, benchCases())
	totalMs := time.Since(start).Milliseconds()

	// 统计
	pass := 0
	for _, r := range results {
		if r.ok {
			pass++
		}
	}
	total := len(results)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# 铸剑炉工具链基准报告 %s\n\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("**成功率: %d/%d (%.0f%%)** | 总耗时: %d ms\n\n", pass, total, float64(pass)*100/float64(total), totalMs))
	sb.WriteString("| 分类 | 任务 | 结果 | 耗时(ms) | 备注 |\n|---|---|---|---|---|\n")
	for _, r := range results {
		mark := "✅"
		if !r.ok {
			mark = "❌"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %s |\n", r.cate, r.name, mark, r.ms, r.note))
	}
	sb.WriteString("\n---\n说明: 本基准测工具链确定性能力 (gate/编译器), 不调 LLM, 可重复运行。\n")

	// 落盘
	dir := filepath.Join(".", "bench")
	if err := os.MkdirAll(dir, 0755); err == nil {
		report := filepath.Join(dir, "report_"+time.Now().Format("20060102")+".md")
		_ = os.WriteFile(report, []byte(sb.String()), 0644)
		t.Logf("报告已落盘: %s", report)
	}

	// 输出摘要到 stdout
	t.Logf("\n%s", sb.String())

	// 基准不过度断言: 允许环境性失败 (如 go 工具链慢), 但记录
	// 关键 gate (math/logic) 失败会打印, 不 fail 测试 (基准是报告不是门禁)
	if pass < total*6/10 {
		t.Logf("警告: 成功率低于 60%%, 请检查工具链状态")
	}
}
