package main

// ============================================================================
// hygiene_dir_test.go —— 根目录「目录」白名单哨兵 (5S/1S 整理 · 2S 定置管理)
//
// 为什么与文件哨兵分列 (20260928, ⑦ 阶段):
//   文件哨兵只管根目录的普通文件 (hygieneRootFiles 里 `if e.IsDir() { continue }`),
//   19 个一级子目录完全在视野外 —— 实测 plugins/ 是 0 项空壳, 躺了不知多久,
//   四套守卫没有任何一个看得见它。
//
//   目录是容器, 判据与文件不同 (文件 = 点, 目录 = 面):
//     1. 隐藏目录 (.*) -> 允许 (配置/运行时区, 低可见性)
//     2. allow_dirs 已登记 -> 允许; 但「空壳」(0 项) 且不在 allow_empty_dirs -> 违规
//     3. 未登记 -> 违规
//   「空壳目录」单列, 因为它是 1S 里最无争议的垃圾形态: 东西拿走了壳没删。
//
//   与文件哨兵同源同判据 (同一个 hygiene_whitelist.json), 但同一条目在两侧的
//   处置不同: 文件红牌到期可移入隔离区, 目录只贴牌不移动 (目录可能是程序工作
//   目录, 移动风险高于文件)。
//
// 拆分原因: 合并后单文件 521 行, 超 fractal F2 (文件 ≤500 行) 上限。
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

// hygieneDir 目录白名单条目。role 必填(这个目录是干什么的),
// note 选填(存疑项的说明, 如「演示产物, 0 引用, 待确认去留」)。
type hygieneDir struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Note string `json:"note"`
}

// hygieneRootDirs 返回根目录下的一级子目录名 (不含普通文件)。
func hygieneRootDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取根目录失败: %v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// hygieneDirIsEmpty 判定目录是否为「空壳」(0 项)。
// 读不到时不判空 (偏向不误报), 由测试的自检阈值兜底。
func hygieneDirIsEmpty(name string) bool {
	entries, err := os.ReadDir(name)
	if err != nil {
		return false
	}
	return len(entries) == 0
}

// hygieneInList 判定名字是否在字符串列表中 (大小写敏感, 目录名不做折叠)。
func hygieneInList(name string, list []string) bool {
	for _, x := range list {
		if x == name {
			return true
		}
	}
	return false
}

// hygieneDirVerdict 判定单个目录名: (是否允许, 归类说明)。
// 优先级: allow_empty_dirs(显式豁免) > allow_dir_globs(隐藏) > allow_dirs > 未登记。
// allow_empty_dirs 提到最前 —— 它是「这个容器允许为空」的显式声明,
// 对隐藏目录与非隐藏目录同样有效 (.forge-temp 启动清空后就是空的,
// 若只在 allow_dirs 分支里豁免, 隐藏目录就无处声明)。
// 空壳判定只在 allow_dirs 命中后追加 —— 未登记的目录无论空不空都是违规,
// 分开报会让同一目录产生两条原因, 排查时互相干扰。
func hygieneDirVerdict(name string, wl hygieneWhitelist, empty bool) (bool, string) {
	if empty && hygieneInList(name, wl.AllowEmptyDirs) {
		return true, "empty-ok"
	}
	for _, g := range wl.AllowDirGlobs {
		if ok, _ := filepath.Match(g, name); ok {
			return true, "dirglob:" + g
		}
	}
	for _, d := range wl.AllowDirs {
		if d.Name != name {
			continue
		}
		if empty {
			return false, "空壳目录 (0 项) —— 东西拿走了壳没删; 确需保留请加 allow_empty_dirs"
		}
		return true, "dir:" + d.Role
	}
	return false, "未登记的根目录 —— 2S 定置管理: 每个容器必须有位置 " +
		"(归档/临时区用 _ 前缀; 确属项目本体请加 allow_dirs, 需 git 可见)"
}

// TestHygieneRootDirWhitelist 哨兵: 根目录不得出现未登记的一级子目录。
// 与文件哨兵同构 (自检阈值 + 违规聚合 + 有违规时不打印「全部合规」)。
func TestHygieneRootDirWhitelist(t *testing.T) {
	wl := loadHygieneWhitelist(t)
	dirs := hygieneRootDirs(t)
	if len(dirs) == 0 {
		t.Fatal("根目录未发现任何子目录, 目录哨兵失效")
	}

	// 自检: 白名单必须真的匹配到东西 (防路径写错 / 白名单被清空后静默失效)
	matched := 0
	for _, d := range dirs {
		if ok, _ := hygieneDirVerdict(d, wl, hygieneDirIsEmpty(d)); ok {
			matched++
		}
	}
	if matched < 5 {
		t.Fatalf("目录白名单仅匹配到 %d/%d, 疑似失效 (检查 %s 与测试 cwd)",
			matched, len(dirs), hygieneWhitelistFile)
	}

	var bad []string
	for _, d := range dirs {
		empty := hygieneDirIsEmpty(d)
		ok, why := hygieneDirVerdict(d, wl, empty)
		if ok {
			continue
		}
		tag := ""
		if empty {
			tag = " [空壳]"
		}
		bad = append(bad, fmt.Sprintf("%s%s  [%s]", d, tag, why))
	}
	if len(bad) > 0 {
		t.Errorf("根目录出现违规子目录 %d 个 (5S/1S 整理 · 2S 定置管理):\n  %s\n"+
			"处理: 归档/临时区 → _ 前缀 ; 已废弃 → 移入 _archive/ ; "+
			"空壳确需保留 → 加 allow_empty_dirs ; 确属项目本体 → 加 allow_dirs %s (需 git 可见)",
			len(bad), strings.Join(bad, "\n  "), hygieneWhitelistFile)
		return // 有违规时不打印「全部已登记」—— 否则日志自相矛盾, 误导排查
	}
	t.Logf("根目录 %d 个子目录, 全部已登记", len(dirs))
}

