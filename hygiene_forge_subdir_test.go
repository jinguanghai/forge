package main

// ============================================================================
// hygiene_forge_subdir_test.go —— .forge 子目录内部清单哨兵 (20260928 补)
//
// 为什么需要:
//   根清单 hygiene_forge_manifest.json 只登记了 forge-tools 这一层 ——
//   该目录内部 19 项 / 77.8MB 无任何判据。实测后果:
//     tcc_gate_src/    94 文件 11.23MB —— 四期已裁剪的死代码, 躺了数月
//     chain_gate.exe   2.31MB         —— 真引用 0 处 (记忆写着"孤儿"却无人处置)
//     _bak/            4 文件 7.17MB  —— 无轮转的 gate 二进制备份
//   三者都因「根哨兵不递归」而完全不可见 —— 不是漏判, 是判据根本不存在。
//
//   判据形态与根层完全同构 (entries 精确 + patterns glob + 悬置反向), 复用
//   forgeVerdict / forgeStaleEntries —— 两套独立实现必然漂移
//   (watchlist 双份定义漂移、selfheal 硬编码 9 文件, 同一教训已复发两次)。
//
// 只读: 本文件只读目录, 绝不移动/删除/修改任何文件。
// ============================================================================

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// forgeSubdirSpec 把子清单折成判定规格 —— 直接复用根层的 forgeVerdict。
// 子清单与根清单字段同名同义 (entries / patterns / allow_empty_dirs)。
func forgeSubdirSpec(s forgeSubdir) forgeManifest {
	return forgeManifest{Entries: s.Entries, Patterns: s.Patterns, AllowEmptyDirs: s.AllowEmptyDirs}
}

