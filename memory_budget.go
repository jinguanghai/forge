package main

// memory_budget.go — 记忆预算判据 (DG-3 L3, 20261012)
//
// 病根: 记忆有「写」的判据 (memory_guard 拦锚点), 没有「减」的判据 ——
// 只进不出 = 单调膨胀。实测 20261012: environment 一项 10201 字符占常驻 55%,
// lessons 92 条而召回配额 topK=3 → 长尾永不命中, 纯负担。
//
// 判据分两级 (可判定性不同, 混在一起会让真问题被软告警淹没):
//   硬 (判据红) B1 常驻预算: 全部非动态字段的紧凑 JSON 长度之和 ≤ memoryResidentBudget
//   硬 (判据红) B2 引用有效: folded_memory.items 的 archive 必须指向真实文件
//                (死引用 = 折叠假象: 索引在, 细节没了)
//   软 (只告警) B3 池子上限: lessons 条数 > memoryLessonsCeiling → 提醒该折叠
//
// 口径与 Python 侧 defense_system/memory_budget.py 同源 (同一份 dynamic 清单由
// anchor_fields_sentinel_test.go 的跨语言哨兵钉住)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// memoryResidentBudget 常驻字符预算 (DG-3 定义: 17925→12000 的目标值)。
	memoryResidentBudget = 12000
	// memoryLessonsCeiling lessons 池条数上限 —— 只约束「会过期」的条目
	// (ts != static)。static 是永久事实, 不该受池上限约束: 旧口径数全部条数,
	// 而上限 60 < static 61, 从设立之日起就不可能达成 = 告警恒亮 = 狼来了。
	memoryLessonsCeiling = 60
)

// archiveRefRe 档案文件引用形态: _archive/<路径>.<扩展名>。
//
// 用**白名单**字符集而非黑名单: 黑名单挡不住中文标点 (「见 _archive/a.md。」
// 会把句号吃进路径 -> 假红)。与 Python 侧 ARCHIVE_REF_RE 同口径 (Go 写
// \x{4e00} 形式, 语义等价), 由 memory_budget_test.go 的同型向量哨兵钉住。
var archiveRefRe = regexp.MustCompile(`_archive[\\/][A-Za-z0-9_\-./\\\x{4e00}-\x{9fff}]+\.[A-Za-z0-9]{1,8}`)

// compactJSONLen 紧凑 JSON 的**字符数** (不是字节数)。
//
// 两个坑, 缺一个两侧就算出两个数 (20261012 实测: Go 报 18289 而 Python 报 10464):
//
//	① 必须关 HTML 转义 —— Go 默认把 < > & 转成 \u003c 等, Python 不会
//	   (记忆里 systemPrompt 段就含 <>)。
//	② 必须数 rune 不数字节 —— 中文 1 字符 = 3 字节, 数字节等于给中文记三倍账。
//	   口径与生产 estimateTokens 一致 (1 rune = 1 token 的保守上界), 也与
//	   Python 侧 len(str) 一致。
func compactJSONLen(v interface{}) int {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return 0
	}
	return utf8.RuneCount(bytes.TrimRight(buf.Bytes(), "\n"))
}

// memoryResidentChars 常驻字符数 = 全部非动态字段的紧凑 JSON 长度之和。
func memoryResidentChars(m map[string]interface{}) int {
	total := 0
	for k, v := range m {
		if isDynamicMemoryField(k) {
			continue
		}
		total += compactJSONLen(v)
	}
	return total
}

// memoryBudgetCheck 返回 (硬问题, 软告警)。记忆不可读时归入硬问题。
func memoryBudgetCheck(workDir string) (hard []string, soft []string) {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return []string{"记忆不可读: " + err.Error()}, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return []string{"JSON 解析失败: " + err.Error()}, nil
	}
	if n := memoryResidentChars(m); n > memoryResidentBudget {
		hard = append(hard, fmt.Sprintf("常驻字符 %d > 预算 %d (史实该折叠进 _archive/folded_memory/)", n, memoryResidentBudget))
	}
	hard = append(hard, foldedArchiveIssues(workDir, m)...)
	hard = append(hard, archiveRefIssues(workDir, m)...)
	if _, dated := lessonsCounts(m); dated > memoryLessonsCeiling {
		soft = append(soft, fmt.Sprintf("会过期的 lessons %d 条 > 上限 %d (static 永久事实不计; 召回配额 topK=%d, 长尾永不命中)", dated, memoryLessonsCeiling, lessonsRecallTopK))
	}
	return hard, soft
}

// foldedArchiveIssues 折叠条目必须带 archive 字段 (结构完整性)。
//
// 死引用检查已并入 archiveRefIssues (全文口径, 天然覆盖 items) —— 旧实现只查
// items[].archive, 而实测记忆里另有 2 处**正文**引用同样死了却看不见
// (defense / environment.史实归档), 判据覆盖面单向 = 漏检无人管。
func foldedArchiveIssues(workDir string, m map[string]interface{}) []string {
	fm, ok := m["folded_memory"].(map[string]interface{})
	if !ok {
		return nil
	}
	items, ok := fm["items"].([]interface{})
	if !ok {
		return nil
	}
	var issues []string
	for _, it := range items {
		im, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := im["id"].(string)
		if ap, _ := im["archive"].(string); ap == "" {
			issues = append(issues, fmt.Sprintf("折叠条目 %s 缺 archive 字段", id))
		}
	}
	return issues
}

