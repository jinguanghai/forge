package main

// mutation_probe_sentinel_test.go — 判据鉴别力清单哨兵 (20261003)
//
// 背景: mutation_probe.py 用受控变异证明"判据真的有鉴别力"(改坏生产代码 ->
// 判据必须报红)。但探针依赖一份清单 defense_system/mutation_manifest.json ——
// 清单被删/缩水/锚点腐化时, 探针只会静默少跑几条, 没有任何东西会响。
// 这正是"判据的判据"缺位的经典形态。
//
// 本哨兵把清单完整性固化为死程序判定 (公理三):
//   ① 文件必须存在且可解析 (fail-closed)
//   ② 条目数不得低于 watermark, watermark 不得低于硬下限 (只升不降)
//   ③ 每条必填字段齐全, id 不重复
//   ④ 每条 old 锚点在目标文件里必须恰好命中 1 次
//      (0 次 = 锚点已腐化, 探针会 ERR; >1 次 = 变异对象不唯一, 结果不可信)
//   ⑤ 每条 sentinel_tests 必须真实存在于对应哨兵文件 (防指向不存在的测试)
//   ⑥ 必须至少有一条 selftest=true (探针自检被删则探针失效无人发现)
//
// 与 hygiene 那套同构: 校验抽成纯函数, 便于喂畸形结构做数据变异自证 ——
// 判据必须能被测到, 否则它自己就是下一个盲区。

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const mutationManifestFile = "defense_system/mutation_manifest.json"

// mutationWatermarkFloor 水位硬下限: 只升不降。
// 新增鉴别力条目后, 把本常量与 manifest.watermark 一起上调。
// (20261003: 10 -> 12 netroute 红线两条; 12 -> 14 记忆写入护栏两条; 14 -> 15 preflight 入口收拢一条;
//
//	15 -> 18 guard_off 名实相符修复三条: 出口工具硬编码回归 / 跨语言动态字段漂移 / 配额豁免面;
//	18 -> 19 判据 json 监控面收口一条: axiom_carriers.json 未纳入 watchlist;
//	19 -> 20 自愈臂「删除也是演进」闸门一条: selfheal 对已提交的删除从不问 git
//	(同批修掉 snapshot_too_old 对丢失文件抛 FileNotFoundError 打断整个自愈循环);
//	20 -> 21 快照生成臂一条: 生成动作只挂人工按钮 -> 快照库必然腐坏
//	(判据看得见的「份数/体积」与真正重要的「抗体新鲜度」不是同一件事);
//	21 -> 23 P3-2 验收单一条 (真值外置: 改成读自产日志必报红) + P4 出口工具 src 标记一条
//	(cmd_patch 记 save: 合规写入不得与「手工绕过」同标记);
//	23 -> 24 验收单语言盲区一条: schtasks 结果解析只认英文标签 -> 该项恒 skip
//	(「永远跳过的检查项」等于没有检查项, 20261005 在 task gate 下实测);
//	24 -> 26 埋点消费者两条 (20261004): Consumer 声明被清空 / 报告不再消费某类事件
//	(埋点 → 消费者 → 度量可见 只做第一层 = 「可测但没人测」, 实测上线当天报告里零数字);
//	26 -> 27 规则↔载体清单腐烂一条 (20261004): prompt 加规则而清单没跟上
//	(清单是「哪些规则已下沉」的唯一答案, 它腐烂 = 漏项重新变成不可见);
//	27 -> 29 llm_sanitize 双缺陷两条 (20261004): 两侧 id 各自随机 -> 真实工具结果被
//	换成占位符(静默数据丢失); 无 index 端点的 arguments 续片被 continue 丢弃
//	(旧用例全绿而两处分支零覆盖 —— 行覆盖了、形态没覆盖))
//	29 -> 30 变异探针轮转算法漂移一条 (20261004): Go 侧复刻退化成 +1 步进 ->
//	哨兵会一直绿着验证一个不存在的实现 (轮转覆盖面判据随之失去意义);
//	同批把探针从「手动可跑」接进 hourly (--apply --rotate 1) + 还原终检判据。
const mutationWatermarkFloor = 30

type mutationTarget struct {
	ID            string   `json:"id"`
	Why           string   `json:"why"`
	File          string   `json:"file"`
	Sentinel      string   `json:"sentinel"`
	Run           string   `json:"run"`
	SentinelTests []string `json:"sentinel_tests"`
	Old           string   `json:"old"`
	New           string   `json:"new"`
	Selftest      bool     `json:"selftest"`
}

