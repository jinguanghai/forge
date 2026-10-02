package main

// ============================================================================
// hygiene_forge_test.go —— .forge/ 一级项清单哨兵 (5S/1S 整理 · 2S 定置管理)
//
// 为什么需要 (20260928 实测):
//   .forge/ 是每次会话都在写的目录 (流量最大), 但四套守卫全部只判根目录一级 ——
//   .forge/ 下 32 项 / 238MB 没有任何判据看得见。流量最大的目录反而不设边界。
//
//   根因不是「哨兵不递归」, 而是「递归了也没法判」:
//   四类东西混在一个平面 —— state(运行时活跃) / backup(有时效) / doc(永久) /
//   temp(待处理), 没有分类就没有判据, 只能全都放过。
//
//   所以本清单的核心字段是 category, 每个一级项必须声明「它是什么 + 谁产生的」。
//   有归属的项不会失控 (反证: checkpoints/ 有 pruneCheckpoints 轮转, 实测正常);
//   失控的都是无归属的 (regress_* / commit_msg_b.txt / fractal_baseline.json.bak)。
//
// 与根目录哨兵的分工:
//   根目录哨兵管「允许存在什么」(二值判据);
//   .forge 哨兵管「每一项属于哪类 + 谁产生」(分类判据) —— 判据形态不同, 故分列。
//
// 硬/软两档 (关键设计):
//   硬 (rc=1): 未登记 / kind 不符 / 空壳目录未豁免 / patterns 超 keep
//   软 (rc=0): backup|temp 类超 review_days 天 -> 该看一眼, 但不是违规
//   混在一起报红 = 狼来了, 红牌会失效。
//
// 单一数据源: defense_system/hygiene_forge_manifest.json,
// 与 defense_system/hygiene_forge_check.py 共用 (两处各写一份必然腐化)。
//
// 只读不移动: .forge/ 下的项**有程序正在读写** (events.jsonl / checkpoint.json),
// 移动风险高于根目录 —— 与「目录只贴牌不隔离」同一理由。
// ============================================================================

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const forgeManifestFile = "defense_system/hygiene_forge_manifest.json"

type forgeEntry struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Category string `json:"category"`
	Owner    string `json:"owner"`
	Note     string `json:"note"`
}

type forgePattern struct {
	Glob     string `json:"glob"`
	Kind     string `json:"kind"`
	Category string `json:"category"`
	Keep     int    `json:"keep"`
	Note     string `json:"note"`
}

type forgeVector struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Empty       bool   `json:"empty"`
	ExpectOK    bool   `json:"expect_ok"`
	WhyContains string `json:"why_contains"`
	Note        string `json:"note"`
}

type forgeManifest struct {
	Version        int            `json:"version"`
	ReviewDays     int            `json:"review_days"`
	Entries        []forgeEntry   `json:"entries"`
	Patterns       []forgePattern `json:"patterns"`
	AllowEmptyDirs []string       `json:"allow_empty_dirs"`
	Vectors        []forgeVector  `json:"vectors"`
	Subdirs        []forgeSubdir  `json:"subdirs"`
}

// forgeSubdir 是 .forge 下某个子目录的内部清单 (20260928 补)。
//
// 根清单只登记了 forge-tools 这一层 —— 内部 19 项 / 77.8MB 无任何判据。
// 实测后果: tcc_gate_src/(死代码 11.23MB) 与 chain_gate.exe(真引用 0 处, 2.31MB)
// 在该目录躺了数月, 四套守卫全部看不见。判据形态与根层同构, 复用同一套判定。
type forgeSubdir struct {
	Path           string         `json:"path"`
	Doc            string         `json:"doc"`
	Entries        []forgeEntry   `json:"entries"`
	Patterns       []forgePattern `json:"patterns"`
	AllowEmptyDirs []string       `json:"allow_empty_dirs"`
	// AllowEmptySelf: 容器自身允许为空 (如 .forge/updates 仅在自改时写入一条审计,
	// 平时为空属正常)。与「路径写错」必须区分 —— 把真实空态误报为悬空,
	// 会让人干脆关掉整条判据, 那才是真的失去防线。
	AllowEmptySelf bool `json:"allow_empty_self"`
}

