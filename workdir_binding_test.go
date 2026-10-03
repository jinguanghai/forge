package main

// workdir_binding_test.go — 子进程工作目录绑定哨兵 (20260925)
//
// 由来: 从 C:\\Users\\<user> 启动 forge.exe 时, 自托管 gate (relation/tcm) 继承"启动目录",
// 于是 relation_gate.py 扫不到任何 .go (报"生产符号 0"), tcm 找不到 formula_db.json
// (报"未找到") —— 而全量 657 用例全绿。根因: 既有测试里 WorkDir = os.Getwd(),
// "工作目录"与"进程当前目录"恒等, 错位场景在结构上无法被构造。
// 判据(普适): 凡依赖进程 cwd 的组件, 测试必须补 cwd != workDir 的反例, 否则测试恒真。

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// chdirTemp: 进程级切目录并在测试结束时还原 (t.Chdir 需 go1.24, 本模块声明 go1.22)。
func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func testCfg() *Config {
	return &Config{MaxConcurrent: 1, CacheMaxSize: 4, RetryMax: 0}
}

// TestNewCmdBindsWorkDir: newCmd 是全部 gate 子进程的唯一入口, 必须在此绑定工作目录。
// 修复前: Dir == "" (继承启动目录) -> 失败。
func TestNewCmdBindsWorkDir(t *testing.T) {
	f := NewForge(`D:\forge`, testCfg())
	defer f.Shutdown()
	cmd := f.newCmd(context.Background(), "go", "version")
	if cmd.Dir != f.workDir {
		t.Fatalf("newCmd 未绑定工作目录: Dir=%q, want %q —— 子进程会继承启动目录", cmd.Dir, f.workDir)
	}
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "FORGE_WORKDIR="+f.workDir) {
		t.Errorf("newCmd 未注入 FORGE_WORKDIR=%s (relation 等脚本的兜底根目录)", f.workDir)
	}
	if !strings.Contains(env, "FORGE_DATA="+f.workDir) {
		t.Errorf("newCmd 未注入 FORGE_DATA=%s (tcm 方剂库目录)", f.workDir)
	}
}

// TestNewCmdEnvNoOverride: 用户显式设置的 FORGE_WORKDIR/FORGE_DATA 不得被覆盖。
func TestNewCmdEnvNoOverride(t *testing.T) {
	t.Setenv("FORGE_DATA", `X:\custom_data`)
	t.Setenv("FORGE_WORKDIR", `X:\custom_root`)
	f := NewForge(`D:\forge`, testCfg())
	defer f.Shutdown()
	cmd := f.newCmd(context.Background(), "go", "version")
	nData, nRoot := 0, 0
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, "FORGE_DATA=") {
			nData++
			if e != `FORGE_DATA=X:\custom_data` {
				t.Errorf("FORGE_DATA 被覆盖: %s", e)
			}
		}
		if strings.HasPrefix(e, "FORGE_WORKDIR=") {
			nRoot++
			if e != `FORGE_WORKDIR=X:\custom_root` {
				t.Errorf("FORGE_WORKDIR 被覆盖: %s", e)
			}
		}
	}
	if nData != 1 || nRoot != 1 {
		t.Errorf("环境变量重复注入: FORGE_DATA x%d, FORGE_WORKDIR x%d", nData, nRoot)
	}
}

// TestRelationGateProductionPathFromForeignCwd: 真·错位复现。
// 进程 cwd 换成空目录, WorkDir 仍指 D:\forge -> 骨架必须扫到源码。
func TestRelationGateProductionPathFromForeignCwd(t *testing.T) {
	foreign := t.TempDir()
	chdirTemp(t, foreign)
	f := NewForge(`D:\forge`, testCfg())
	defer f.Shutdown()
	r := f.forgeGateSkipCache(`{"type":"scan"}`, "relation", "", true)
	if !r.OK {
		t.Fatalf("relation gate 生产路径失败: %s", r.Error)
	}
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		t.Fatalf("判定输出非法 JSON: %v / %s", err, r.Stdout)
	}
	total := parseSymbolTotal(t, d)
	if total == 0 {
		t.Fatalf("错位 cwd 下骨架为空(生产符号 0) —— 扫描根落到了启动目录: %s", r.Stdout)
	}
	// 断言路径也必须可用(此前报"主语 agent.go 在骨架中不存在")
	r2 := f.forgeGateSkipCache(`{"type":"assert","claim":"agent.go 调用了 describeGates"}`, "relation", "", true)
	if !r2.OK {
		t.Fatalf("assert 执行失败: %s", r2.Error)
	}
	var d2 map[string]interface{}
	if err := json.Unmarshal([]byte(r2.Stdout), &d2); err != nil {
		t.Fatalf("assert 输出非法 JSON: %v / %s", err, r2.Stdout)
	}
	if v, _ := d2["verdict"].(string); strings.Contains(v, "在骨架中不存在") {
		t.Fatalf("错位 cwd 下主语识别失败: %s", r2.Stdout)
	}
}