type mutationManifest struct {
	Version   int              `json:"version"`
	Watermark int              `json:"watermark"`
	Targets   []mutationTarget `json:"targets"`
}

var mutationTestFuncRe = regexp.MustCompile(`func (Test\w+)\(`)

func mutationTestFuncNames(src string) []string {
	var out []string
	for _, mm := range mutationTestFuncRe.FindAllStringSubmatch(src, -1) {
		out = append(out, mm[1])
	}
	return out
}

func mutationHasStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// mutationManifestProblem 校验清单结构 + 锚点有效性, 返回问题描述 (空串 = 可用)。
// files: 文件名 -> 内容, 由调用方注入 (纯函数: 便于喂畸形样本, 不依赖磁盘状态)。
func mutationManifestProblem(m mutationManifest, files map[string]string) string {
	var bad []string
	if m.Version <= 0 {
		bad = append(bad, "version 缺失")
	}
	if m.Watermark < mutationWatermarkFloor {
		bad = append(bad, fmt.Sprintf("watermark %d 低于硬下限 %d (水位只升不降)",
			m.Watermark, mutationWatermarkFloor))
	}
	if len(m.Targets) == 0 {
		bad = append(bad, "targets 为空")
	}
	if len(m.Targets) < m.Watermark {
		bad = append(bad, fmt.Sprintf("条目数 %d 低于水位 %d (清单被缩水)",
			len(m.Targets), m.Watermark))
	}
	selftest := 0
	seen := map[string]bool{}
	for i, tg := range m.Targets {
		tag := tg.ID
		if tag == "" {
			tag = fmt.Sprintf("targets[%d]", i)
			bad = append(bad, tag+": 缺 id")
		} else if seen[tg.ID] {
			bad = append(bad, tag+": id 重复")
		}
		seen[tg.ID] = true
		if tg.Why == "" {
			bad = append(bad, tag+": 缺 why (说不出为什么值得测)")
		}
		if tg.Selftest {
			selftest++
		}
		if tg.File == "" || tg.Old == "" || tg.New == "" {
			bad = append(bad, tag+": 缺 file/old/new")
			continue
		}
		if tg.Old == tg.New {
			bad = append(bad, tag+": old 与 new 相同 (变异体无效果)")
		}
		content, ok := files[tg.File]
		if !ok {
			bad = append(bad, tag+": 目标文件不存在 "+tg.File)
		} else if n := strings.Count(content, tg.Old); n != 1 {
			bad = append(bad, fmt.Sprintf("%s: 锚点在 %s 命中 %d 次 (必须恰好 1 次)",
				tag, tg.File, n))
		}
		if tg.Sentinel == "" || strings.HasPrefix(tg.Sentinel, "(") {
			continue // selftest 条目的哨兵是占位符, 无对应文件
		}
		sc, ok := files[tg.Sentinel]
		if !ok {
			bad = append(bad, tag+": 哨兵文件不存在 "+tg.Sentinel)
			continue
		}
		names := mutationTestFuncNames(sc)
		for _, want := range tg.SentinelTests {
			if !mutationHasStr(names, want) {
				bad = append(bad, tag+": run 指向不存在的测试 "+want)
			}
		}
		// ⑦ 双向覆盖 (20261003 补): 哨兵文件里的每个用例都必须在 run 里。
		// 只查单向(清单 ⊆ 文件)抓不到"新加了用例却没登记" —— 实测教训: netroute_test.go
		// 的 3 个端到端用例从未进 run, 于是"把红线拒绝分支打穿"的变异被判为仍绿(FAIL),
		// 而清单哨兵全绿。覆盖面漏登记 = 那条臂根本没被测到。
		for _, name := range names {
			if !mutationHasStr(tg.SentinelTests, name) {
				bad = append(bad, fmt.Sprintf("%s: 哨兵 %s 的用例 %s 未列入 run (覆盖面漏登记)",
					tag, tg.Sentinel, name))
			}
		}
	}
	if selftest == 0 {
		bad = append(bad, "无 selftest 条目 —— 探针自身失效将无人发现")
	}
	if len(bad) == 0 {
		return ""
	}
	sort.Strings(bad)
	return strings.Join(bad, "; ")
}

