package main

// axiom_carrier_test.go — 公理↔载体双向映射哨兵 (20261004, P3-3)
//
// 背景: 全仓 30 个文件在注释里散乱提及公理编号, 但【没有任何结构化映射】——
// 正向问「公理四由谁守」无答案(实测 20261004: 全仓只有本组哨兵自身引用过公理四,
// 真实载体数 0); 反向问「这个文件守哪条公理」只能靠人读注释。而注释里的引用会腐化
// (同批实测: anchor_fields_sentinel_test.go 里有一处省略式并列引用, 第二个编号是幽灵编号,
// 而 axiom_ref_sentinel_test.go 全绿 —— 旧正则抓不到省略式并列, 已同批修)。
//
// 处置: 把映射固化为单一数据源 defense_system/axiom_carriers.json + 本哨兵判定。
// 判据 (公理三: 判据自身也要有判据 —— 六类变异双向钉住, 见 MutationSelfCheck):
//   ① 清单可解析, 且 memory.json 能解析出公理定义 (fail-closed)
//   ② 清单编号 ∈ memory.json 合法集合 (幽灵编号报红)
//   ③ memory.json 每条公理都在清单登记 (无载体公理报红)
//   ④ 载体文件必须存在 (改名/删除腐化报红)
//   ⑤ 每条必填字段齐全 (file/role/kind), 且每条公理 >=1 个 anchor 命中的载体
//   ⑥ anchor 非空时: 必须与所属公理一致, 且文件内确实含该字面串
//   ⑦ 清单文件本身必须被 git 跟踪 (改动可见 / 可回滚 —— 公理四)
//   ⑧ role 描述里的"数字+单位"必须在载体文件内可复现 (描述过期也是腐化)
//
// 单一数据源: 合法编号从 memory.json 现场解析, 不硬编码 (硬编码会在下次改公理时
// 腐化 —— 正是本哨兵要防的病)。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const axiomCarrierManifestPath = "defense_system/axiom_carriers.json"

type axiomCarrierItem struct {
	File   string `json:"file"`
	Kind   string `json:"kind"`
	Anchor string `json:"anchor"`
	Role   string `json:"role"`
}

type axiomCarrierGroup struct {
	Num      string             `json:"num"`
	Theme    string             `json:"theme"`
	Carriers []axiomCarrierItem `json:"carriers"`
}

type axiomCarrierManifest struct {
	Version int                 `json:"version"`
	Doc     string              `json:"doc"`
	Axioms  []axiomCarrierGroup `json:"axioms"`
}

// axiomNumbersFromText 从 axioms 正文解析合法编号集合 (定义行 = "公理X:" )。
// 复用 axiom_ref_sentinel_test.go 的 axiomDefRe —— 两处口径必须同一份, 防漂移。
func axiomNumbersFromText(text string) map[string]bool {
	out := map[string]bool{}
	for _, mm := range axiomDefRe.FindAllStringSubmatch(text, -1) {
		out[mm[1]] = true
	}
	return out
}

// roleNumberRe 描述里的"数字+单位" (只认可复现的量: 字符/行/秒/ms)。
// 不认"倍/次"这类修辞性数字, 避免误报 —— 判据宁可窄也不能吵。
var roleNumberRe = regexp.MustCompile(`(\d+)\s*(字符|行|秒|ms)`)

// roleStaleNumbers 返回 role 里写了、但载体文件里找不到的数字 (描述过期的证据)。
//
// 动机 (20261004): 只钉 anchor 字面串的判据看不见"描述过期" —— 实测
// axiom_carriers.json 里 agent_memory.go 的 role 写着"固定头 4200 字符预算",
// 而同日 prompt 已瘦身到 4000, 过期描述静默存活。能钉语义的就不该只钉字符串。
func roleStaleNumbers(role, body string) []string {
	var out []string
	for _, mm := range roleNumberRe.FindAllStringSubmatch(role, -1) {
		if !strings.Contains(body, mm[1]) {
			out = append(out, mm[0])
		}
	}
	return out
}