// forgeItem 是 .forge/ 下的一个一级项。
type forgeItem struct {
	Name  string
	IsDir bool
	Empty bool
}

// forgeCategories 允许的分类 —— 白名单式枚举, 防「随手写个新分类」让判据失去意义。
var forgeCategories = []string{"state", "backup", "doc", "temp", "tool"}

func loadForgeManifest(t *testing.T) forgeManifest {
	t.Helper()
	raw, err := os.ReadFile(forgeManifestFile)
	if err != nil {
		t.Fatalf("清单读取失败 (fail-closed, 不放行): %v", err)
	}
	var m forgeManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("清单解析失败: %v", err)
	}
	if p := forgeManifestProblem(m); p != "" {
		t.Fatalf("清单不可用 (fail-closed, 不放行): %s", p)
	}
	return m
}

// forgeManifestProblem 校验清单结构, 返回问题描述 (空串 = 可用)。
// 抽成纯函数: fail-closed 本身也是判据, 判据必须能被测到 —— 直接写在 load 里
// 就只能靠「改坏真文件」来验证, 既危险又测不全。
func forgeManifestProblem(m forgeManifest) string {
	var missing []string
	if len(m.Entries) == 0 {
		missing = append(missing, "entries")
	}
	if len(m.Patterns) == 0 {
		missing = append(missing, "patterns")
	}
	if len(m.Vectors) < 8 {
		missing = append(missing, "vectors(<8 条, 覆盖不足)")
	}
	if m.ReviewDays <= 0 {
		missing = append(missing, "review_days")
	}
	// subdirs: 子目录内部判据 (20260928 补)。缺则 forge-tools 内部 19 项重回盲区
	// —— 该目录实测躺过 11.23MB 死代码 + 2.31MB 孤儿, 数月无人发现。
	if len(m.Subdirs) == 0 {
		missing = append(missing, "subdirs(缺则子目录内部无判据)")
	}
	for i, sd := range m.Subdirs {
		if sd.Path == "" {
			missing = append(missing, fmt.Sprintf("subdirs[%d].path", i))
		}
		// entries 与 patterns 至少一个非空: 轮转产物 (备份/快照/缓存) 用 patterns
		// 登记是合法形态, 不能强制 entries —— 但这类容器仍必须至少有判据, 否则重回盲区。
		if len(sd.Entries) == 0 && len(sd.Patterns) == 0 {
			missing = append(missing, fmt.Sprintf("subdirs[%d].entries/patterns(至少一个, 否则该容器无判据)", i))
		}
	}
	if len(missing) > 0 {
		return "缺少必需字段 " + strings.Join(missing, ", ") +
			" —— 缺字段会让判定失去对照, 静默变成「全部放行」或「全部违规」"
	}
	return ""
}

