package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ============================================================================
// watchlist_wiring_test.go —— 受监控文件「单一数据源」的接线哨兵
//
// 缺陷 (20260928 实测):
//   forge_guard.py 与 selfheal.py 各维护一份 WATCH 定义, 已漂移 ——
//     forge_guard.py -> 动态收集根目录 *.go (20260910 已修)
//     selfheal.py    -> 硬编码 9 个文件名 (未修)
//   后果有两层: (1) 监控面不一致; (2) 更严重 —— selfheal.refresh_baseline_files()
//   用那份过小清单【全量重建】基线, 实测把 327 条写成 12 条 (丢失 315 条,
//   其中 defense_system 下 27 条会【永久脱离监控】), 且日志只报 evolved 数
//   -> 缩水是静默的。
//
// 修法: 抽出 defense_system/watchlist.py 作唯一数据源, 两处 import 同一对象。
//
// 本文件把三件事钉死(接线只能由死程序判定, 模型声称一律不算证据):
//   1. 两处 WATCH 必须是【同一对象】(is), 且源码里不得再有本地定义
//   2. scope 必须含 defense_system/ 下全部 .py 与判据 json
//   3. forge_baseline.json 必须【排除】—— 自指: 纳入后 "刷新基线 -> 基线自身
//      哈希变 -> 报篡改 -> 再刷新" 永不收敛
//
// 为什么是 Go 测试而非 Python 自检: 接线/归属是包级属性, 必须进入 go test
// 回归才有人跑; Python 侧自检函数无人调用就是「备而未用」。
// ============================================================================

const (
	watchlistRel = "defense_system/watchlist.py"
	guardPyRel   = "defense_system/forge_guard.py"
	selfhealRel  = "defense_system/selfheal.py"
)

// watchlistProbe 输出 JSON 事实供 Go 断言 (argv[1] = defense_system 绝对路径)。
// 只用单引号与 % 无关的写法 —— 本常量经 strings.Replace 注入路径, 不走 Sprintf。
const watchlistProbe = `import sys, json, os
ds = sys.argv[1]
sys.path.insert(0, ds)
import watchlist, forge_guard, selfheal
sf = watchlist.source_files()
snap = os.path.join(ds, "snapshots")
snapnames = []
if os.path.isdir(snap):
    dirs = sorted(os.listdir(snap), reverse=True)
    if dirs:
        snapnames = sorted(os.listdir(os.path.join(snap, dirs[0])))
flat = set()
for f in os.listdir(ds):
    if os.path.isfile(os.path.join(ds, f)):
        flat.add(f)
print(json.dumps({
    "same_object": forge_guard.WATCH is selfheal.WATCH,
    "same_source_list": forge_guard.WATCH["source"] is selfheal.WATCH["source"],
    "source_count": len(sf),
    "py": [x for x in sf if x.startswith("defense_system/") and x.endswith(".py")],
    "json": [x for x in sf if x.endswith(".json")],
    "excluded": {k: any(k in x for x in sf)
                 for k in ["forge_baseline", "__pycache__",
                           "trace_demo_report", ".bak"]},
    "has_guard": "defense_system/forge_guard.py" in sf,
    "has_selfheal": "defense_system/selfheal.py" in sf,
    "has_self": "defense_system/watchlist.py" in sf,
    "snap_intersect": sorted(set(snapnames) & flat),
}))`

// watchlistFacts 是探针输出的 JSON 事实。
type watchlistFacts struct {
	SameObject     bool            `json:"same_object"`
	SameSourceList bool            `json:"same_source_list"`
	SourceCount    int             `json:"source_count"`
	Py             []string        `json:"py"`
	JSON           []string        `json:"json"`
	Excluded       map[string]bool `json:"excluded"`
	HasGuard       bool            `json:"has_guard"`
	HasSelfheal    bool            `json:"has_selfheal"`
	HasSelf        bool            `json:"has_self"`
	SnapIntersect  []string        `json:"snap_intersect"`
}

// watchlistFactsOf 跑真实 python 探针取事实 (fail-closed: 探针失败即 t.Fatal)。
func watchlistFactsOf(t *testing.T) watchlistFacts {
	t.Helper()
	ds, err := filepath.Abs("defense_system")
	if err != nil {
		t.Fatalf("解析 defense_system 绝对路径失败: %v", err)
	}
	cmd := exec.Command(guardGatePython(), "-c", watchlistProbe, ds)
	// 与全仓其它 python 调用点同源: 显式强制 UTF-8 (pythonUTF8Env 已含 PYTHONDONTWRITEBYTECODE),
	// 不依赖外部环境 —— 否则计划任务/裸 go test 下探针输出按 GBK 落地, JSON 解析假红。
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("探针执行失败 (fail-closed, 不放行): %v\n%s", err, out)
	}
	var f watchlistFacts
	// 取【第一个】'{' 起的内容: JSON 内含嵌套对象, 用 LastIndex 会从嵌套的
	// '{' 处截断 -> "invalid character ',' after top-level value" (实测踩过)。
	body := out
	if i := bytes.IndexByte(out, '{'); i >= 0 {
		body = out[i:]
	}
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("探针输出不是合法 JSON (fail-closed): %v\n%s", err, out)
	}
	return f
}

