package main

// memory_budget_test.go — 记忆预算判据的判据 (DG-3 L3)
//
// 三层:
//   ① 真实仓库硬判据 —— 常驻 ≤ 预算, 折叠档案全部存在 (真数据, 不是合成样本)
//   ② 合成样本向量 —— 每条判据都要有正向样本 (否则「判据恒真」不可能被发现)
//   ③ 口径自证 —— Go 的紧凑 JSON 长度必须与 Python 口径一致 (关 HTML 转义)

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func budgetFixture(t *testing.T, m map[string]interface{}) string {
	t.Helper()
	d := t.TempDir()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "memory.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	return d
}

// 真实记忆必须过全部硬判据 (债务可见的前提: 绿就是真绿)。
func TestMemoryBudgetRealMemoryPassesHardRules(t *testing.T) {
	wd := testRoot(t)
	hard, soft := memoryBudgetCheck(wd)
	if len(hard) > 0 {
		t.Fatalf("真实记忆违反硬判据:\n  %s", strings.Join(hard, "\n  "))
	}
	t.Logf("真实记忆: 硬判据全过, 软告警 %d 条 %v", len(soft), soft)
	data, _, _ := LoadMemory(wd)
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	n := memoryResidentChars(m)
	t.Logf("常驻字符 = %d (预算 %d, 余量 %d)", n, memoryResidentBudget, memoryResidentBudget-n)
	if n <= 0 {
		t.Fatal("常驻字符算成 0 —— 口径写反或清单为空")
	}
}

// B1 正向样本: 超预算必须报硬问题。
func TestMemoryBudgetResidentOverBudgetDetected(t *testing.T) {
	big := strings.Repeat("字", memoryResidentBudget+100)
	d := budgetFixture(t, map[string]interface{}{"identity": "t", "architecture": big})
	hard, _ := memoryBudgetCheck(d)
	if !budgetHasIssue(hard, "常驻字符") {
		t.Fatalf("超预算未报: %v", hard)
	}
}

// B1 边界: 恰好在预算内不报 (阈值方向正确)。
func TestMemoryBudgetResidentAtBudgetOK(t *testing.T) {
	// 紧凑 JSON 会加引号(2) 与转义, 故留出余量, 只断言「不超预算」这一类判据
	d := budgetFixture(t, map[string]interface{}{"identity": "t"})
	hard, _ := memoryBudgetCheck(d)
	if budgetHasIssue(hard, "常驻字符") {
		t.Fatalf("小记忆被误判超预算: %v", hard)
	}
}

// B2 正向样本: archive 指向不存在的文件 → 硬问题 (折叠假象)。
func TestMemoryBudgetDeadArchiveDetected(t *testing.T) {
	d := budgetFixture(t, map[string]interface{}{
		"identity": "t",
		"folded_memory": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"id": "fold_x", "archive": "_archive/folded_memory/nope.md"},
			},
		},
	})
	hard, _ := memoryBudgetCheck(d)
	if !budgetHasIssue(hard, "nope.md") {
		t.Fatalf("死引用未报: %v", hard)
	}
}

// B2 反向样本: archive 存在 → 不报 (防「见到折叠就报」的假红)。
func TestMemoryBudgetLiveArchiveOK(t *testing.T) {
	d := budgetFixture(t, map[string]interface{}{
		"identity": "t",
		"folded_memory": map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"id": "fold_y", "archive": "_archive/folded_memory/live.md"},
			},
		},
	})
	p := filepath.Join(d, "_archive", "folded_memory", "live.md")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	hard, _ := memoryBudgetCheck(d)
	if budgetHasIssue(hard, "live.md") {
		t.Fatalf("有效引用被误报: %v", hard)
	}
}