// forgeScanSubdir 扫 .forge/<rel> 的一级项 (按名排序)。与 forgeItems 同构。
func forgeScanSubdir(t *testing.T, rel string) []forgeItem {
	t.Helper()
	d := filepath.Join(".forge", rel)
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatalf("读取 .forge/%s 失败 (fail-closed, 不放行): %v", rel, err)
	}
	var out []forgeItem
	for _, e := range entries {
		it := forgeItem{Name: e.Name(), IsDir: e.IsDir()}
		if it.IsDir {
			sub, err := os.ReadDir(filepath.Join(d, it.Name))
			if err == nil {
				it.Empty = len(sub) == 0
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// forgeUncoveredDirs 判定「容器清单缺口」: .forge 下哪些一级目录没在 subdirs 登记。
// 没登记的容器, 内部就是盲区 —— 这正是本文件存在的理由, 却漏了自己。
//
// 为什么需要 (20260928 变异实测): 从 subdirs 删掉 backups 容器后, Go 与 Python
// 两侧均 rc=0。原因: 根 entries 仍登记着 backups 这个目录本身 (未登记检测通过),
// 而没有任何检查问「这个目录的内部归谁管」。
// 与 entries 的未登记/悬置同理 —— 只做单向对照必漏。
//
// 只收「非空目录」: 空壳目录内部无物, 且已由 allow_empty_dirs 机制管辖。
// 收名字集合而非 []forgeItem: 便于内存构造直接测判据本身。
func forgeUncoveredDirs(dirs []string, m forgeManifest) []string {
	declared := make(map[string]bool, len(m.Subdirs))
	for _, sd := range m.Subdirs {
		declared[sd.Path] = true
	}
	var out []string
	for _, d := range dirs {
		if !declared[d] {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// forgeKeepViolations 判定「轮转产物超 keep」: 同一 glob 的命中份数 > 上限即违规。
//
// 为什么抽成纯函数 (20260930 实测缺口):
//
//	该判据原先只写在根层 TestHygieneForgePatternKeep 里, 且只喂 forgeItems()
//	(仅 .forge 一级项) —— 子目录清单里的 patterns.keep 从未被任何判据看见。
//	实测: .forge/backups 内 forge.exe.* 现存 4 份 > keep=3, Python 侧
//	(judge_items 对根层与子目录共用同一实现) rc=1 报红, Go 侧 go test 全绿 ——
//	同一份清单、两套实现, 判据漂移 (与 watchlist 双份定义、selfheal 硬编码 9 文件同构)。
//	修法: 抽成纯函数, 根层与子目录共用; 纯函数可被内存构造直接测到, 不必改真文件。
func forgeKeepViolations(items []forgeItem, m forgeManifest) []string {
	var out []string
	for _, p := range m.Patterns {
		var hits []string
		for _, it := range items {
			if ok, _ := filepath.Match(p.Glob, it.Name); ok {
				hits = append(hits, it.Name)
			}
		}
		if len(hits) > p.Keep {
			out = append(out, fmt.Sprintf(
				"轮转超限: %s 现存 %d 份 > 上限 %d (%s)\n  命中: %s\n  处理: 保留最新 %d 份, 其余移入 _archive/",
				p.Glob, len(hits), p.Keep, p.Note, strings.Join(hits, ", "), p.Keep))
		}
	}
	return out
}

// TestHygieneForgeSubdirs 哨兵: 每个 subdir 的一级项必须全部登记, 且无悬置条目。
func TestHygieneForgeSubdirs(t *testing.T) {
	m := loadForgeManifest(t)
	if len(m.Subdirs) == 0 {
		t.Fatal("清单无 subdirs —— 子目录内部重回盲区 (fail-closed, 不放行)")
	}

	// 容器清单完整性: 每个非空一级目录都必须有对应容器, 否则其内部无判据。
	// (变异实测: 删掉某个容器后两侧均 rc=0 —— 这条判据此前不存在。)
	var dirs []string
	for _, it := range forgeItems(t) {
		if it.IsDir && !it.Empty {
			dirs = append(dirs, it.Name)
		}
	}
	if unc := forgeUncoveredDirs(dirs, m); len(unc) > 0 {
		t.Errorf(".forge 有 %d 个一级目录未在 subdirs 登记 —— 其内部无任何判据 (盲区):\n  %s\n"+
			"处理: 加进 subdirs (path + entries 或 patterns) %s",
			len(unc), strings.Join(unc, "\n  "), forgeManifestFile)
	}

	for _, sd := range m.Subdirs {
		items := forgeScanSubdir(t, sd.Path)
		if len(items) == 0 {
			// allow_empty_self: 容器自身允许为空 (updates 仅在自改时写入一条审计)。
			// 与「路径写错」必须分开报 —— 否则真实空态被误报为悬空, 而误报会让人关掉整条判据。
			if sd.AllowEmptySelf {
				t.Logf(".forge/%s: 当前为空 (清单声明 allow_empty_self)", sd.Path)
				continue
			}
			t.Errorf("子目录 .forge/%s 不存在或为空 —— 判据悬空 (路径写错或已移走?)", sd.Path)
			continue
		}
		spec := forgeSubdirSpec(sd)

		// 自检: 清单必须真的匹配到东西 (防路径写错后静默全放过)
		matched := 0
		for _, it := range items {
			if ok, _ := forgeVerdict(it.Name, it.IsDir, it.Empty, spec); ok {
				matched++
			}
		}
		if matched*2 < len(items) {
			t.Fatalf("子目录 .forge/%s 清单仅匹配到 %d/%d 项, 疑似失效",
				sd.Path, matched, len(items))
		}

		var bad []string
		for _, it := range items {
			ok, why := forgeVerdict(it.Name, it.IsDir, it.Empty, spec)
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

		names := make([]string, 0, len(items))
		for _, it := range items {
			names = append(names, it.Name)
		}
		stale := forgeStaleEntries(names, spec)
		if len(stale) > 0 {
			t.Errorf(".forge/%s 存在悬置条目 %d 条 —— 登记了但目录里不存在 (判据腐化):\n  %s\n清单: %s",
				sd.Path, len(stale), strings.Join(stale, "\n  "), forgeManifestFile)
		}
		if len(bad) > 0 {
			t.Errorf(".forge/%s 出现未登记/异常项 %d 个 (5S/1S 整理):\n  %s\n"+
				"处理: 历史资产 → _archive/ ; 确属项目本体 → 加进 subdirs 的 entries %s",
				sd.Path, len(bad), strings.Join(bad, "\n  "), forgeManifestFile)
		}
		// 轮转份数上限 (硬判据): 子目录 patterns 的 keep 此前从未被判定 ——
		// 根层 TestHygieneForgePatternKeep 只扫 .forge 一级项 (forgeItems), 不递归。
		for _, kv := range forgeKeepViolations(items, spec) {
			t.Errorf(".forge/%s %s\n清单: %s", sd.Path, kv, forgeManifestFile)
		}
		if len(bad) == 0 && len(stale) == 0 {
			t.Logf(".forge/%s: %d 个一级项, 全部已登记", sd.Path, len(items))
		}
	}
}

// TestHygieneForgeKeepVerdict 自检: keep 判定真的能抓 —— 内存构造, 不必改真文件。
func TestHygieneForgeKeepVerdict(t *testing.T) {
	spec := forgeManifest{Patterns: []forgePattern{
		{Glob: "forge.exe.*", Kind: "file", Category: "backup", Keep: 3, Note: "测试用"},
	}}
	at := []forgeItem{{Name: "forge.exe.a"}, {Name: "forge.exe.b"}, {Name: "forge.exe.c"}}
	if v := forgeKeepViolations(at, spec); len(v) != 0 {
		t.Errorf("恰好达上限 (%d 份 == keep) 被误报: %v", len(at), v)
	}
	over := append(append([]forgeItem{}, at...), forgeItem{Name: "forge.exe.d"})
	if v := forgeKeepViolations(over, spec); len(v) != 1 {
		t.Errorf("超限 1 份未报 (得 %d 条): %v", len(v), v)
	}
	// 不匹配 glob 的名字不得计入份数 (否则 keep 会被无关文件撑破)
	other := append(append([]forgeItem{}, at...), forgeItem{Name: "notes.txt"})
	if v := forgeKeepViolations(other, spec); len(v) != 0 {
		t.Errorf("不匹配 glob 的名字被计入份数: %v", v)
	}
	// keep=0 语义: 任何命中即超限 (与 Python 侧 p.get('keep', 0) 缺省一致)
	zero := forgeManifest{Patterns: []forgePattern{{Glob: "*.tmp", Kind: "file", Keep: 0}}}
	if v := forgeKeepViolations([]forgeItem{{Name: "x.tmp"}}, zero); len(v) != 1 {
		t.Errorf("keep=0 未拦下命中项: %v", v)
	}
	// 无 patterns 的容器 (纯 entries 型) 不得报违规
	if v := forgeKeepViolations(at, forgeManifest{}); len(v) != 0 {
		t.Errorf("无 patterns 清单被误报: %v", v)
	}
}