// axiomCarrierProblems 返回清单的全部问题; 空切片 = 健康。
// 纯函数(输入=字节/文本/读取器, 无全局状态) —— 变异自检得以喂伪造输入。
func axiomCarrierProblems(manifestJSON []byte, axiomsText string, readFile func(string) (string, bool)) []string {
	var probs []string
	var mf axiomCarrierManifest
	if err := json.Unmarshal(manifestJSON, &mf); err != nil {
		return []string{"清单不可解析: " + err.Error()}
	}
	if len(mf.Axioms) == 0 {
		return []string{"清单 axioms 为空 (fail-closed)"}
	}
	legal := axiomNumbersFromText(axiomsText)
	if len(legal) == 0 {
		return []string{"memory.json.axioms 未解析出任何公理定义 (格式变化? fail-closed 中止)"}
	}
	seen := map[string]bool{}
	for _, g := range mf.Axioms {
		if strings.TrimSpace(g.Num) == "" {
			probs = append(probs, "清单条目 num 为空")
			continue
		}
		if seen[g.Num] {
			probs = append(probs, "重复登记: 公理"+g.Num)
		}
		seen[g.Num] = true
		if !legal[g.Num] {
			probs = append(probs, "幽灵编号: 公理"+g.Num+" 不在 memory.json 定义内")
		}
		if strings.TrimSpace(g.Theme) == "" {
			probs = append(probs, "公理"+g.Num+" theme 为空")
		}
		if len(g.Carriers) == 0 {
			probs = append(probs, "无载体: 公理"+g.Num+" 载体清单为空")
			continue
		}
		anchored := 0
		for i, c := range g.Carriers {
			tag := fmt.Sprintf("公理%s 载体#%d[%s]", g.Num, i+1, c.File)
			if strings.TrimSpace(c.File) == "" {
				probs = append(probs, tag+" file 为空")
				continue
			}
			if strings.TrimSpace(c.Role) == "" {
				probs = append(probs, tag+" role 为空 (占位载体)")
			}
			if c.Kind != "judge" && c.Kind != "process" {
				probs = append(probs, tag+" kind 非法 (须 judge|process): "+c.Kind)
			}
			body, ok := readFile(c.File)
			if !ok {
				probs = append(probs, tag+" 载体文件不存在 (改名/删除腐化)")
				continue
			}
			if c.Anchor == "" {
				continue // 显式承认的隐式载体: 只校验存在性
			}
			if !strings.Contains(c.Anchor, "公理"+g.Num) {
				probs = append(probs, tag+" anchor 与所属公理不一致: "+c.Anchor)
				continue
			}
			if !strings.Contains(body, c.Anchor) {
				probs = append(probs, tag+" 文件内找不到引用锚 "+c.Anchor)
				continue
			}
			// ⑧ 描述真实性: role 里带单位的数字必须在载体文件里可复现。
			// 描述与代码是两份东西, 代码改了描述不会自动跟着改 —— 只有判据能发现。
			for _, stale := range roleStaleNumbers(c.Role, body) {
				probs = append(probs, tag+" role 描述过期: "+stale+" 在文件内不存在")
			}
			anchored++
		}
		if anchored == 0 {
			probs = append(probs, "无可验证锚点: 公理"+g.Num+" 至少需 1 个 anchor 命中的载体")
		}
	}
	var missing []string
	for n := range legal {
		if !seen[n] {
			missing = append(missing, "公理"+n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		probs = append(probs, "无载体公理: "+strings.Join(missing, "/")+" 未在清单登记")
	}
	sort.Strings(probs)
	return probs
}

// repoAxiomFileReader 相对仓库根读取载体文件。
func repoAxiomFileReader(wd string) func(string) (string, bool) {
	return func(rel string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(wd, filepath.FromSlash(rel)))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

// loadAxiomCarrierFixture 取真实清单 + 真实 axioms 正文 + 真实读取器。
func loadAxiomCarrierFixture(t *testing.T, wd string) ([]byte, string, func(string) (string, bool)) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(wd, axiomCarrierManifestPath))
	if err != nil {
		t.Skipf("清单不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	memRaw, err := os.ReadFile(filepath.Join(wd, "memory.json"))
	if err != nil {
		t.Skipf("无 memory.json (%v) —— 非主仓库环境", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(memRaw, &m); err != nil {
		t.Fatalf("memory.json 不可解析: %v", err)
	}
	axText, _ := m["axioms"].(string)
	return raw, axText, repoAxiomFileReader(wd)
}

// TestAxiomCarrier_ManifestHealthy 真清单必须零问题, 并打印完整映射表 ——
// 这张表是人工独立验收的抓手 (正向: 公理->载体; 反向: 载体->公理)。
func TestAxiomCarrier_ManifestHealthy(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, axText, read := loadAxiomCarrierFixture(t, wd)
	probs := axiomCarrierProblems(raw, axText, read)
	if len(probs) > 0 {
		t.Fatalf("公理↔载体映射存在问题 (共 %d 条):\n  %s", len(probs), strings.Join(probs, "\n  "))
	}
	var mf axiomCarrierManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	t.Log("── 正向: 公理 → 载体 ──")
	total, anchored := 0, 0
	for _, g := range mf.Axioms {
		var names []string
		a := 0
		for _, c := range g.Carriers {
			names = append(names, c.File)
			if c.Anchor != "" {
				a++
			}
		}
		total += len(g.Carriers)
		anchored += a
		t.Logf("  公理%s [%s] %d 载体 (anchor %d): %s", g.Num, g.Theme, len(g.Carriers), a, strings.Join(names, ", "))
	}
	t.Log("── 反向: 载体 → 公理 ──")
	rev := map[string][]string{}
	for _, g := range mf.Axioms {
		for _, c := range g.Carriers {
			rev[c.File] = append(rev[c.File], "公理"+g.Num)
		}
	}
	files := make([]string, 0, len(rev))
	for f := range rev {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		sort.Strings(rev[f])
		t.Logf("  %-40s → %s", f, strings.Join(rev[f], " + "))
	}
	t.Logf("合计 %d 条公理 / %d 个载体 / %d 个带文本锚点", len(mf.Axioms), total, anchored)
}

// TestAxiomCarrier_MutationSelfCheck 判据的判据: 六类注入必须各自报红, 健康输入必须绿。
// 任何一条变异后仍绿 = 该判据臂不存在 (探针精神: 检测到 ≠ 拦得住)。
func TestAxiomCarrier_MutationSelfCheck(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, axText, read := loadAxiomCarrierFixture(t, wd)

	if probs := axiomCarrierProblems(raw, axText, read); len(probs) != 0 {
		t.Fatalf("⑥ 健康输入被判有问题 (基线不绿, 其余变异无意义): %v", probs)
	} else {
		t.Log("⑥ 健康输入 → 零问题 (绿) ✓")
	}

	mutate := func(f func(*axiomCarrierManifest)) []byte {
		var mf axiomCarrierManifest
		if err := json.Unmarshal(raw, &mf); err != nil {
			t.Fatal(err)
		}
		f(&mf)
		out, err := json.Marshal(&mf)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	// ① 删掉某条公理条目 → 无载体公理
	firstNum := axiomFirstNum(t, raw)
	m1 := mutate(func(m *axiomCarrierManifest) {
		var keep []axiomCarrierGroup
		for _, g := range m.Axioms {
			if g.Num != firstNum {
				keep = append(keep, g)
			}
		}
		m.Axioms = keep
	})
	assertAxiomProbs(t, "① 删条目", axiomCarrierProblems(m1, axText, read), "无载体公理: 公理"+firstNum)

	// ② 载体 file 改成不存在的名字 → 载体文件不存在
	m2 := mutate(func(m *axiomCarrierManifest) {
		m.Axioms[0].Carriers[0].File = "no_such_carrier_2099.go"
	})
	assertAxiomProbs(t, "② 文件改名", axiomCarrierProblems(m2, axText, read), "载体文件不存在")

	// ③ role 置空 → 占位载体
	m3 := mutate(func(m *axiomCarrierManifest) {
		m.Axioms[0].Carriers[0].Role = ""
	})
	assertAxiomProbs(t, "③ role 置空", axiomCarrierProblems(m3, axText, read), "role 为空")

	// ④ 编号改成幽灵编号 → 幽灵编号 + 原公理无载体
	m4 := mutate(func(m *axiomCarrierManifest) {
		m.Axioms[0].Num = "五"
	})
	assertAxiomProbs(t, "④ 幽灵编号", axiomCarrierProblems(m4, axText, read), "幽灵编号", "无载体公理: 公理"+firstNum)

	// ⑤ 载体文件内容注入(抹掉引用锚) → 文件内找不到引用锚
	target, anchor := axiomFirstAnchoredCarrier(t, raw)
	if target == "" {
		t.Fatal("清单里找不到任何带 anchor 的载体, 无法验证 ⑤")
	}
	fakeRead := func(rel string) (string, bool) {
		if rel == target {
			return "package main\n// 人为抹掉引用锚的内容\n", true
		}
		return read(rel)
	}
	assertAxiomProbs(t, "⑤ 抹掉引用锚", axiomCarrierProblems(raw, axText, fakeRead), "文件内找不到引用锚 "+anchor)

	// ⑧ role 里的数字过期 (载体文件内不存在) → 描述真实性判据报红
	m8 := mutate(func(m *axiomCarrierManifest) {
		m.Axioms[0].Carriers[0].Role = "固定头 99999 字符预算 (人为过期)"
	})
	assertAxiomProbs(t, "⑧ 描述数字过期", axiomCarrierProblems(m8, axText, read), "role 描述过期")

	// ⑦ axioms 正文解析不出定义 → fail-closed (不能静默判绿)
	assertAxiomProbs(t, "⑦ 定义解析失败", axiomCarrierProblems(raw, "没有定义行的正文", read), "未解析出任何公理定义")
}

// TestAxiomCarrier_ManifestTracked 清单文件本身必须被 git 跟踪 ——
// 否则改动不可见、不可回滚, 与公理四(git=安全网)相悖。
func TestAxiomCarrier_ManifestTracked(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, axiomCarrierManifestPath)); err != nil {
		t.Skipf("清单不在位 (%v) —— 开源仓库不含 defense_system, 跳过", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用: %v", err)
	}
	out, err := exec.Command("git", "-C", wd, "ls-files", "--error-unmatch", axiomCarrierManifestPath).CombinedOutput()
	if err != nil {
		t.Fatalf("清单未被 git 跟踪 (改动不可见/不可回滚, 违反公理四): %v\n%s", err, out)
	}
	t.Logf("清单已被 git 跟踪: %s", strings.TrimSpace(string(out)))
}

func axiomFirstNum(t *testing.T, raw []byte) string {
	t.Helper()
	var mf axiomCarrierManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	if len(mf.Axioms) == 0 {
		t.Fatal("清单无条目")
	}
	return mf.Axioms[0].Num
}

func axiomFirstAnchoredCarrier(t *testing.T, raw []byte) (string, string) {
	t.Helper()
	var mf axiomCarrierManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	for _, g := range mf.Axioms {
		for _, c := range g.Carriers {
			if c.Anchor != "" {
				return c.File, c.Anchor
			}
		}
	}
	return "", ""
}

func assertAxiomProbs(t *testing.T, name string, probs []string, wantSubstrs ...string) {
	t.Helper()
	if len(probs) == 0 {
		t.Fatalf("%s: 期望报红, 实得零问题 (该判据臂无鉴别力)", name)
	}
	for _, w := range wantSubstrs {
		found := false
		for _, p := range probs {
			if strings.Contains(p, w) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s: 期望问题含 %q, 实得 %v", name, w, probs)
		}
	}
	t.Logf("%s → 报红 %d 条 (含 %q) ✓", name, len(probs), wantSubstrs[0])
}
