package main

// gates_consistency_test.go — gate 清单/表/展示层一致性 + 判定函数覆盖 (20260913)
//
// 背景: gate 清单原有多份独立副本 (gatesync.go currentGates / memory_health.go
// 体检局部 cur / forge.go 铸剑炉_COMPILERS 的 key / main_commands.go /tools 展示名)。
// 已统一为唯一源 铸剑炉_GATES; 本文件把"表与清单不得脱节"固化为死程序判定。
// 另补齐若干 0% 覆盖的确定性判定函数 (compilerTimeout /
// looksLikeValidGoTopLevel / 两个时段入口包装)。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// 哨兵: 铸剑炉_COMPILERS 的 key 集合必须与唯一源 铸剑炉_GATES 完全一致。
// 脱节后果: 新 gate 无超时/执行命令配置 → 静默用默认 30s; 或表里留幽灵 gate。
func TestGatesList_MatchesCompilerTable(t *testing.T) {
	list := map[string]bool{}
	for _, g := range 铸剑炉_GATES {
		if list[g] {
			t.Fatalf("铸剑炉_GATES 有重复项: %s", g)
		}
		list[g] = true
		if _, ok := 铸剑炉_COMPILERS[g]; !ok {
			t.Errorf("清单里的 %s 在 铸剑炉_COMPILERS 表中缺失 (无超时/执行配置)", g)
		}
	}
	for k := range 铸剑炉_COMPILERS {
		if !list[k] {
			t.Errorf("表里有 %s 但不在 铸剑炉_GATES 清单中 (幽灵 gate)", k)
		}
	}
	if len(铸剑炉_GATES) != 14 {
		t.Errorf("当前应为 14 面 gate (sh 已于 20261001 退役; 20261002 新增 task 长任务通道), 实际 %d", len(铸剑炉_GATES))
	}
}

// 哨兵: /tools 展示层与清单同集合 (展示名可带别名, 如 sh/bash)。
func TestGateDisplayNames_MatchGatesList(t *testing.T) {
	shown := map[string]bool{}
	for _, d := range gateDisplayNames {
		base := strings.Split(d, "/")[0]
		shown[base] = true
	}
	for _, g := range 铸剑炉_GATES {
		if !shown[g] {
			t.Errorf("/tools 展示缺 gate: %s", g)
		}
	}
	for b := range shown {
		found := false
		for _, g := range 铸剑炉_GATES {
			if g == b {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("/tools 展示了不存在的 gate: %s", b)
		}
	}
	if len(gateDisplayNames) != len(铸剑炉_GATES) {
		t.Errorf("展示项数 %d 与清单 %d 不一致", len(gateDisplayNames), len(铸剑炉_GATES))
	}
}

func TestCompilerTimeout(t *testing.T) {
	for _, g := range 铸剑炉_GATES {
		want := 30 * time.Second
		if c, ok := 铸剑炉_COMPILERS[g]; ok && c.ExecTimeout > 0 {
			want = c.ExecTimeout
		}
		if got := compilerTimeout(g); got != want {
			t.Errorf("compilerTimeout(%s) = %v, want %v", g, got, want)
		}
		if compilerTimeout(g) <= 0 {
			t.Errorf("compilerTimeout(%s) 必须为正", g)
		}
	}
	if got := compilerTimeout("no_such_gate"); got != 30*time.Second {
		t.Errorf("未知语言应回退 30s, 实际 %v", got)
	}
}

func TestExeSuffix(t *testing.T) {
	got := exeSuffix()
	if got != ".exe" && got != "" {
		t.Fatalf("exeSuffix 只能是 .exe 或空串, 实际 %q", got)
	}
	if runtime.GOOS == "windows" && got != ".exe" {
		t.Fatalf("Windows 上应为 .exe, 实际 %q", got)
	}
}

func TestLooksLikeValidGoTopLevel(t *testing.T) {
	pos := []string{
		"func main() {}",
		"type T struct{}",
		"var x = 1",
		"const A = 1",
		`import "fmt"`,
		"package main",
		"// 注释开头",
		"/* 块注释 */",
		"  func f() {}",
	}
	for _, c := range pos {
		if !looksLikeValidGoTopLevel(c) {
			t.Errorf("looksLikeValidGoTopLevel(%q) 应为 true", c)
		}
	}
	neg := []string{"", "   ", "fmt.Println(1)", "x := 1", "for i := 0; i < 3; i++ {}"}
	for _, c := range neg {
		if looksLikeValidGoTopLevel(c) {
			t.Errorf("looksLikeValidGoTopLevel(%q) 应为 false", c)
		}
	}
}

// 入口包装必须与纯函数同判据 —— 包装写错时纯函数测试仍全绿, 生产行为却错。
func TestPeakHourEntry_MatchesPureFunc(t *testing.T) {
	if isPeakHour() != isPeakHourAt(time.Now()) {
		if isPeakHour() != isPeakHourAt(time.Now()) { // 容忍跨小时边界
			t.Fatal("isPeakHour() 与 isPeakHourAt(now) 不一致 (入口包装写错?)")
		}
	}
}

// 哨兵: tool schema 的 lang.enum 必须与唯一源 铸剑炉_GATES 完全一致 (20261002)
//
// 动机: enum 是给 LLM 的「邀请函」—— 列着就是在邀请模型用它, 漏项即该 gate 对
// 模型事实上不可达。原为 11 项硬编码副本 (缺 relation/media) 而四套守卫全绿:
// gates_consistency_test 查「表 ↔ 清单」、sh_retired_test 只查「enum 无 sh」——
// 无一条判据查「enum 覆盖全部 gate」。缺口存在 = 判据缺席, 非「没人忘」。
// 修法: enum 直接引用唯一源 (副本消失 → 不可能漂), 本哨兵钉住该引用关系。
func TestForgeToolSchema_EnumMatchesGatesList(t *testing.T) {
	var doc struct {
		Function struct {
			Parameters struct {
				Properties struct {
					Lang struct {
						Enum []string `json:"enum"`
					} `json:"lang"`
				} `json:"properties"`
			} `json:"parameters"`
		} `json:"function"`
	}
	raw := ForgeToolSchema()
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("ForgeToolSchema 非法 JSON: %v", err)
	}
	got := doc.Function.Parameters.Properties.Lang.Enum
	if len(got) == 0 {
		t.Fatal("schema 缺 lang.enum —— 邀请函整体缺失")
	}

	// ① 不得有重复项
	seen := map[string]bool{}
	for _, g := range got {
		if seen[g] {
			t.Errorf("enum 有重复项: %s", g)
		}
		seen[g] = true
	}

	// ② 必须覆盖唯一源全部 gate (缺一面 = 该面对 LLM 不可达)
	want := map[string]bool{}
	for _, g := range 铸剑炉_GATES {
		want[g] = true
		if !seen[g] {
			t.Errorf("enum 缺 gate: %s —— 该面对 LLM 不可达 (邀请函漏项)", g)
		}
	}

	// ③ 不得含清单外的幽灵项
	for _, g := range got {
		if !want[g] {
			t.Errorf("enum 含清单外 gate: %s (幽灵项)", g)
		}
	}

	// ④ 面数对齐 (兜底: 上面两条都过时长度必然相等, 保留作独立断言)
	if len(got) != len(铸剑炉_GATES) {
		t.Errorf("enum 面数 %d 与唯一源 %d 不一致", len(got), len(铸剑炉_GATES))
	}
}

