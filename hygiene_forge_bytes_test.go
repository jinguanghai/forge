package main

// ============================================================================
// hygiene_forge_bytes_test.go —— .forge 轮转产物的【体积判据】与 keep 双源同步
// (20261002 加)
//
// 为什么需要:
//  1) 份数有界 != 体积有界。checkpoints 的 keep=3 恒定, 而每份是顶层 *.go +
//     gate 源码的完整副本 —— 源码行数翻倍即体积翻倍, 份数判据却全绿。
//     实测 20261002: 3 份 / 5.96MB (单份 1.98MB)。
//  2) manifest 的 max_bytes 字段此前【登记了但无人读】: holidays_*.json 写着
//     max_bytes=51200, 而 Go struct 无该字段、Python 侧只读 keep ——
//     「约束只以文档形式存在」= 不存在。本文件把它变成可执行判据。
//  3) 同一个 keep 数字写在两处: upgrade.go 的 const backupKeepCount (真正执行
//     轮转) 与清单/白名单里的 keep (判据上限)。历史上已经漂移过 ——
//     hygiene_whitelist.json 的 quota_globs 注释写着「旧值 1 与代码常量双源,
//     每次 deploy 到第 2 份即误报」。单一数据源做不到 (Go 运行时不依赖
//     defense_system/), 故用双向断言钉住。
//
// 与 Python 侧同构: defense_system/hygiene_forge_check.py 的 item_bytes /
// bytes_violations / keep_sync_violations。两套独立实现必然漂移, 故两侧同批
// 测试, 度量口径写在注释里 (极值不是证据, 分布才是 —— 但边界必须一致)。
//
// 只读: 本文件只读目录, 绝不移动/删除/修改任何文件。
// ============================================================================

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// forgeKeepConstSource 是 keep 数字的唯一权威来源 (真正执行轮转的那处)。
const forgeKeepConstSource = "upgrade.go"

// forgeWhitelistFile 根目录白名单 (quota_globs 里也有一个 keep 数字)。
const forgeWhitelistFile = "defense_system/hygiene_whitelist.json"

// forgeDirSize 返回 path 的体积 (目录递归求和; 出错项按 0 计, 不让 I/O 错误伪装成合规)。
// 与 Python 侧 item_bytes 同构 —— 两套独立实现必然漂移 (watchlist 双份定义同教训),
// 故度量口径写在注释里并由两侧同批测试钉住。
func forgeDirSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !st.IsDir() {
		return st.Size()
	}
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// TestHygieneCheckpointKeepInSync 哨兵: 清单的 keep 必须等于代码里的实际保留份数。
//
// 为什么需要 (20261002): 同一个数字写在两处 —— upgrade.go 的
// `const backupKeepCount = 3` (真正执行轮转) 与 manifest 的 patterns.keep (判据上限)。
// 改一处忘另一处, 判据就与实际不符: 代码留 5 份而清单说 3 份 -> 哨兵天天报红
// (狼来了, 红牌失效); 反向则判据永远追不上实际 (约束退化为文档)。
// 单一数据源做不到 (Go 运行时不依赖 defense_system/), 故用双向断言钉住 ——
// Python 侧同构见 hygiene_forge_check.py 的 keep_sync_violations。
func TestHygieneCheckpointKeepInSync(t *testing.T) {
	raw, err := os.ReadFile("upgrade.go")
	if err != nil {
		t.Fatalf("读取 upgrade.go 失败 (fail-closed, 不放行): %v", err)
	}
	mt := regexp.MustCompile(`const\s+backupKeepCount\s*=\s*(\d+)`).FindSubmatch(raw)
	if mt == nil {
		t.Fatal("upgrade.go 中未找到 const backupKeepCount —— 常量被改名/移走, " +
			"keep 同步判据失效 (fail-closed, 不放行)")
	}
	want, err := strconv.Atoi(string(mt[1]))
	if err != nil {
		t.Fatalf("backupKeepCount 解析失败: %v", err)
	}
	m := loadForgeManifest(t)
	seen := 0
	for _, sd := range m.Subdirs {
		if sd.Path != "checkpoints" {
			continue
		}
		for _, p := range sd.Patterns {
			seen++
			if p.Keep != want {
				t.Errorf("keep 漂移: 清单 %s 的 %s keep=%d != upgrade.go backupKeepCount=%d\n"+
					"  处理: 两处必须同改 (清单 %s)",
					sd.Path, p.Glob, p.Keep, want, forgeManifestFile)
			}
		}
	}
	if seen == 0 {
		t.Errorf("checkpoints 容器无 patterns —— keep 同步判据无对照对象 (判据腐化): %s",
			forgeManifestFile)
	}

	// 同一数字的第二个落点: 根目录白名单的 quota_globs (forge.exe.bak_* keep)。
	// 这不是假想风险 —— 该条目的 desc 自己记着历史漂移:
	// 「旧值 1 与代码常量双源, 每次 deploy 到第 2 份即误报」。
	hwRaw, err := os.ReadFile(forgeWhitelistFile)
	if err != nil {
		t.Fatalf("读取 %s 失败 (fail-closed, 不放行): %v", forgeWhitelistFile, err)
	}
	var hw struct {
		QuotaGlobs []struct {
			Pattern string `json:"pattern"`
			Keep    int    `json:"keep"`
			Desc    string `json:"desc"`
		} `json:"quota_globs"`
	}
	if err := json.Unmarshal(hwRaw, &hw); err != nil {
		t.Fatalf("%s 解析失败: %v", forgeWhitelistFile, err)
	}
	qseen := 0
	for _, q := range hw.QuotaGlobs {
		if q.Pattern != "forge.exe.bak_*" {
			continue
		}
		qseen++
		if q.Keep != want {
			t.Errorf("keep 漂移: 白名单 quota_globs[%s].keep=%d != %s backupKeepCount=%d\n"+
				"  处理: 两处必须同改 (白名单 %s)",
				q.Pattern, q.Keep, forgeKeepConstSource, want, forgeWhitelistFile)
		}
	}
	if qseen == 0 {
		t.Errorf("%s 无 forge.exe.bak_* 配额条目 —— exe 备份份数失去上限 (判据腐化)",
			forgeWhitelistFile)
	}
	t.Logf("keep=%d 三处同步: %s backupKeepCount + checkpoints(%d glob) + quota_globs(%d 条)",
		want, forgeKeepConstSource, seen, qseen)
}