func loadMutationManifest(t *testing.T) mutationManifest {
	t.Helper()
	raw, err := os.ReadFile(mutationManifestFile)
	if err != nil {
		t.Fatalf("鉴别力清单读取失败 (fail-closed, 不放行): %v", err)
	}
	var m mutationManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("鉴别力清单解析失败: %v", err)
	}
	return m
}

func mutationFilesFor(m mutationManifest) map[string]string {
	files := map[string]string{}
	for _, tg := range m.Targets {
		for _, f := range []string{tg.File, tg.Sentinel} {
			if f == "" || strings.HasPrefix(f, "(") {
				continue
			}
			if _, ok := files[f]; ok {
				continue
			}
			if b, err := os.ReadFile(f); err == nil {
				files[f] = string(b)
			}
		}
	}
	return files
}

// TestMutationManifestWellFormed 哨兵: 真实清单必须可用。
func TestMutationManifestWellFormed(t *testing.T) {
	m := loadMutationManifest(t)
	if p := mutationManifestProblem(m, mutationFilesFor(m)); p != "" {
		t.Fatalf("鉴别力清单不可用 (fail-closed): %s", p)
	}
	t.Logf("鉴别力清单: %d 条 (水位 %d), 锚点全部唯一命中", len(m.Targets), m.Watermark)
}

// TestMutationManifestFailClosed 哨兵: 清单结构残缺时必须被拦下。
// 缺字段的危险不在「报错」而在「不报错」: 少一条 targets, 探针就少跑一次变异,
// 而没有任何东西会响 —— 这正是本哨兵要防的病。
func TestMutationManifestFailClosed(t *testing.T) {
	full := loadMutationManifest(t)
	files := mutationFilesFor(full)
	if p := mutationManifestProblem(full, files); p != "" {
		t.Fatalf("真实清单被判为不可用: %s", p)
	}
	clone := func() (mutationManifest, map[string]string) {
		mm := full
		mm.Targets = make([]mutationTarget, len(full.Targets))
		for i, tg := range full.Targets {
			tg.SentinelTests = append([]string{}, tg.SentinelTests...)
			mm.Targets[i] = tg
		}
		ff := map[string]string{}
		for k, v := range files {
			ff[k] = v
		}
		return mm, ff
	}
	cases := []struct {
		name   string
		mutate func(m *mutationManifest, f map[string]string)
	}{
		{"targets 清空", func(m *mutationManifest, f map[string]string) { m.Targets = nil }},
		{"条目数跌破水位", func(m *mutationManifest, f map[string]string) { m.Watermark = len(m.Targets) + 1 }},
		{"水位跌破硬下限", func(m *mutationManifest, f map[string]string) { m.Watermark = mutationWatermarkFloor - 1 }},
		{"version 缺失", func(m *mutationManifest, f map[string]string) { m.Version = 0 }},
		{"selftest 标记被清", func(m *mutationManifest, f map[string]string) {
			for i := range m.Targets {
				m.Targets[i].Selftest = false
			}
		}},
		{"锚点腐化 (old 加一个字符)", func(m *mutationManifest, f map[string]string) {
			m.Targets[0].Old = m.Targets[0].Old + "X"
		}},
		{"锚点不唯一", func(m *mutationManifest, f map[string]string) {
			m.Targets[0].Old = "func "
		}},
		{"old 与 new 相同", func(m *mutationManifest, f map[string]string) {
			m.Targets[0].New = m.Targets[0].Old
		}},
		{"id 重复", func(m *mutationManifest, f map[string]string) {
			m.Targets[1].ID = m.Targets[0].ID
		}},
		{"缺 why", func(m *mutationManifest, f map[string]string) { m.Targets[0].Why = "" }},
		{"run 指向不存在的测试", func(m *mutationManifest, f map[string]string) {
			m.Targets[0].SentinelTests = append(m.Targets[0].SentinelTests, "TestNoSuchThing2099")
		}},
		{"哨兵文件不存在", func(m *mutationManifest, f map[string]string) {
			m.Targets[0].Sentinel = "no_such_sentinel_2099_test.go"
		}},
	}
	for _, c := range cases {
		mm, ff := clone()
		c.mutate(&mm, ff)
		if p := mutationManifestProblem(mm, ff); p == "" {
			t.Errorf("fail-closed 失效: %s 未被检出", c.name)
		}
	}
}
