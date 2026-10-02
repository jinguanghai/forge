// gatesync_test.go: gate 五处同步检查(/gatesync)的行为契约。
//
// 关键防的是"短名假命中": 裸子串判定会让 sh 被 shell/finish/push 假命中,
// 该面等于永远不报警。hasGateToken 必须做词边界判定。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatesync_HasGateToken(t *testing.T) {
	cases := []struct {
		data, name string
		want       bool
	}{
		{"gate: 'python',", "python", true},
		{"[sh, python]", "sh", true},
		{"sh", "sh", true},
		{"python/go", "go", true},
		{"'go'", "go", true},
		// 词边界: 短名不得被无关词假命中
		{"shell script", "sh", false},
		{"finish", "sh", false},
		{"push", "sh", false},
		{"shx", "sh", false},
		{"xsh", "sh", false},
		{"a_sh_b", "sh", false},
		{"logo", "go", false},
		{"gate_go_x", "go", false},
		{"python3", "python", false},
		{"", "sh", false},
		{"nothing here", "tcm", false},
	}
	for _, c := range cases {
		if got := hasGateToken(c.data, c.name); got != c.want {
			t.Errorf("hasGateToken(%q, %q) = %v 期望 %v", c.data, c.name, got, c.want)
		}
	}
}

func TestGatesync_IsWordByte(t *testing.T) {
	word := []byte{'_', '0', '9', 'a', 'z', 'A', 'Z'}
	for _, b := range word {
		if !isWordByte(b) {
			t.Errorf("isWordByte(%q) 应为 true", b)
		}
	}
	nonWord := []byte{'-', ' ', ':', ',', '\n', '\'', '"', '/', '.', 0x80}
	for _, b := range nonWord {
		if isWordByte(b) {
			t.Errorf("isWordByte(%q) 应为 false", b)
		}
	}
}

func TestGatesync_IsPluginUnpublished(t *testing.T) {
	for _, g := range pluginUnpublishedGates {
		if !isPluginUnpublished(g) {
			t.Errorf("%s 应在有意不发布清单中", g)
		}
	}
	for _, g := range []string{"python", "go", "node", "math", "logic", "regex", "knowledge", "chain"} {
		if isPluginUnpublished(g) {
			t.Errorf("%s 应发布, 不得列入不发布清单", g)
		}
	}
}

// gateSyncFixture 构造五处同步检查所需的目录结构。
type gateSyncFixture struct {
	workDir   string
	pluginDir string
}