// TestHygieneForgeBytesVerdict 自检: 体积判定真的能抓 —— 内存构造, 不必改真文件。
// 判据必须能被测到, 否则「加了判据」这个结论本身是假的 (变异自检自身会假绿: 见
// -run 传标签而非测试名的教训)。
func TestHygieneForgeBytesVerdict(t *testing.T) {
	const mb = 1 << 20
	items := []forgeItem{
		{Name: "20261002_000001_self", IsDir: true, Size: 6 * mb},
		{Name: "20261002_000002_self", IsDir: true, Size: 6 * mb},
		{Name: "20261002_000003_self", IsDir: true, Size: 6 * mb},
		{Name: "holidays_2026.json", Size: 25 * 1024},
	}
	// ① 容器超限: 实 18MB > 16MB -> 1 条
	if v := forgeBytesViolations(items, forgeManifest{MaxBytes: 16 * mb}); len(v) != 1 {
		t.Errorf("容器超限未报 (得 %d 条): %v", len(v), v)
	}
	// ② 容器未超限 -> 0 条
	if v := forgeBytesViolations(items, forgeManifest{MaxBytes: 32 * mb}); len(v) != 0 {
		t.Errorf("容器未超限被误报: %v", v)
	}
	// ③ 单份超限: 上限 5MB, 三份 6MB -> 3 条
	sp := forgeManifest{Patterns: []forgePattern{{Glob: "*_self", Kind: "dir", MaxBytes: 5 * mb}}}
	if v := forgeBytesViolations(items, sp); len(v) != 3 {
		t.Errorf("单份超限未逐项报 (期望 3, 得 %d): %v", len(v), v)
	}
	// ④ 单份恰好等于上限 -> 不报 (边界: 严格大于才违规, 与 Python 侧同口径)
	at := forgeManifest{Patterns: []forgePattern{{Glob: "*_self", Kind: "dir", MaxBytes: 6 * mb}}}
	if v := forgeBytesViolations(items, at); len(v) != 0 {
		t.Errorf("恰好达上限被误报: %v", v)
	}
	// ⑤ 不匹配 glob 的不计 (否则无关大文件会撑破 pattern 上限)
	if v := forgeBytesViolations(items, forgeManifest{
		Patterns: []forgePattern{{Glob: "*.tmp", Kind: "file", MaxBytes: 1}}}); len(v) != 0 {
		t.Errorf("不匹配 glob 的名字被计入: %v", v)
	}
	// ⑥ 未声明 (0) 一律不判
	if v := forgeBytesViolations(items, forgeManifest{}); len(v) != 0 {
		t.Errorf("未声明上限被误报: %v", v)
	}
	// ⑦ 真清单的 holidays 单份上限: 25KB 那份 < 51200B -> 不报 (确认字段真的被读到)
	m := loadForgeManifest(t)
	for _, p := range m.Patterns {
		if p.Glob == "holidays_*.json" && p.MaxBytes <= 0 {
			t.Errorf("holidays_*.json 的 max_bytes 未被解析 (Go struct 漏字段 = 假判据): %s",
				forgeManifestFile)
		}
	}
}