func forgeItems(t *testing.T) []forgeItem {
	t.Helper()
	entries, err := os.ReadDir(".forge")
	if err != nil {
		t.Fatalf("读取 .forge 失败 (fail-closed): %v", err)
	}
	var out []forgeItem
	for _, e := range entries {
		it := forgeItem{Name: e.Name(), IsDir: e.IsDir()}
		if it.IsDir {
			sub, err := os.ReadDir(filepath.Join(".forge", it.Name))
			if err == nil {
				it.Empty = len(sub) == 0
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func forgeInList(name string, list []string) bool {
	for _, x := range list {
		if x == name {
			return true
		}
	}
	return false
}

func forgeKindOf(isDir bool) string {
	if isDir {
		return "dir"
	}
	return "file"
}

// forgeVerdict 判定单个一级项: (是否合规, 归类说明)。
// 优先级: entries(精确) > patterns(glob) > 未登记。
// 与根目录哨兵的优先级链同理: 精确条目必须先于 glob, 否则已登记项被宽 glob
// 抢先命中后, 其 kind 校验与空壳判定就永远走不到。
func forgeVerdict(name string, isDir, empty bool, m forgeManifest) (bool, string) {
	want := forgeKindOf(isDir)
	for _, e := range m.Entries {
		if e.Name != name {
			continue
		}
		if e.Kind != want {
			return false, "kind 不符: 登记为 " + e.Kind + ", 实为 " + want +
				" —— 清单腐化 (owner=" + e.Owner + ")"
		}
		if isDir && empty && !forgeInList(name, m.AllowEmptyDirs) {
			return false, "空壳目录 (0 项) —— 东西拿走了壳没删; 确需保留请加 allow_empty_dirs"
		}
		// 豁免分支单独打标: 否则「允许」的原因与「已登记且非空」无法区分,
		// 排查时分不清是「本来就有内容」还是「空着被豁免」。
		if isDir && empty {
			return true, "empty-ok:" + e.Category
		}
		return true, e.Category
	}
	for _, p := range m.Patterns {
		if ok, _ := filepath.Match(p.Glob, name); ok {
			if p.Kind != want {
				return false, "kind 不符: pattern " + p.Glob + " 登记为 " + p.Kind + ", 实为 " + want
			}
			return true, "pattern:" + p.Glob
		}
	}
	return false, "未登记 —— 2S 定置管理: .forge 下每个项必须有位置 " +
		"(state/backup/doc/temp/tool 五类之一; 确属项目本体请加 entries, 需 git 可见)"
}

// forgeStaleEntries 判定「悬置条目」: entries 里登记了, 但 .forge/ 里并不存在。
//
// 与未登记检测互为反向 —— 前者防「有东西没位置」, 后者防「有位置没东西」。
// 悬置的危险不是「多一行」, 而是判据腐化: 条目指向不存在的项之后, 同类项下次
// 出现时会被判为「已登记」而放过 (归档时忘删条目正是这条路径, 实测无哨兵)。
//
// 只收名字集合而非 []forgeItem, 便于用内存构造的清单直接测判据本身 ——
// 与 forgeManifestProblem 抽纯函数同理: 判据必须能被测到, 不能只靠改真文件。
func forgeStaleEntries(names []string, m forgeManifest) []string {
	var out []string
	for _, e := range m.Entries {
		if forgeInList(e.Name, names) {
			continue
		}
		// 轮转产物走 patterns, 不要求名字恰好存在。正常不会走到这里
		// (TestHygieneForgeNoDuplicate 已禁止 entries 与 patterns 重叠),
		// 此处是防御性冗余: 即使那条检查被误删, 也不会误报。
		covered := false
		for _, p := range m.Patterns {
			if ok, _ := filepath.Match(p.Glob, e.Name); ok {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, e.Name)
		}
	}
	return out
}

// TestHygieneForgeManifest 哨兵: .forge/ 不得出现未登记的一级项。
func TestHygieneForgeManifest(t *testing.T) {
	m := loadForgeManifest(t)
	items := forgeItems(t)
	if len(items) == 0 {
		t.Fatal(".forge 未发现任何一级项, 哨兵失效")
	}

	// 自检: 清单必须真的匹配到东西 (防路径写错 / 清单被清空后静默失效)
	matched := 0
	for _, it := range items {
		if ok, _ := forgeVerdict(it.Name, it.IsDir, it.Empty, m); ok {
			matched++
		}
	}
	if matched < 20 {
		t.Fatalf("清单仅匹配到 %d/%d 项, 疑似失效 (检查 %s 与测试 cwd)",
			matched, len(items), forgeManifestFile)
	}

	var bad []string
	for _, it := range items {
		ok, why := forgeVerdict(it.Name, it.IsDir, it.Empty, m)
		if ok {
			continue
		}
		tag := ""
		if it.IsDir {
			tag = " [目录]"
			if it.Empty {
				tag = " [目录·空壳]"
			}
		}
		bad = append(bad, fmt.Sprintf("%s%s  [%s]", it.Name, tag, why))
	}

	// 反向判据: 悬置条目。两类问题一起报, 免得修完一轮又冒一轮。
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	stale := forgeStaleEntries(names, m)
	if len(stale) > 0 {
		t.Errorf("清单存在悬置条目 %d 条 —— 登记了但 .forge/ 里不存在 (判据腐化):\n  %s\n"+
			"处理: 已归档/删除 → 删掉对应 entries 条目 ; 确属按需创建 → 写明创建条件或移出 entries ; 清单: %s",
			len(stale), strings.Join(stale, "\n  "), forgeManifestFile)
	}
	if len(bad) > 0 {
		t.Errorf(".forge 出现未登记/异常项 %d 个 (5S/1S 整理):\n  %s\n"+
			"处理: 临时产物 → .forge-temp/ ; 历史资产 → _archive/ ; "+
			"轮转产物 → 加 patterns 条目 ; 确属运行状态 → 加 entries %s (需 git 可见)",
			len(bad), strings.Join(bad, "\n  "), forgeManifestFile)
	}
	if len(bad) > 0 || len(stale) > 0 {
		// 有任一违规时不打印「全部已登记」—— 否则日志自相矛盾, 误导排查。
		// (悬置曾漏判: stale 走独立 t.Errorf 不 return, 实测悬置时日志仍打印
		//  「N 个一级项, 全部已登记」。)
		return
	}
	t.Logf(".forge %d 个一级项, 全部已登记", len(items))
}

// TestHygieneForgePatternKeep 哨兵: 轮转产物的份数不得超过各自声明的上限。
// 与根目录 quota_globs 同理 —— 轮转产物的名字带时间戳, 无法逐个登记,
// 只能声明「这一类最多留几份」。
func TestHygieneForgePatternKeep(t *testing.T) {
	// 判定实现抽在 hygiene_forge_subdir_test.go 的 forgeKeepViolations —— 根层与
	// 子目录必须共用同一实现 (20260930 实测缺口: 原先此处独有一份, 子目录侧根本
	// 不存在, 于是 .forge/backups 内 forge.exe.* 4 份 > keep=3 时 Go 侧全绿而
	// Python 侧报红 —— 同一份清单、两套实现, 判据漂移)。
	for _, v := range forgeKeepViolations(forgeItems(t), loadForgeManifest(t)) {
		t.Error(v)
	}
}
func TestHygieneForgeNoDuplicate(t *testing.T) {
	m := loadForgeManifest(t)

	seen := map[string]bool{}
	for _, e := range m.Entries {
		if e.Name == "" {
			t.Error("entries 存在空 name")
			continue
		}
		if seen[e.Name] {
			t.Errorf("entries 重复条目: %q", e.Name)
		}
		seen[e.Name] = true
		if e.Kind != "file" && e.Kind != "dir" {
			t.Errorf("entries %q 的 kind 非法: %q (须为 file|dir)", e.Name, e.Kind)
		}
		if e.Owner == "" {
			t.Errorf("entries %q 缺 owner —— 必须写明产生点, 否则无法判断失控时找谁", e.Name)
		}
		if !forgeInList(e.Category, forgeCategories) {
			t.Errorf("entries %q 的 category 非法: %q (须为 %s)",
				e.Name, e.Category, strings.Join(forgeCategories, "|"))
		}
		// 已登记项若又被 patterns 命中, 则其 kind 校验与空壳判定永不生效 —— 判据打架
		for _, p := range m.Patterns {
			if ok, _ := filepath.Match(p.Glob, e.Name); ok {
				t.Errorf("判据打架: entries 的 %q 被 patterns 的 %q 抢先命中, 其 kind/空壳判定永不生效",
					e.Name, p.Glob)
			}
		}
	}

	pseen := map[string]bool{}
	for _, p := range m.Patterns {
		if p.Glob == "" {
			t.Error("patterns 存在空 glob")
		}
		if p.Keep < 0 {
			t.Errorf("pattern %s 的 keep 为负: %d", p.Glob, p.Keep)
		}
		if pseen[p.Glob] {
			t.Errorf("patterns 重复 glob: %s", p.Glob)
		}
		pseen[p.Glob] = true
		if !forgeInList(p.Category, forgeCategories) {
			t.Errorf("pattern %s 的 category 非法: %q", p.Glob, p.Category)
		}
	}

	for _, e := range m.AllowEmptyDirs {
		if !seen[e] {
			t.Errorf("allow_empty_dirs 的 %q 不在 entries 里 —— 豁免指向了未登记的项", e)
		}
	}

	// 悬置判据本身 (内存构造, 不碰真清单) —— 判据必须能被测到, 否则只能靠改真文件验证
	staleCases := []struct {
		why   string
		names []string
		m     forgeManifest
		want  int
	}{
		{"全部存在", []string{"a", "b"},
			forgeManifest{Entries: []forgeEntry{{Name: "a"}, {Name: "b"}}}, 0},
		{"一条悬置", []string{"a"},
			forgeManifest{Entries: []forgeEntry{{Name: "a"}, {Name: "gone"}}}, 1},
		{"悬置但被 pattern 覆盖", []string{"a"},
			forgeManifest{Entries: []forgeEntry{{Name: "ev_20260101.jsonl"}},
				Patterns: []forgePattern{{Glob: "ev_*.jsonl"}}}, 0},
		{"全部悬置", nil,
			forgeManifest{Entries: []forgeEntry{{Name: "x"}, {Name: "y"}}}, 2},
		{"空清单", []string{"a"}, forgeManifest{}, 0},
	}
	for _, c := range staleCases {
		if got := len(forgeStaleEntries(c.names, c.m)); got != c.want {
			t.Errorf("悬置判据 [%s]: 期望 %d 条, 实得 %d", c.why, c.want, got)
		}
	}
}

// TestHygieneForgeManifestFailClosed 哨兵: 清单结构残缺时必须被拦下。
// 缺字段的危险不在「报错」而在「不报错」: 少一个 entries, 「未登记」判定就
// 失去对照, 静默变成「全部违规」(刷屏后被人加豁免而失效) 或「全部放行」。
func TestHygieneForgeManifestFailClosed(t *testing.T) {
	full := loadForgeManifest(t)
	if p := forgeManifestProblem(full); p != "" {
		t.Fatalf("真实清单被判为不可用: %s", p)
	}
	cases := []struct {
		name   string
		mutate func(m *forgeManifest)
	}{
		{"缺 entries", func(m *forgeManifest) { m.Entries = nil }},
		{"缺 patterns", func(m *forgeManifest) { m.Patterns = nil }},
		{"vectors 不足", func(m *forgeManifest) { m.Vectors = m.Vectors[:3] }},
		{"review_days 为 0", func(m *forgeManifest) { m.ReviewDays = 0 }},
	}
	for _, c := range cases {
		mm := full
		c.mutate(&mm)
		if p := forgeManifestProblem(mm); p == "" {
			t.Errorf("fail-closed 失效: %s 未被检出", c.name)
		}
	}
}

// TestHygieneForgeVectors 哨兵: 判据一致性向量。
// 清单是 Go 与 Python 共用的单一数据源, 但**判定逻辑是两套独立实现** ——
// 只改一侧会造成静默漂移 (哨兵说合规、红牌说违规, 两边测试却都绿)。
func TestHygieneForgeVectors(t *testing.T) {
	m := loadForgeManifest(t)
	if len(m.Vectors) < 8 {
		t.Fatalf("向量仅 %d 条, 覆盖不足, 哨兵失效", len(m.Vectors))
	}
	for _, c := range m.Vectors {
		got, why := forgeVerdict(c.Name, c.Kind == "dir", c.Empty, m)
		if got != c.ExpectOK {
			t.Errorf("向量不符: %q (kind=%s, empty=%v) 期望 ok=%v, 实得 %v [%s]%s",
				c.Name, c.Kind, c.Empty, c.ExpectOK, got, why, forgeNote(c.Note))
			continue
		}
		if c.WhyContains != "" && !strings.Contains(why, c.WhyContains) {
			t.Errorf("向量归类不符: %q 原因应含 %q, 实得 %q%s",
				c.Name, c.WhyContains, why, forgeNote(c.Note))
		}
	}
	t.Logf("判据向量 %d 条全部一致 (Go 侧)", len(m.Vectors))
}

func forgeNote(note string) string {
	if note == "" {
		return ""
	}
	return "  // " + note
}