// iterJSONStrings 递归产出 JSON 里的全部字符串值 (顺序无关, 只要全覆盖)。
func iterJSONStrings(v interface{}, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case map[string]interface{}:
		for _, e := range x {
			iterJSONStrings(e, out)
		}
	case []interface{}:
		for _, e := range x {
			iterJSONStrings(e, out)
		}
	}
}

// archiveRefIssues 全文扫描档案引用, 挑出指向不存在文件的那些。
//
// 为什么是硬判据: 折叠/归档机制的意义就是「索引在、细节在别处」。引用指向不
// 存在的文件 = 细节其实丢了, 而机制看起来一切正常。实测 20261013: 归档动作
// 把 _archive/folded_memory/ 改名成 folded_memory_20261012/, 6 处引用全死
// (4 条 items + 2 处正文), 而判据当时只查 items, 只报得出 4 条。
// 目录形态引用 (_archive/hygiene_quarantine) 与纯文字提及不算引用, 不报。
func archiveRefIssues(workDir string, m map[string]interface{}) []string {
	var strs []string
	iterJSONStrings(m, &strs)
	seen := map[string]bool{}
	var dead []string
	for _, s := range strs {
		for _, mt := range archiveRefRe.FindAllString(s, -1) {
			rel := strings.ReplaceAll(mt, "\\", "/")
			if seen[rel] {
				continue
			}
			seen[rel] = true
			if _, err := os.Stat(filepath.Join(workDir, rel)); err != nil {
				dead = append(dead, rel)
			}
		}
	}
	sort.Strings(dead)
	return dead
}

// lessonsCounts 返回 (总条数, 会过期的条数)。
//
// 兼容旧格式整段字符串 (按非空行计, 全部算会过期)。缺 ts 视为会过期
// (fail-closed: 无法证明它是永久事实就按会过期管, 与召回侧 stale 降权同口径)。
func lessonsCounts(m map[string]interface{}) (int, int) {
	switch v := m["lessons"].(type) {
	case []interface{}:
		dated := 0
		for _, it := range v {
			im, ok := it.(map[string]interface{})
			if !ok {
				dated++ // 形态不可判定 -> 按会过期算
				continue
			}
			if ts, _ := im["ts"].(string); ts != "static" {
				dated++
			}
		}
		return len(v), dated
	case string:
		n := 0
		for _, line := range bytes.Split([]byte(v), []byte("\n")) {
			if len(bytes.TrimSpace(line)) > 0 {
				n++
			}
		}
		return n, n
	}
	return 0, 0
}

// memoryBudgetEnabled 记忆预算臂开关 (默认开; FORGE_MEMORY_BUDGET=0 关闭)。
func memoryBudgetEnabled() bool {
	return os.Getenv("FORGE_MEMORY_BUDGET") != "0"
}

// memoryBudgetText 拒绝回执的修复路径。
const memoryBudgetText = `记忆常驻超预算 = 每一轮对话都要白付的 token (注意力是稀缺资源)。
修复路径:
  · 把过时史实折叠进 _archive/folded_memory/, 并在 memory.json 的
    folded_memory.items 登记 (archive 必须指向真实存在的文件)
  · 删掉指向不存在文件的引用 (死引用 = 细节其实丢了, 机制却看着正常)
改完重跑一次即可。`

// memoryBudgetDeny 记忆预算执行臂 (T53, 20261014)。
//
// 为什么必须接线: 本文件的判据 (memoryBudgetCheck) 写完且测试全绿, 但生产侧
// 零调用 -> 链接器 DCE 整棵剔除 (实测 go tool nm 里 memoryBudget* 符号 0 个)
// -> 那次源码改动对 exe 零行为影响。判据的第三种形态 = 「写了、测了、但不跑」。
//
// 定位 = 运行时拦 (同 memoryGuardDeny): 记忆系统自身不健康时拒绝干活, 先修记忆。
// 沙箱无 memory.json -> 不判定 (同 checkMemoryGuard 口径; 否则测试隔离目录集体误拒)。
func (f *Forge) memoryBudgetDeny(code, lang string) (string, *ForgeGateResult, bool) {
	if !memoryBudgetEnabled() {
		return "", nil, false
	}
	if _, err := os.Stat(memoryFilePath(f.workDir)); err != nil {
		return "", nil, false
	}
	hard, _ := memoryBudgetCheck(f.workDir)
	if len(hard) == 0 {
		return "", nil, false
	}
	ev := strings.Join(hard, "; ")
	res := &ForgeGateResult{
		OK: false, Lang: lang, Stage: "rejected",
		Error: "记忆预算被拒: " + ev,
	}
	logGuardEvent(f.workDir, "high", "记忆预算", "budget", "deny", ev, code)
	return fmt.Sprintf("--- ⛔ 记忆预算被拒 [rejected] ---\n%s\n\n%s\n--- 结束 ---",
		strings.Join(hard, "\n"), memoryBudgetText), res, true
}
