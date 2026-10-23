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
//
//	56 -> 57 T53 元判据「豁免清单定位前缀」一条 (20261014): 摘掉前缀 ->
//	「登记了就当没看见」重新可行 (判据自身也要有判据)。
//
//	55 -> 56 T53 记忆预算臂接线一条 (20261014): preflightDeny 漏调 memoryBudgetDeny ->
//	判据退回「写了、测了、但不跑」(链接器 DCE 整棵剔除, 而单测全绿)。
//	这是判据的第三种形态: 不是「有判据」, 也不是「没判据」。
//
//	43 -> 44 MiniMax 思考分离一条 (20261008): 摘掉 reasoning_split 下发块 ->
//	MiniMax 思维链重新灌进正文(<think> 标签), 指令遵循生产组 0/6; 哨兵必须报红。
//
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
//	30 -> 31 描述数字可复现一条 (20261005 T1b): architecture 里的数字与载体脱钩;
//	31 -> 32 T9 时间戳合理性一条 (20261005): patch 的 last_updated 窗口前置校验被摘掉 ->
//	手写预填未来重新可写入 (该由死程序生成的值落在手写路径上); 同批把 floor 与 watermark
//	同步到 32 (floor 曾滞后 watermark 一条, 属「水位只升不降」的半修)。
//	32 -> 33 佐制方(害佐模式)一条 (20261005 主人定调: 治未病/害佐模式): 规则的「实测来源」
//	校验被摘掉 -> 无来源的规则(画出来的药)重新可入表, 佐制清单退化为装饰品。
//	33 -> 34 T13 仓库级字节卫生一条 (20261005): 只扫主包 .go 的盲区被补上后又摘掉
//	-> defense_system/*.py 与判据 json 的 CRLF 污染重新不可见 (污染当时从盲区漏过)。
//	34 -> 35 VPS 登录判据的「判据自己制造误报」一条 (20261005): 白名单为空的失效分支被摘掉
//	-> 「非白名单登录」重新变成逻辑必然, 主人自己的 29 次登录被报 high 真入侵。
//	缺陷形态特殊: 判据越「正常」误报越多, 既有测试不会因此变红 —— 只有
//	「空白名单不得产生 rogue」写成断言才拦得住。
//	35 -> 37 判据台账两条 (20261005 J5/J6/J7): ①台账哨兵的「未裁决即报红」被摘掉
//	-> 新增判据不登记维度也能全绿 (台账自身此前无判据 = 公理三 无判据=愿望);
//	②漂移维阈值的「变松即报红」被摘掉 -> 抬闸门(最便宜的规避)重新无声通过
//	(漂移维是唯一可动态的维度, 但「往更松必须过人」此前无判据)。
//	37 -> 38 T14 开源仓漂移一条 (20261006): 判据的「missing 即报红」被摘掉 ->
//	新增未同步的源码文件重新无声通过。实证: 上线前逐文件 sha256 比对发现 6 个
//	已提交源码文件未进开源仓, 而四套守卫全绿 —— 它们都不看发布面 (零判据的典型形态)。
//	38 -> 39 T17 写入端判据一条 (20261007): 状态文件写入端缺 newline="\n" 被摘掉 ->
//	Python text 模式在 Windows 做 os.linesep 转换, 每次 hourly 重造 CRLF 污染仓库文本面;
//	而当时判据只判「工作区现状」(文件被删/被忽略即静默变绿) —— 生成污染要等现状判据
//	扫到才报, 晚了。新判据直调写入函数(不经 CLI), 钉住写法本身; 并顺带钉住 CLI 链路
//	确实调用它 (状态文件必须产出);
//	39 -> 40 T23 子进程工作目录一条 (20261007): verifyClaim.cmd.Dir = workDir 被摘掉 ->
//	子命令在调用方进程 cwd 执行, 守卫(go vet / git) 输出的路径/状态错位 -> 证据链可被外部 cwd 操纵;
//	统一入口的判据只护住 newCmd, 漏掉裸 exec.Command 的旧调用点。包级结构哨兵
//	exec_workdir_sentinel_test.go 扫整个包, 新增裸调用点必报红; verify_claim_cwd_test.go 行为判据钉住修复本身。
//	40 -> 41 T24 P2 超时出路一条 (20261008): 超时回告的「改走长任务通道」引导被摘掉
//	-> 模型只剩「原地重写」这条出路(实测 461 条超时里 137 条跑满 3 次重试 ≈93s);
//	出路只写在 prompt 里属建议类约束, 实测无效, 必须由死程序写进反馈本身。
//	41 -> 42 T27 硬编码盘符哨兵一条 (20261008): 生产代码禁写死盘符的正则被改瞎
//	-> 13 处硬编码可重新写回而哨兵全绿 (Linux 端到端会再次跑不通)。
//	42 -> 43 「注意力剩余」分子口径一条 (20261008): contextBudgetUsed 的跨轮 reasoning 剥离被摘掉
//	-> 不发出去的思考段重新计入「已用」(真实会话实测虚增 4450/14789 = 30.1%,
//	显示「剩27%」而实际「剩49%」), 且 maybeCompact 共用同一函数 → 历史压缩被提前触发。
//	44 -> 48 T32 分形守卫四条 (20261008): F1 判定架空 / 扫描覆盖断言架空 /
//	元判据贴边检测架空 / 元判据恒真检测架空 —— 四条都是「判据失效但全绿」型缺陷的
//	变异样本(与 F1 折叠指纹恒真数月同型, 那次没有任何判据能发现)。
//	48 -> 51 故障兜底三条 (20261011 T31, MiniMax 由『高峰路由』改『故障兜底』):
//	①兜底接线被摘掉 -> DeepSeek 抖动/5xx 重试耗尽即整轮失败, 兜底端点配置形同虚设;
//	②主路径端点解析退回「配了 MiniMax 就切过去」-> 高峰重新自动切厂商 (每次贵 1.39~3.90 倍
//	且正文被思维链污染, 差异只在账单上可见, 无判据则无人察觉);
//	③识图请求的兜底豁免被摘掉 -> 图片请求被打到看不见图的端点, 拿回编造回复而调用方无从分辨。
//	54 (20261012): 三条新判据的鉴别力 —— 告警去重 / 豁免告警触发 / 预算口径 (HTML 转义)。
//	新增判据不带鉴别力证明 = 「判据存在」与「判据有效」是两回事 (本文件存在的理由)。
//	54 -> 55 SKIP 归因一条 (20261013): 分类恒判 SKIP_ENV -> 基线红被当环境抖动,
//	退出码不改/告警不写, 「判据从未被验证有鉴别力」重新静默; 判据 = mutation_probe_skip_test.go。
//	57 -> 61 T58 测试分层齐备三条 (20261016): 层成员数水位被置 0 (度量恒真) /
//	分层分类的 sentinel 分支被改瞎 / 活证据判定被架空 —— 三条都是「判据失效但全绿」型,
//	判据 = layer_guard_sentinel_test.go (层齐备哨兵, 七条判据 + 活证据自证)。
//	61 -> 63 T59 判据鉴别力两条 (20261017): 验证入口回退成裸 go test -> 动态扫描判据须报红 /
//	环形日志回退成粒度混用 -> 接线判据须报红。
//
//	65 -> 66 发版动作臂落盘一条 (20261017 T60-②): 摘掉 version_bump.py 的逐行
//	_LOG_F.flush() -> 进程被中断时日志落空文件 (20261016 实测裸 go test 落 0 字节),
//	「打开文件写」与「真的能留下证据」是两件事; 哨兵 version_bump_log_sentinel_test.go 须报红。
//	66 -> 68 F5 圈复杂度维两条 (20261017 T35 P1): 阈值被收到 5 (判据被放宽/收错方向都得红) /
//	CaseClause 口径反转 (度量退化而主守卫仍绿 —— 只有度量自检抓得到)。
//	68 -> 69 阈值棘轮一条 (20261018 T35 P2): 受管阈值被无声放宽(F2 500->600)而台账不记账 ->
//	棘轮对账须报红。为什么值得测: 阈值/水位的「只能改小」此前只是注释纪律, 改大后判据自己
//	被架空而全量测试仍绿 —— 判据 = fractal_ratchet_test.go (链严格递减 + 链尾==当前值 + 双向对账)。
//	69 -> 70 佐制方 --run 留痕接线一条 (20261023 T12 分母): 摘掉 append_run_log(rec) -> 留痕消失而
//	佐制方自身输出毫无变化 (rc 与报告都不变), 只有 side_effect_runlog_test.go 会响。
//	为什么值得测: 「判据在岗而分布不可得」是 T12 卡了半个月的真正原因 —— 分母必须在岗且必须被钉住。
const mutationWatermarkFloor = 70

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