// B3 软告警: lessons 超上限只进软清单 (不是硬判据 —— 债务可见, 但不阻断)。
func TestMemoryBudgetLessonsCeilingIsSoft(t *testing.T) {
	items := make([]interface{}, 0, memoryLessonsCeiling+1)
	for i := 0; i <= memoryLessonsCeiling; i++ {
		items = append(items, map[string]interface{}{"title": "t", "content": "c"})
	}
	d := budgetFixture(t, map[string]interface{}{"identity": "t", "lessons": items})
	hard, soft := memoryBudgetCheck(d)
	if budgetHasIssue(hard, "lessons") {
		t.Fatalf("lessons 条数应为软告警, 不该进硬清单: %v", hard)
	}
	if !budgetHasIssue(soft, "lessons") {
		t.Fatalf("超上限未告警: %v", soft)
	}
}

// 口径自证: 关 HTML 转义 —— 否则 < > & 被转成 \u003c, 与 Python 侧算出两个数。
func TestCompactJSONLenNoHTMLEscape(t *testing.T) {
	got := compactJSONLen("<sword_furnace> & co")
	want := len(`"<sword_furnace> & co"`)
	if got != want {
		t.Fatalf("紧凑长度 = %d, want %d (HTML 转义没关?)", got, want)
	}
}

// 口径自证 (二): 数 rune 不数字节 —— 中文 1 字符 = 3 字节, 数字节等于给中文记三倍账。
// 实测事故 (20261012): Go 侧数字节报 18289, Python 侧数 rune 报 10464, 同一份数据两个数。
func TestCompactJSONLenCountsRunesNotBytes(t *testing.T) {
	got := compactJSONLen("中文")
	// 2 引号 + 2 字符 = 4 (若数字节则是 2 + 6 = 8)
	const want = 4
	if got != want {
		t.Fatalf("紧凑长度 = %d, want %d (数了字节?)", got, want)
	}
}

// B2 正向样本 (二): 正文里的裸路径死引用 —— 旧口径只查 items[].archive,
// 而实测 20261013 记忆里另有 2 处正文引用同样死了却报不出来 (漏检无人管)。
func TestMemoryBudgetDeadRefInProseDetected(t *testing.T) {
	d := budgetFixture(t, map[string]interface{}{
		"identity": "t",
		"defense":  "细节见 _archive/folded_memory/defense_details_20261012.md",
	})
	hard, _ := memoryBudgetCheck(d)
	if !budgetHasIssue(hard, "defense_details_20261012.md") {
		t.Fatalf("正文死引用未报: %v", hard)
	}
}

// B2 正向样本 (三): 反斜杠形态 (Windows 写法) 同样要认。
func TestMemoryBudgetDeadRefBackslashDetected(t *testing.T) {
	d := budgetFixture(t, map[string]interface{}{
		"identity": "t",
		"defense":  `见 _archive\folded_memory\gone_20260101.md`,
	})
	hard, _ := memoryBudgetCheck(d)
	if !budgetHasIssue(hard, "gone_20260101.md") {
		t.Fatalf("反斜杠形态未报: %v", hard)
	}
}

// B2 反向样本 (二): 目录形态引用与纯文字提及不算引用 (防「见到 _archive 就报」)。
func TestMemoryBudgetDirMentionNotAReference(t *testing.T) {
	d := budgetFixture(t, map[string]interface{}{
		"identity": "t",
		"defense":  "红牌 3 天 -> _archive/hygiene_quarantine (只移不删); _archive/是历史归档",
	})
	hard, _ := memoryBudgetCheck(d)
	if budgetHasIssue(hard, "档案引用") {
		t.Fatalf("目录形态被误报为引用: %v", hard)
	}
}

