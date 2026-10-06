package main

// ══════════════════════════════════════════════════════════════════
// memory_timestamp_test.go — 手写时间戳合理性判据 (T9, 20261005)
//
// 病根: 该由死程序生成的值落在手写路径上 —— 与教训「判据只钉字面串, 看不见描述过期」
// 同一类。实测同日两处:
//   memory.json.last_updated = "2026-10-05 18:20", 而该次写入实际发生在 18:12:25
//   TODO.md 标题写「18:25 更新」, 而实际编辑发生在 18:19
// 两处都无害(没有任何代码解析这些时间的数值), 但都不可复现 —— 预填的时间戳是
// 「未来事实」, 事后无法判断它是否真发生过。
//
// 处置两条:
//   ① 生成端交给死程序: memory_write.py stamp 子命令盖章 (手写路径不再需要);
//      写入时刻前置校验 [now-300s, now+60s], 越界拒绝 (Python 侧, selftest 覆盖)。
//   ② 事后哨兵 (本文件): 真文件的 last_updated / TODO.md 标题不得是「未来」。
//      未来时间戳属物理不可能型 (文件已存在而内容声称未来) —— 精确率天然 100%,
//      且刻意不用文件 mtime 做参照 (git checkout / 快照还原会改 mtime -> 误报)。
//
// 判据自身也有判据: TestTimestampWindowMutationSelfCheck 喂边界向量做鉴别力自检;
// 两侧窗口常量同源由 TestPythonTimestampWindowConstantsMatchGo 钉住 (防 Go/Python 漂移)。
// ══════════════════════════════════════════════════════════════════

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	tsWindowBackSec = 300 // 允许回溯 5 分钟 (先拟稿后落盘)
	tsWindowFwdSec  = 60  // 允许未来 1 分钟 (时钟抖动)
)

var (
	pyTsBackRe     = regexp.MustCompile(`(?m)^TS_WINDOW_BACK_SEC\s*=\s*(\d+)`)
	pyTsFwdRe      = regexp.MustCompile(`(?m)^TS_WINDOW_FWD_SEC\s*=\s*(\d+)`)
	todoHeaderTsRe = regexp.MustCompile(`（(\d{4}-\d{2}-\d{2} \d{2}:\d{2})\s*更新）`)
)

// tsParseAny 解析 "YYYY-MM-DD HH:MM" 与 ISO (T 分隔 / 带秒 / 带时区后缀) -> 本地时刻。
func tsParseAny(s string) (time.Time, bool) {
	t := strings.TrimSpace(strings.ReplaceAll(s, "T", " "))
	if len(t) > 19 {
		t = t[:19]
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if v, err := time.ParseInLocation(layout, t, time.Local); err == nil {
			return v, true
		}
	}
	return time.Time{}, false
}

// tsFutureProblem 只判「未来」方向 —— 真文件判据走这条。
// 未来时间戳属物理不可能型 (文件已存在而内容声称未来), 精确率天然 100%;
// 刻意不用文件 mtime 做参照 (git checkout / 快照还原会改 mtime -> 误报)。
// 拆成独立纯函数的另一个理由: 真文件判据的报红分支必须能被变异自检喂到,
// 否则"报红"只存在于文档里 (判据自身没有判据)。
func tsFutureProblem(ts string, now time.Time) string {
	if strings.TrimSpace(ts) == "" {
		return "缺时间戳 (fail-closed)"
	}
	v, ok := tsParseAny(ts)
	if !ok {
		return "不可解析的时间戳: " + ts
	}
	if v.After(now.Add(time.Duration(tsWindowFwdSec) * time.Second)) {
		return "晚于参照时刻 (预填未来): " + ts
	}
	return ""
}

// tsWindowProblem 时间戳 vs 参照时刻的双向窗口判定, 空串 = 在窗口内。
// 纯函数 (输入=字符串 + 参照时刻, 无全局状态) —— 变异自检得以喂伪造输入。
// 与 Python 侧 ts_window_problem 同构: 同一窗口常量、同一三个分支 (缺值/未来/陈旧)。
func tsWindowProblem(ts string, now time.Time) string {
	if p := tsFutureProblem(ts, now); p != "" {
		return p
	}
	v, _ := tsParseAny(ts)
	if v.Before(now.Add(-time.Duration(tsWindowBackSec) * time.Second)) {
		return "早于参照时刻 (陈旧手写值): " + ts
	}
	return ""
}

// TestMemoryLastUpdatedNotFuture 真 memory.json 的 last_updated 不得是未来时刻。
func TestMemoryLastUpdatedNotFuture(t *testing.T) {
	var ts string
	if err := json.Unmarshal(readMemoryField(t, "last_updated"), &ts); err != nil {
		t.Fatalf("解析 memory.json.last_updated 失败: %v", err)
	}
	v, ok := tsParseAny(ts)
	if !ok {
		t.Fatalf("memory.json.last_updated 不可解析: %q (fail-closed)", ts)
	}
	now := time.Now()
	if p := tsFutureProblem(ts, now); p != "" {
		t.Errorf("❌ memory.json.last_updated=%s (当前 %s, 超前 %s) —— %s; "+
			"用 .forge/forge-tools/memory_write.py stamp 由死程序盖章",
			ts, now.Format("2006-01-02 15:04"), v.Sub(now).Truncate(time.Second), p)
	}
}

