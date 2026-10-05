package main

// anchor_fields_sentinel_test.go — 锚点字段清单防漂移哨兵 (20260913)
//
// 背景: anchorFields 白名单需人工维护, 与实际进固定头的字段集合脱节,
// 实测漏检 6 个锚点字段 (architecture/lessons/user_profile/swordless_roadmap/
// evolution_consensus/rescue), 且漏检字段连审计都不留痕 → 长期无人发现。
// 本哨兵把"清单必须覆盖真实字段"固化为死程序判定 (公理二)。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 真实 memory.json 里的全部顶层字段必须被判为锚点改动 (动态字段除外)。
// 新增顶层字段时本测试自动覆盖 → 无需改测试即受保护 (fail-safe 语义验证)。
func TestAnchorSentinel_RealMemoryFieldsAllAnchored(t *testing.T) {
	wd, _ := os.Getwd()
	data, err := os.ReadFile(filepath.Join(wd, "memory.json"))
	if err != nil {
		t.Skip("无 memory.json (非主仓库环境), 跳过真实字段哨兵")
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("memory.json 不可解析: %v", err)
	}
	if len(m) == 0 {
		t.Fatal("memory.json 顶层为空")
	}
	var missed []string
	anchored := 0
	for k := range m {
		if isDynamicMemoryField(k) {
			continue
		}
		anchored++
		if !isAnchorChange([]string{k}) {
			missed = append(missed, k)
		}
	}
	if len(missed) > 0 {
		t.Fatalf("锚点漏检 %d 个字段 (护栏不拦其改动, 且审计不留痕): %v", len(missed), missed)
	}
	if anchored == 0 {
		t.Fatal("真实记忆里零个锚点字段, 清单方向可能写反")
	}
	t.Logf("哨兵通过: %d 个锚点字段全部被护栏捕获, %d 个动态字段排除",
		anchored, len(m)-anchored)
}

// 纯动态字段组合 → 不算锚点改动 (否则片段写入会被误拦)。
func TestAnchorSentinel_DynamicOnlyNotAnchor(t *testing.T) {
	if isAnchorChange(append([]string{}, dynamicMemoryFields...)) {
		t.Fatalf("纯动态字段组合被判为锚点: %v", dynamicMemoryFields)
	}
}

// 空差异 / nil → 不算锚点。
func TestAnchorSentinel_EmptyNotAnchor(t *testing.T) {
	if isAnchorChange(nil) {
		t.Fatal("nil 字段列表被判为锚点")
	}
	if isAnchorChange([]string{}) {
		t.Fatal("空字段列表被判为锚点")
	}
}

// 未知新字段 (将来新增的顶层键) → 必须算锚点 (fail-safe, 原白名单实现在此 fail-open)。
func TestAnchorSentinel_UnknownFieldIsAnchor(t *testing.T) {
	if !isAnchorChange([]string{"brand_new_field_2099"}) {
		t.Fatal("未知新字段未被判为锚点 —— fail-safe 失效, 新增字段将绕过护栏")
	}
}