func newGateSyncFixture(t *testing.T, memGates string, withMemFile bool,
	readmeGates []string, withReadme bool, pluginGates []string, withPlugin bool) gateSyncFixture {
	t.Helper()
	work := t.TempDir()
	plug := t.TempDir()

	if withMemFile {
		raw, err := json.Marshal(map[string]string{"gates": memGates})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, "memory.json"), raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if withReadme {
		if err := os.WriteFile(filepath.Join(work, "README.md"),
			[]byte("gates: "+strings.Join(readmeGates, " ")), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if withPlugin {
		dir := filepath.Join(plug, "dsh-forge-plugins", "plugins", "forge-gates")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		for _, g := range pluginGates {
			sb.WriteString("  gate: '" + g + "',\n")
		}
		if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(sb.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return gateSyncFixture{workDir: work, pluginDir: plug}
}

func allGates() []string { return append([]string(nil), currentGates...) }
func publishableGates() []string {
	var out []string
	for _, g := range currentGates {
		if !isPluginUnpublished(g) {
			out = append(out, g)
		}
	}
	return out
}

func TestGatesync_CheckAllConsistent(t *testing.T) {
	f := newGateSyncFixture(t, strings.Join(allGates(), "/"), true,
		allGates(), true, publishableGates(), true)
	out := gateSyncCheck(f.workDir, f.pluginDir)
	if !strings.Contains(out, "🎉 五处全部一致") {
		t.Errorf("齐全时应报五处一致, 实际输出:\n%s", out)
	}
	if strings.Contains(out, "⚠️") {
		t.Errorf("不应有差异项:\n%s", out)
	}
}

func TestGatesync_CheckMissingInMemory(t *testing.T) {
	gates := allGates()
	var without []string
	for _, g := range gates {
		if g != "tcm" {
			without = append(without, g)
		}
	}
	f := newGateSyncFixture(t, strings.Join(without, "/"), true,
		allGates(), true, publishableGates(), true)
	out := gateSyncCheck(f.workDir, f.pluginDir)
	if !strings.Contains(out, "③记忆 memory.json.gates 缺: tcm") {
		t.Errorf("应报记忆面缺 tcm:\n%s", out)
	}
}

func TestGatesync_CheckMissingInReadme(t *testing.T) {
	var without []string
	for _, g := range allGates() {
		if g != "knowledge" {
			without = append(without, g)
		}
	}
	f := newGateSyncFixture(t, strings.Join(allGates(), "/"), true,
		without, true, publishableGates(), true)
	out := gateSyncCheck(f.workDir, f.pluginDir)
	if !strings.Contains(out, "④ README 缺 gate 名: knowledge") {
		t.Errorf("应报 README 缺 knowledge:\n%s", out)
	}
}

func TestGatesync_CheckMissingInPlugin(t *testing.T) {
	var without []string
	for _, g := range publishableGates() {
		if g != "math" {
			without = append(without, g)
		}
	}
	f := newGateSyncFixture(t, strings.Join(allGates(), "/"), true,
		allGates(), true, without, true)
	out := gateSyncCheck(f.workDir, f.pluginDir)
	if !strings.Contains(out, "⑤ 发布包缺 gate 名: math") {
		t.Errorf("应报发布包缺 math:\n%s", out)
	}
}

func TestGatesync_UnpublishedGateNotRequiredInPlugin(t *testing.T) {
	// 有意不发布的 gate 缺席时不得报警 —— 否则该面永远报警, 等于失效。
	f := newGateSyncFixture(t, strings.Join(allGates(), "/"), true,
		allGates(), true, publishableGates(), true)
	out := gateSyncCheck(f.workDir, f.pluginDir)
	for _, g := range pluginUnpublishedGates {
		if strings.Contains(out, "⑤ 发布包缺 gate 名: "+g) {
			t.Errorf("有意不发布的 %s 不应报缺失:\n%s", g, out)
		}
	}
}

func TestGatesync_CheckMissingFiles(t *testing.T) {
	f := newGateSyncFixture(t, "", false, nil, false, nil, false)
	out := gateSyncCheck(f.workDir, f.pluginDir)
	for _, want := range []string{
		"③记忆 memory.json 无 gates 字段",
		"④ README.md 不可读",
		"⑤ 发布包 forge-gates/index.js 不可读",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "🎉") {
		t.Error("有差异时不得报全部一致")
	}
}

func TestGatesync_ContainsIssue(t *testing.T) {
	issues := []string{"③记忆 memory.json.gates 缺: tcm", "④ README 缺 gate 名: math"}
	if !containsIssue(issues, "memory.json.gates") {
		t.Error("应命中子串")
	}
	if containsIssue(issues, "⑤") {
		t.Error("不应命中不存在的子串")
	}
	if containsIssue(nil, "x") {
		t.Error("空切片应为 false")
	}
}

func TestGatesync_CurrentGatesHas13Unique(t *testing.T) {
	got := currentGates
	if len(got) != 13 {
		t.Fatalf("gate 面数 = %d 期望 13 (sh 已于 20261001 退役)", len(got))
	}
	seen := map[string]bool{}
	for _, g := range got {
		if g == "" {
			t.Error("gate 名不得为空")
		}
		if seen[g] {
			t.Errorf("gate 名重复: %s", g)
		}
		seen[g] = true
	}
	// 单一源: currentGates 必须就是 铸剑炉_GATES(不得留副本)
	if len(铸剑炉_GATES) != len(got) {
		t.Errorf("currentGates(%d) 与 铸剑炉_GATES(%d) 不一致 —— 单一源被破坏",
			len(got), len(铸剑炉_GATES))
	}
	for i := range got {
		if got[i] != 铸剑炉_GATES[i] {
			t.Errorf("第 %d 面不一致: %s vs %s", i, got[i], 铸剑炉_GATES[i])
		}
	}
}

func TestGatesync_RealRepoThirdFace(t *testing.T) {
	// 真实仓库第③处(记忆 gates 字段)必须含全部 gate 名 —— 这是可判定的同步点。
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(memoryFilePath(wd))
	if err != nil {
		t.Skipf("无 memory.json, 跳过真实仓库检查: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("memory.json 不是合法 JSON: %v", err)
	}
	memGates, _ := m["gates"].(string)
	if memGates == "" {
		t.Fatal("memory.json 缺 gates 字段 —— 五处同步第③处失效")
	}
	for _, g := range currentGates {
		if !strings.Contains(memGates, g) {
			t.Errorf("memory.json.gates 缺 %s (记忆面未同步)", g)
		}
	}
}