// 哨兵: tool schema 的 lang.description 不得复述 gate 规则 (20261013)
//
// 动机: description 是 gateRegistry 的手写副本, 已实测漂移 —— 它列 8 个 gate
// (math/logic/regex/knowledge/tcm/browser/self/chain), 而注册表有 11 个
// (缺 relation/task/media)。与上一条 enum 缺陷同型: 副本存在 → 必然漂。
// gate 选用规则单点在 <gates_enabled> (由 describeGates 生成, 受 promptMapBudgetRunes
// 预算管控); description 只保留 enum 无法表达的信息 (默认语言/自动检测/退役说明)。
// 本哨兵钉住"副本不得复现"。
func TestForgeToolSchema_LangDescriptionNoGateNames(t *testing.T) {
	raw := ForgeToolSchema()
	var doc struct {
		Function struct {
			Parameters struct {
				Properties struct {
					Lang struct {
						Description string `json:"description"`
					} `json:"lang"`
				} `json:"properties"`
			} `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("ForgeToolSchema 非法 JSON: %v", err)
	}
	desc := doc.Function.Parameters.Properties.Lang.Description
	if desc == "" {
		t.Fatal("schema 缺 lang.description")
	}
	// 词边界匹配 (gate 名全为 ASCII 字母, 避免 self-check 这类子串误命中)
	wordAt := func(s, w string) bool {
		isWord := func(b byte) bool {
			return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
		}
		for i := 0; i+len(w) <= len(s); i++ {
			if s[i:i+len(w)] != w {
				continue
			}
			l, r := byte(' '), byte(' ')
			if i > 0 {
				l = s[i-1]
			}
			if i+len(w) < len(s) {
				r = s[i+len(w)]
			}
			if !isWord(l) && !isWord(r) {
				return true
			}
		}
		return false
	}
	for _, g := range gateNames() {
		if wordAt(desc, g) {
			t.Errorf("lang.description 复述了 gate %q —— 规则单点在 <gates_enabled>; "+
				"此处副本已实测漂移 (描述列 8 个 vs 注册表 %d 个), 副本消失才不可能漂", g, len(gateNames()))
		}
	}
	// 反向断言: 编译器名必须保留 (否则模型不知道默认执行体是什么)
	for _, c := range []string{"python", "go", "node"} {
		if !wordAt(desc, c) {
			t.Errorf("lang.description 丢了编译器 %q 的说明 (enum 之外模型仍需知道默认执行体)", c)
		}
	}
}

// 哨兵: gate_registry.go 的注释里不得再出现 gate 名 (20261013)
//
// 动机: 原注释写"只注册自托管 gate(math/logic/regex/knowledge/tcm/browser/self/chain)"
// —— 8 项手写快照, 实测已漂 (注册表 11 个)。与 lang.description 同型: 副本存在 → 必然漂。
// 注释已改为"以下方 gateRegistry 为准"; 本哨兵钉住它不再复现。
func TestGateRegistryComment_NoGateNameSnapshot(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败, 无法定位源码目录")
	}
	path := filepath.Join(filepath.Dir(thisFile), "gate_registry.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", path, err)
	}
	text := string(raw)
	// 探针自检: 扫到的是不是真源码 (路径错/内容退化时立即报红, 不静默全绿)
	if !strings.Contains(text, "var gateRegistry") {
		t.Fatalf("探针自检失败: %s 不含 var gateRegistry, 扫描目标可疑", path)
	}
	if len(gateNames()) == 0 {
		t.Fatal("探针自检失败: gateNames() 为空, 判据退化")
	}
	for ln, line := range strings.Split(text, "\n") {
		i := strings.Index(line, "//")
		if i < 0 {
			continue
		}
		comment := line[i:]
		for _, g := range gateNames() {
			if strings.Contains(comment, g) {
				t.Errorf("gate_registry.go:%d 注释里出现 gate 名 %q —— 手写清单必漂; "+
					"清单唯一源是 gateRegistry, 注释请勿另列", ln+1, g)
			}
		}
	}
}