// 消费方共用同一份清单: 每个动态字段都必须被 buildMemoryTailText 剔除
// (防清单与实际剔除行为分叉)。
// 20260925: 原第二实现 stripDynamicMemory 生产零调用(死代码), 已删 ——
// 它在时本测试看似"双实现交叉验证", 实为给死代码背书。
func TestAnchorSentinel_AllConsumersDropAllDynamicFields(t *testing.T) {
	dir := t.TempDir()
	m := map[string]interface{}{"identity": "哨兵", "lessons_core": "教训"}
	for _, f := range dynamicMemoryFields {
		m[f] = map[string]interface{}{"probe": "哨兵探针值"}
	}
	raw, _ := json.Marshal(m)

	// ① buildMemoryTailText (走文件)
	if err := os.WriteFile(filepath.Join(dir, "memory.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	tail := buildMemoryTailText(dir)
	if tail == "" {
		t.Fatal("buildMemoryTailText 返回空")
	}

	for _, f := range dynamicMemoryFields {
		if hasKey(tail, f) {
			t.Errorf("buildMemoryTailText 未剔除动态字段 %q", f)
		}
	}
	// 锚点字段必须保留 (剔除过度 = 记忆丢失)
	// 注: lessons 于 20261004 起归动态字段 (由 RecallMemory 召回), 常驻部分迁至 lessons_core。
	for _, k := range []string{"identity", "lessons_core"} {
		if !hasKey(tail, k) {
			t.Errorf("buildMemoryTailText 误删锚点字段 %q", k)
		}
	}
}

// 审计不再静默丢弃锚点改动: 改 lessons_core (常驻硬约束, 锚点字段) 必须留痕。
func TestAnchorSentinel_AuditKeepsPreviouslyMissedField(t *testing.T) {
	wd := t.TempDir()
	base := map[string]interface{}{
		"identity": "x", "role": "y", "language": "zh", "working_dir": "wd",
		"gates": "python", "axioms": "a", "self_governance": map[string]interface{}{},
		"lessons_core": "旧教训",
	}
	d0, _ := json.Marshal(base)
	if err := SaveMemory(wd, d0); err != nil {
		t.Fatalf("首次写入: %v", err)
	}
	base["lessons_core"] = "新教训"
	d1, _ := json.Marshal(base)
	if err := SaveMemory(wd, d1); err == nil {
		t.Fatal("同日第 2 次锚点写入应被护栏拦截 (lessons_core 属锚点字段)")
	}
	raw, err := os.ReadFile(anchorAuditPath(wd))
	if err != nil {
		t.Fatalf("审计文件缺失: %v", err)
	}
	if !containsT(string(raw), "lessons_core") {
		t.Fatalf("lessons_core 改动未入审计 (原实现静默丢弃): %s", string(raw))
	}
}

// pyMemoryWritePath 记忆写入出口工具 (Python 侧) —— 跨语言同源判据的锚点。
const pyMemoryWritePath = ".forge/forge-tools/memory_write.py"

// TestPythonDynamicFieldsMatchGo: 跨语言同源清单 —— memory_write.py 的 DYNAMIC_FIELDS
// 必须与 Go 侧 dynamicMemoryFields 逐元素相同。
// 两侧各自维护清单 = 必然漂移 (本文件开头那 6 个漏检字段就是同一病根):
// Go 侧加字段而 Python 侧没加 → 该字段改动被出口工具当成非锚点 → 审计条目与配额双双漏掉。
func TestPythonDynamicFieldsMatchGo(t *testing.T) {
	raw, err := os.ReadFile(pyMemoryWritePath)
	if err != nil {
		t.Skipf("出口工具不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	m := regexp.MustCompile(`(?s)DYNAMIC_FIELDS\s*=\s*\(([^)]*)\)`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("%s 未找到 DYNAMIC_FIELDS 清单 (判据锚点丢失)", pyMemoryWritePath)
	}
	var got []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(string(m[1]), -1) {
		got = append(got, q[1])
	}
	want := append([]string{}, dynamicMemoryFields...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Python 侧 DYNAMIC_FIELDS 与 Go 侧 dynamicMemoryFields 漂移:\n  py=%v\n  go=%v", got, want)
	}
}

// TestPythonGuardOffNotHardcoded: 回归 20261003 —— 出口工具曾把审计条目的 guard_off
// 硬编码 true, 后果: ①审计恒显 [护栏关闭] (名实不符) ②anchorAuditTodayCount 跳过该条
// → 走该工具的写入永不占当日配额 = 合规工具成了护栏后门。
// 钉住三件事: 不得硬编码 / 必须探测真值 / exempt 必须单列。
func TestPythonGuardOffNotHardcoded(t *testing.T) {
	raw, err := os.ReadFile(pyMemoryWritePath)
	if err != nil {
		t.Skipf("出口工具不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	src := string(raw)
	hard := `"guard_off": ` + "True"
	if strings.Contains(src, hard) {
		t.Fatalf("%s 仍硬编码 %s → 审计标签恒假且永不占配额", pyMemoryWritePath, hard)
	}
	// 形态级判据: guard_off 的每一次赋值都必须由守卫行 (含 off/guard) 引导。
	// 只钉字面量是不够的 —— 变异自检实测 `entry["guard_off"] = True` (下标赋值) 逃过了
	// 字面量判据; 行为级判据在 memory_write.py selftest (guard_off_real / quota_block 向量)。
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		if !strings.Contains(line, "guard_off") || !strings.Contains(line, "True") ||
			strings.Contains(line, "#") || strings.Contains(line, "==") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		guarded := false
		for j := i - 1; j >= 0; j-- {
			prev := lines[j]
			if strings.TrimSpace(prev) == "" {
				continue
			}
			if pi := len(prev) - len(strings.TrimLeft(prev, " ")); pi < indent {
				guarded = strings.Contains(prev, "off") || strings.Contains(prev, "guard")
				break
			}
		}
		if !guarded {
			t.Fatalf("%s:%d guard_off 赋值无守卫行 (名实不符/永不占配额): %s",
				pyMemoryWritePath, i+1, strings.TrimSpace(line))
		}
	}
	for _, need := range []string{"guard_enabled()", "SRC_EXEMPT", "quota_deny("} {
		if !strings.Contains(src, need) {
			t.Fatalf("%s 缺 %s (护栏真值探测 / exempt 单列 / 写入前配额拦截)", pyMemoryWritePath, need)
		}
	}
}

// TestPythonMemoryWriteSelftest: 出口工具的行为级自检必须真跑 (接线才算做完)。
// 上面两条哨兵只看源码文本; 真正证明「guard_off 取真值 / 配额拦截生效 / exempt 不占配额」
// 的是 memory_write.py selftest 的 10 条向量 —— 没有任何调度跑到它 = 判据不存在。
// 工具不在位(开源仓库)或无 python → skip, 不制造假红。
func TestPythonMemoryWriteSelftest(t *testing.T) {
	if _, err := os.Stat(pyMemoryWritePath); err != nil {
		t.Skipf("出口工具不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	cmd := exec.Command(py, "-u", pyMemoryWritePath, "selftest")
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("出口工具 selftest 未通过 (rc=%v): %s", err, string(out))
	}
	if !strings.Contains(string(out), `"ok": true`) {
		t.Fatalf("selftest 未报 ok: %s", string(out))
	}
}