// TestWatchlistSingleSource 钉住「两处 import 同一对象」。
func TestWatchlistSingleSource(t *testing.T) {
	t.Parallel()
	f := watchlistFactsOf(t)
	if !f.SameObject {
		t.Errorf("forge_guard.WATCH 与 selfheal.WATCH 不是同一对象 —— 单一数据源失效")
	}
	if !f.SameSourceList {
		t.Errorf("两处的 source 列表不是同一对象 —— 存在第二份清单")
	}
	// 源码级: 本地定义必须已删除 (对象同一可能因偶然 import 而成立)
	for _, rel := range []string{guardPyRel, selfhealRel} {
		raw, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("读取 %s 失败 (fail-closed): %v", rel, err)
		}
		s := string(raw)
		if !strings.Contains(s, "from watchlist import") {
			t.Errorf("%s 未 import watchlist —— 接线缺失", rel)
		}
		if strings.Contains(s, "def _source_files") {
			t.Errorf("%s 仍有本地 _source_files 定义 —— 旧实现未删", rel)
		}
		if strings.Contains(s, "\"source\": [\"main.go\"") {
			t.Errorf("%s 仍有硬编码 source 清单 —— 旧实现未删", rel)
		}
	}
	if _, err := os.Stat(watchlistRel); err != nil {
		t.Errorf("单一数据源 %s 不存在: %v", watchlistRel, err)
	}
}

// TestWatchlistScope 钉住监控面: 必须覆盖 defense_system 自身。
func TestWatchlistScope(t *testing.T) {
	t.Parallel()
	f := watchlistFactsOf(t)
	if len(f.Py) < 20 {
		t.Errorf("defense_system 下受监控 .py 仅 %d 个 (期望 >=20) —— 防御系统自身在监控盲区", len(f.Py))
	}
	if !f.HasGuard || !f.HasSelfheal || !f.HasSelf {
		t.Errorf("关键脚本未纳入监控: guard=%v selfheal=%v watchlist=%v",
			f.HasGuard, f.HasSelfheal, f.HasSelf)
	}
	for _, want := range []string{"defense_system/config.json", "defense_system/hygiene_whitelist.json",
		"defense_system/hygiene_vectors.json", "defense_system/hygiene_forge_manifest.json",
		"defense_system/axiom_carriers.json"} {
		found := false
		for _, g := range f.JSON {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("判据 json 未纳入监控: %s (被改会静默改变哨兵判定)", want)
		}
	}
}

// TestWatchlistExcludesSelfReference 钉住排除项 (自指死循环 + 运行时产物)。
func TestWatchlistExcludesSelfReference(t *testing.T) {
	t.Parallel()
	f := watchlistFactsOf(t)
	for k, in := range f.Excluded {
		if in {
			t.Errorf("排除项 %q 仍在监控列表 —— 会导致自指死循环或噪音", k)
		}
	}
	// 反向: 快照文件名与受监控文件的 basename 不得有交集 ——
	// is_rolled_back() 用 basename 在快照里找同名文件, 有交集即误判回滚污染。
	if len(f.SnapIntersect) != 0 {
		t.Errorf("快照 basename 与受监控文件交集非空: %v —— is_rolled_back 会误判", f.SnapIntersect)
	}
	if f.SourceCount < 20 {
		t.Errorf("source 总数 %d 异常偏小", f.SourceCount)
	}
	// 排除项本身也必须钉住: 实测 M2(单删 EXCLUDE_SUBSTR 里的 forge_baseline)
	// 行为测试【不报红】—— 因为 forge_baseline.json 本就不在 CRITERIA_JSON 里,
	// 第二层接住了。这是双保险的正常表现, 但也意味着"判据被改"这件事不可见。
	// 故此处静态钉住判据文本, 让单层失效能被发现。
	raw, err := os.ReadFile(watchlistRel)
	if err != nil {
		t.Fatalf("读取 %s 失败 (fail-closed): %v", watchlistRel, err)
	}
	if !strings.Contains(string(raw), "\"forge_baseline\"") {
		t.Errorf("watchlist.py 的 EXCLUDE_SUBSTR 不再含 forge_baseline —— 自指死循环防线缺一层")
	}
}