func parseSymbolTotal(t *testing.T, d map[string]interface{}) int {
	t.Helper()
	ev, _ := d["evidence"].([]interface{})
	for _, e := range ev {
		s, _ := e.(string)
		if m := regexp.MustCompile(`符号总数\(生产定义\):\s*(\d+)`).FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	t.Fatalf("evidence 里找不到符号总数: %v", d["evidence"])
	return -1
}

// TestTCMGateHerbPairFromForeignCwd: 主人的核心功能 —— 药对查询在错位 cwd 下必须可用。
func TestTCMGateHerbPairFromForeignCwd(t *testing.T) {
	if _, err := os.Stat(filepath.Join(ForgeToolsDir, "tcm_gate.exe")); err != nil {
		t.Skip("tcm_gate.exe 不存在")
	}
	foreign := t.TempDir()
	chdirTemp(t, foreign)
	f := NewForge(`D:\forge`, testCfg())
	defer f.Shutdown()
	r := f.forgeGateSkipCache(`{"type":"herb_pair","herb1":"黄芪","herb2":"当归"}`, "tcm", "", true)
	if !r.OK {
		t.Fatalf("tcm gate 执行失败: %s", r.Error)
	}
	var d map[string]interface{}
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		t.Fatalf("输出非法 JSON: %v / %s", err, r.Stdout)
	}
	if msg, _ := d["error"].(string); msg != "" {
		t.Fatalf("错位 cwd 下药对查询报错: %s", msg)
	}
	cnt, _ := d["count"].(float64)
	if cnt < 1 {
		t.Fatalf("错位 cwd 下药对查询命中 0 首方剂: %s", r.Stdout)
	}
}

// TestRelationGateFailsClosedOnEmptyRoot: 空骨架必须 fail-closed, 不得静默报"缺口 0"。
func TestRelationGateFailsClosedOnEmptyRoot(t *testing.T) {
	scriptAbs, err := filepath.Abs(filepath.Join(ForgeToolsDir, "relation_gate.py"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(scriptAbs); err != nil {
		t.Skip("relation_gate.py 不存在")
	}
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python 不在 PATH")
	}
	empty := t.TempDir()
	payload, _ := json.Marshal(map[string]string{"type": "scan", "root": empty})
	cmd := exec.Command("python", scriptAbs, string(payload))
	cmd.Env = pythonUTF8Env() // 中文 verdict 必须按 UTF-8 读, 否则判据恒假(见 pythonUTF8Env 注释)
	out, _ := cmd.CombinedOutput()
	if !json.Valid(out) {
		t.Fatalf("输出非合法 JSON: %s", string(out))
	}
	var d map[string]interface{}
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatal(err)
	}
	if ok, _ := d["ok"].(bool); ok {
		t.Fatalf("空扫描根被当成成功(失效时给绿灯): %s", string(out))
	}
	if v, _ := d["verdict"].(string); !strings.Contains(v, "扫描根") && !strings.Contains(v, "空") {
		t.Errorf("verdict 应说明扫描根为空: %q", v)
	}
}

// extractFuncBody 取 sig 起始函数的 { ... } 区间 (括号计数; 仅用于本文件的不变式断言)。
func extractFuncBody(src, sig string) string {
	i := strings.Index(src, sig)
	if i < 0 {
		return ""
	}
	j := strings.Index(src[i:], "{")
	if j < 0 {
		return ""
	}
	start := i + j
	depth := 0
	for k := start; k < len(src); k++ {
		switch src[k] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : k+1]
			}
		}
	}
	return ""
}

// TestNewCmdBindsWorkDirInSource 源码级不变式: 唯一入口 newCmd 必须自己绑定工作目录与环境。
// 运行时断言可能被"某个调用点自行设 Dir"绕过; 源码级断言把职责钉在唯一入口上 ——
// 这是防复发的关键: 今后新增任何 gate 子进程都自动继承绑定, 无需逐处记得设置。
func TestNewCmdBindsWorkDirInSource(t *testing.T) {
	body := extractFuncBody(prodGoSources(t), "func (f *Forge) newCmd(")
	if body == "" {
		t.Fatal("全包中找不到 newCmd 函数体 (函数被改名/删除? 唯一入口的绑定职责会随之丢失)")
	}
	for _, want := range []string{"cmd.Dir = f.workDir", "FORGE_WORKDIR=", "FORGE_DATA="} {
		if !strings.Contains(body, want) {
			t.Errorf("newCmd 函数体缺 %q —— 子进程将继承启动目录/拿不到兜底根目录", want)
		}
	}
}