// TestTodoHeaderTimestampNotFuture TODO.md 标题里的「更新时间」不得是未来时刻。
// fail-closed: 首行不再匹配该格式 = 判据失去作用面, 报红而非静默通过。
func TestTodoHeaderTimestampNotFuture(t *testing.T) {
	raw, err := os.ReadFile("TODO.md")
	if err != nil {
		t.Fatalf("读 TODO.md 失败: %v", err)
	}
	head := string(raw)
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	mm := todoHeaderTsRe.FindStringSubmatch(head)
	if mm == nil {
		t.Fatalf("TODO.md 首行未匹配到「（YYYY-MM-DD HH:MM 更新）」(fail-closed, 格式漂移?): %q", head)
	}
	if _, ok := tsParseAny(mm[1]); !ok {
		t.Fatalf("TODO.md 标题时间戳不可解析: %q", mm[1])
	}
	now := time.Now()
	if p := tsFutureProblem(mm[1], now); p != "" {
		t.Errorf("❌ TODO.md 标题声称「%s 更新」(当前 %s) —— %s; "+
			"标题时间应由死程序生成 (与 memory_write.py stamp 同一时刻)", mm[1], now.Format("2006-01-02 15:04"), p)
	}
}

// TestTimestampWindowMutationSelfCheck 判据自身的鉴别力: 越界必须报红, 窗口内必须不报。
func TestTimestampWindowMutationSelfCheck(t *testing.T) {
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.Local)
	cases := []struct {
		name string
		ts   string
		want bool // true = 应报红
	}{
		{"正例: 与参照时刻同分", "2026-10-05 18:00", false},
		{"正例: 回溯 4 分钟(拟稿窗口内)", "2026-10-05 17:56", false},
		{"正例: 未来 1 分钟内(时钟抖动)", "2026-10-05 18:00", false},
		{"正例: ISO 带秒", "2026-10-05T17:59:30+08:00", false},
		{"反例: 预填未来 10 分钟(病根现场)", "2026-10-05 18:10", true},
		{"反例: 未来 1 小时", "2026-10-05 19:00", true},
		{"反例: 陈旧 10 分钟", "2026-10-05 17:50", true},
		{"反例: 空值(fail-closed)", "", true},
		{"反例: 格式非法(fail-closed)", "刚才", true},
	}
	for _, c := range cases {
		got := tsWindowProblem(c.ts, now) != ""
		if got != c.want {
			t.Errorf("变异「%s」(%q): 报红=%v, 期望=%v", c.name, c.ts, got, c.want)
		}
	}
	// 真文件判据共用 tsFutureProblem —— 它的报红分支必须可达 (不是只写在文档里),
	// 且不得越界去管"陈旧"(陈旧可能来自 git 还原, 由写入路径的窗口校验负责)。
	if tsFutureProblem(now.Add(2*time.Hour).Format("2006-01-02 15:04"), now) == "" {
		t.Errorf("tsFutureProblem 对未来时间戳未报红 —— 真文件判据的报红分支不可达")
	}
	if tsFutureProblem(now.Add(-2*time.Hour).Format("2006-01-02 15:04"), now) != "" {
		t.Errorf("tsFutureProblem 对过去时间戳误报 (它只该管未来方向)")
	}
	if tsFutureProblem("", now) == "" {
		t.Errorf("tsFutureProblem 对空值未报红 (fail-closed 失效)")
	}
	// 边界自证: 窗口两侧的极值必须一侧绿一侧红 (阈值不是装饰)
	inEdge := now.Add(time.Duration(tsWindowFwdSec) * time.Second).Format("2006-01-02 15:04:05")
	outEdge := now.Add(time.Duration(tsWindowFwdSec+60) * time.Second).Format("2006-01-02 15:04:05")
	if tsWindowProblem(inEdge, now) != "" {
		t.Errorf("边界内 (%s) 被误判报红", inEdge)
	}
	if tsWindowProblem(outEdge, now) == "" {
		t.Errorf("边界外 (%s) 未报红 —— 窗口形同虚设", outEdge)
	}
}

// TestPythonTimestampWindowConstantsMatchGo 两侧窗口常量必须逐值相同 (防漂移)。
// 判据同构的前提是常量同源 —— 一侧改了另一侧没改, 两语言就会对同一次写入给出相反判定。
func TestPythonTimestampWindowConstantsMatchGo(t *testing.T) {
	src, err := os.ReadFile(pyMemoryWritePath)
	if err != nil {
		t.Skipf("出口工具不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	for _, c := range []struct {
		name string
		re   *regexp.Regexp
		want int
	}{
		{"TS_WINDOW_BACK_SEC", pyTsBackRe, tsWindowBackSec},
		{"TS_WINDOW_FWD_SEC", pyTsFwdRe, tsWindowFwdSec},
	} {
		mm := c.re.FindStringSubmatch(string(src))
		if mm == nil {
			t.Errorf("memory_write.py 里找不到 %s 常量定义 (fail-closed)", c.name)
			continue
		}
		got, _ := strconv.Atoi(mm[1])
		if got != c.want {
			t.Errorf("窗口常量漂移: memory_write.py %s=%d, Go 侧 %d", c.name, got, c.want)
		}
	}
}