// B2 口径自证: 尾随中文标点 —— 白名单字符集必须在句号处停下。
// 黑名单实现会把「。」吃进路径, 于是路径永不存在 -> 恒假红。
func TestMemoryBudgetRefStopsAtCJKPunct(t *testing.T) {
	d := budgetFixture(t, map[string]interface{}{
		"identity": "t",
		"defense":  "见 _archive/x/ghost_20260101.md。",
	})
	hard, _ := memoryBudgetCheck(d)
	if !budgetHasIssue(hard, "ghost_20260101.md") {
		t.Fatalf("尾随中文标点导致漏检: %v", hard)
	}
	if budgetHasIssue(hard, "ghost_20260101.md。") {
		t.Fatalf("句号被吃进路径 (假红): %v", hard)
	}
}

// B3 口径: static 是永久事实, 不受池上限约束 —— 旧口径数全部条数,
// 而上限 60 < 实测 static 61, 告警恒亮 = 狼来了。两条样本必须一正一反,
// 否则「改成恒不告警」也能全绿。
func TestMemoryBudgetLessonsCeilingCountsDatedOnly(t *testing.T) {
	static := make([]interface{}, 0, memoryLessonsCeiling+1)
	dated := make([]interface{}, 0, memoryLessonsCeiling+1)
	for i := 0; i <= memoryLessonsCeiling; i++ {
		static = append(static, map[string]interface{}{"content": "c", "ts": "static"})
		dated = append(dated, map[string]interface{}{"content": "c", "ts": "20260101"})
	}
	_, soft := memoryBudgetCheck(budgetFixture(t, map[string]interface{}{"identity": "t", "lessons": static}))
	if budgetHasIssue(soft, "lessons") {
		t.Fatalf("static 条目被池上限约束 (旧口径回归): %v", soft)
	}
	_, soft2 := memoryBudgetCheck(budgetFixture(t, map[string]interface{}{"identity": "t", "lessons": dated}))
	if !budgetHasIssue(soft2, "lessons") {
		t.Fatalf("dated 超上限未告警: %v", soft2)
	}
}

// 跨语言同源: Python 侧判据必须与 Go 侧**同行为** (同型向量各跑一遍)。
// 教训来源「跨语言同构不能只校字段名, 必须校行为」: 20261003 实测
// memory_write.py 与 Go 侧对同一次写入给出不同判定, 而字段清单哨兵全绿。
func TestMemoryBudgetPythonSelftest(t *testing.T) {
	cmd := exec.Command(guardGatePython(), "-u",
		filepath.Join("defense_system", "memory_budget.py"), "--selftest")
	cmd.Dir = testRoot(t)
	// pythonUTF8Env: 强制 UTF-8 输出 + 不留 __pycache__。
	// 教训 (20261002 同源事故): 只设 PYTHONDONTWRITEBYTECODE 时, Windows 上
	// 子进程 stdout 按系统代码页(cp936)编码, 中文断言永远为假 -> 判据假红。
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python 自检失败: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "失败 0") {
		t.Fatalf("python 自检未全过:\n%s", out)
	}
}

func budgetHasIssue(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestMemoryBudgetDenyWired 判据必须真的跑在入口上 (T53, 20261014)。
//
// 只测 memoryBudgetCheck 会全绿 —— 而它曾被链接器 DCE 整棵剔除 (生产零调用),
// 测试全绿给的是「已经守住」的错觉。必须钉住 preflightDeny 里调用了
// memoryBudgetDeny。用 AST 口径: 文本断言会被注释里的同名串骗过。
//
// 变异自检: 把 preflight.go 里 preflightDeny 的 memoryBudgetDeny 调用删掉,
// 本用例报红。
func TestMemoryBudgetDenyWired(t *testing.T) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "preflight.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("解析 preflight.go 失败: %v", err)
	}
	found := false
	ast.Inspect(af, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "preflightDeny" || fd.Body == nil {
			return true
		}
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			if se, ok := m.(*ast.SelectorExpr); ok && se.Sel.Name == "memoryBudgetDeny" {
				found = true
			}
			return true
		})
		return false
	})
	if !found {
		t.Error("preflightDeny 未接线 memoryBudgetDeny —— 判据退回「写了但不跑」(T53 复发)")
	}
}