// TestHygieneDirWhitelistNoDuplicate 哨兵: 目录判据自身不得自相矛盾。
// 判据矛盾 = 判定结果取决于遍历顺序 = 不可复现 (与文件侧同理)。
func TestHygieneDirWhitelistNoDuplicate(t *testing.T) {
	wl := loadHygieneWhitelist(t)

	if len(wl.AllowDirGlobs) == 0 {
		t.Error("allow_dir_globs 为空 —— 隐藏目录将全部落入「未登记」")
	}

	// 不得含宽口子 (2026-09-28): 旧值 ".*" 让任何新隐藏目录静默通过 ——
	// 实测 .workbuddy 有内容却无 role, 长期无人过问。
	// 向量测的是行为, 这条测的是结构: 即使向量被一起改坏, 也拦得住。
	for _, g := range wl.AllowDirGlobs {
		if g == ".*" || g == "*" || g == "**" {
			t.Errorf("allow_dir_globs 含宽口子 %q —— 任何新隐藏目录静默通过; 隐藏目录应逐字登记进 allow_dirs", g)
		}
	}

	seen := map[string]bool{}
	for _, d := range wl.AllowDirs {
		if d.Name == "" {
			t.Error("allow_dirs 存在空 name")
			continue
		}
		if d.Role == "" {
			t.Errorf("allow_dirs 条目 %q 缺 role —— 白名单必须说明「这个目录是干什么的」", d.Name)
		}
		if seen[d.Name] {
			t.Errorf("allow_dirs 重复条目: %q", d.Name)
		}
		seen[d.Name] = true

		// 已登记目录若又被 allow_dir_globs 命中, 则空壳判定永远走不到 ——
		// 属于判据打架 (同文件侧 quota 先于 deny 的坑)。
		for _, g := range wl.AllowDirGlobs {
			if ok, _ := filepath.Match(g, d.Name); ok {
				t.Errorf("判据打架: allow_dirs 的 %q 被 allow_dir_globs 的 %q 抢先命中, 其空壳判定永不生效",
					d.Name, g)
			}
		}
	}

	// allow_empty_dirs 必须指向真实允许的目录, 否则是笔误 (豁免了不存在的目录)。
	// 隐藏目录走 allow_dir_globs 分支, 不必出现在 allow_dirs 里 —— 故两者都算命中。
	for _, e := range wl.AllowEmptyDirs {
		if seen[e] {
			continue
		}
		hit := false
		for _, g := range wl.AllowDirGlobs {
			if ok, _ := filepath.Match(g, e); ok {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("allow_empty_dirs 的 %q 既不在 allow_dirs, 也不被 allow_dir_globs 命中 —— 豁免指向了未登记的目录", e)
		}
	}
}

// hygieneVector / hygieneVectors —— 判据一致性向量文件的结构。
// 与 defense_system/hygiene_redcard.py 的 --selftest 共用同一份 JSON:
// 两套独立实现 + 同一批输入 = 「单一数据源」才真的被钉住。
type hygieneVector struct {
	Name        string `json:"name"`
	Empty       bool   `json:"empty"`
	ExpectAllow bool   `json:"expect_allow"`
	WhyContains string `json:"why_contains"`
	Note        string `json:"note"`
}

type hygieneVectors struct {
	Version int             `json:"version"`
	Cases   []hygieneVector `json:"cases"`
}

// TestHygieneDirVectors 哨兵: 判据一致性向量。
// 为什么需要: 白名单是 Go 与 Python 共用的单一数据源, 但**判定逻辑是两套独立实现**。
// 只改一侧的判定(或只改白名单结构)会造成静默漂移 —— 哨兵说合规、红牌说违规,
// 或反之, 而两边各自的测试都绿。本用例把判定行为钉在共享向量上。
func TestHygieneDirVectors(t *testing.T) {
	wl := loadHygieneWhitelist(t)
	raw, err := os.ReadFile("defense_system/hygiene_vectors.json")
	if err != nil {
		t.Fatalf("向量文件读取失败 (fail-closed): %v", err)
	}
	var vec hygieneVectors
	if err := json.Unmarshal(raw, &vec); err != nil {
		t.Fatalf("向量文件解析失败: %v", err)
	}
	if len(vec.Cases) < 10 {
		t.Fatalf("向量文件仅 %d 条, 覆盖不足, 哨兵失效", len(vec.Cases))
	}

	for _, c := range vec.Cases {
		got, why := hygieneDirVerdict(c.Name, wl, c.Empty)
		if got != c.ExpectAllow {
			t.Errorf("向量不符: %q (empty=%v) 期望 allow=%v, 实得 %v [%s]%s",
				c.Name, c.Empty, c.ExpectAllow, got, why, noteSuffix(c.Note))
			continue
		}
		if c.WhyContains != "" && !strings.Contains(why, c.WhyContains) {
			t.Errorf("向量归类不符: %q (empty=%v) 原因应含 %q, 实得 %q%s",
				c.Name, c.Empty, c.WhyContains, why, noteSuffix(c.Note))
		}
	}
	t.Logf("判据向量 %d 条全部一致 (Go 侧)", len(vec.Cases))
}

func noteSuffix(note string) string {
	if note == "" {
		return ""
	}
	return "  // " + note
}