// forgeBytesViolations 判定体积上限 (20261002 加), 两类共用一个实现:
//
//	① 单份: 命中 pattern 的项体积 > pattern.MaxBytes
//	② 容器总量: 全部项体积之和 > spec.MaxBytes
//
// 为什么抽纯函数 (与 forgeKeepViolations 同理由): 根层与子目录必须共用同一实现,
// 且判据要能被内存构造直接测到 —— 靠改真文件验证既危险又测不全。
// 未声明 (MaxBytes<=0) 一律不判: 阈值需要实测基线, 无基线时强行设数字 =
// 拍脑袋阈值 (误报与漏报同源)。
func forgeBytesViolations(items []forgeItem, m forgeManifest) []string {
	var out []string
	for _, p := range m.Patterns {
		if p.MaxBytes <= 0 {
			continue
		}
		for _, it := range items {
			ok, _ := filepath.Match(p.Glob, it.Name)
			if !ok || it.Size <= p.MaxBytes {
				continue
			}
			out = append(out, fmt.Sprintf(
				"单份超体积上限: %s %.1fKB > 上限 %dKB (pattern %s)\n"+
					"  处理: 该产物按需清理/归档, 或按实测基线调高 max_bytes",
				it.Name, float64(it.Size)/1024, p.MaxBytes/1024, p.Glob))
		}
	}
	if m.MaxBytes > 0 {
		var total int64
		for _, it := range items {
			total += it.Size
		}
		if total > m.MaxBytes {
			out = append(out, fmt.Sprintf(
				"容器体积超限: %.2fMB > 上限 %.2fMB (%d 项)\n"+
					"  处理: 轮转/归档最旧项 —— 份数有界 != 体积有界",
				float64(total)/1048576, float64(m.MaxBytes)/1048576, len(items)))
		}
	}
	return out
}

// TestHygieneForgeBytesSpec 哨兵: 体积判据的【规格断言】—— 该有的字段必须在,
// 阈值必须落在实测基线的窄带内。
//
// 为什么单列一条: max_bytes 一旦被从清单里删掉, 判据会静默退化为「只看份数」
// 而所有行为测试仍然全绿 (判据自己也要有判据)。只钉已实测定基线的容器,
// 而非「所有容器都要求」: 阈值需要实测基线, 未定基线的容器强行设数字 =
// 拍脑袋阈值 (误报与漏报同源), 该由实测后按需补 —— 见 manifest 各容器 doc。
//
// 倍率窄带 [1,3] (20261002 加): 下限 1 倍防「阈值低于现状 -> 常驻误报」,
// 上限 3 倍防「阈值大到永不触发 -> 判据静默失效」。两个方向都是判据腐化,
// 与「P95 耗时判据需近期对照, 否则老样本把告警钉成常驻」同源。
// 体积自然增长逼近上限时本测试报红 —— 那是要人复审阈值, 是有意为之的摩擦。
func TestHygieneForgeBytesSpec(t *testing.T) {
	m := loadForgeManifest(t)
	// 已实测定基线的容器 (20261002): 逐个必须有 max_bytes, 且倍率在带内。
	need := map[string]bool{"checkpoints": true, "forge-tools": true,
		"asr_tool": true, "backups": true}
	seen := map[string]bool{}
	for _, sd := range m.Subdirs {
		if sd.MaxBytes < 0 {
			t.Errorf("subdirs 的 %s max_bytes 为负: %d (0 = 不判, 负数无意义)",
				sd.Path, sd.MaxBytes)
		}
		if !need[sd.Path] {
			continue
		}
		seen[sd.Path] = true
		if sd.MaxBytes <= 0 {
			t.Errorf("subdirs 的 %s 未声明 max_bytes —— 体积判据静默失效 "+
				"(份数有界 != 体积有界); 清单: %s", sd.Path, forgeManifestFile)
			continue
		}
		actual := forgeDirSize(filepath.Join(".forge", sd.Path))
		if actual <= 0 {
			t.Errorf("subdirs 的 %s 实测体积为 0 —— 判据失去对照对象 (路径写错? 目录空?)",
				sd.Path)
			continue
		}
		switch {
		case actual > sd.MaxBytes:
			t.Errorf("%s 实测 %.2fMB 已超 max_bytes %.2fMB —— 阈值低于现状 (常驻误报); "+
				"处理: 清理该容器或按实测复审阈值", sd.Path,
				float64(actual)/1048576, float64(sd.MaxBytes)/1048576)
		case actual*3 < sd.MaxBytes:
			t.Errorf("%s max_bytes %.2fMB 是实测 %.2fMB 的 %.2f 倍 —— 超 3 倍等于判据失效; "+
				"处理: 按实测收紧 (口径: 实测 x2~3)", sd.Path,
				float64(sd.MaxBytes)/1048576, float64(actual)/1048576,
				float64(sd.MaxBytes)/float64(actual))
		default:
			t.Logf("%s: 实测 %.2fMB / 上限 %.2fMB = %.2f 倍 (带内 [1,3])", sd.Path,
				float64(actual)/1048576, float64(sd.MaxBytes)/1048576,
				float64(sd.MaxBytes)/float64(actual))
		}
	}
	for name := range need {
		if !seen[name] {
			t.Errorf("容器 %s 不在清单 subdirs 中 —— 体积判据失去对照对象 (判据腐化)", name)
		}
	}
	for _, p := range m.Patterns {
		if p.MaxBytes < 0 {
			t.Errorf("pattern %s 的 max_bytes 为负: %d (0 = 不判, 负数无意义)", p.Glob, p.MaxBytes)
		}
	}
	if len(m.Patterns) == 0 {
		t.Errorf("清单无 patterns —— 体积判据失去对照对象: %s", forgeManifestFile)
	}
}
