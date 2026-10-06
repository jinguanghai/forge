package main

// side_effect_guard_test.go — 佐制方(害佐模式)的判据 (20261005)
//
// 动机(主人定调): "发现问题就放入代办, 但不要解决问题后又产生新的副作用 —— 用害佐模式预防掉。
// 中医治未病思路"。佐制方 = 君药(修复动作)旁边那味制约偏性的佐药:
// defense_system/side_effect_guard.py 按「改动面 -> 已知副作用 -> 靶向判据」查表并执行。
//
// 为什么本文件必须存在(公理三: 架构即测试):
//   规则表是【数据】, 数据自己不会说"我引用的判据不存在"。实测同型事故:
//   hygiene_owner_refs_test.go 出现前, manifest 的 owner 指向已消失的 self_gate.go
//   而哨兵常年全绿 —— 「有值」!=「值有效」。佐制清单若引用了不存在的判据,
//   看着齐全而实际跑不动, 副作用照样漏。
//
// 判据三层:
//   1. 判据真实性  --verify: 规则引用的每个 Test*/脚本必须真实存在
//   2. 鉴别力      --selftest: 注入假改动面/假规则, 必须命中预期、不误报、抓出画出来的药
//   3. 防缩水      covers: 已知副作用类型必须全部在表内(硬编码清单, 删规则即报红)
//
// 工具不在位(开源仓库无 defense_system)或无 python -> skip, 不制造假红。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const seGuardScript = "side_effect_guard.py"
const seRulesFile = "side_effect_rules.json"

// seKnownSideEffects 是【已实测过的副作用类型】硬编码清单 —— 规则表必须是它的超集。
// 删规则 = 删掉一条已知副作用的防线, 本清单不改就该报红。
var seKnownSideEffects = []string{
	"env_credential",   // .env 加凭据 -> 历史副本报红 (踩坑34)
	"root_new_file",    // 根目录新增文件 -> hygiene 白名单 (踩坑35)
	"py_change",        // 改 .py -> __pycache__ 落盘 (踩坑35)
	"ds_script_add_rm", // defense_system 脚本增删 -> owner 引用有效性
	"new_gotest",       // 新哨兵 -> 变异清单水位
	"memory_json",      // 改记忆 -> 写入留痕护栏 (踩坑32)
	"judge_json",       // 改判据 json -> 判定行为静默改变
	"go_prod",          // 改生产 .go -> exe 就位一致性
	"hourly_wiring",    // 改调度/测试通道 -> 接线判据表
	"go_format_scope",  // 批量 gofmt -w -> 波及 _archive 历史快照(无 git 可回滚, 20261005 实测事故)
}

type seAction struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Text string `json:"text"`
	Path string `json:"path"`
}

type seRule struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Risk    string     `json:"risk"`
	Source  string     `json:"source"`
	Match   []string   `json:"match"`
	Actions []seAction `json:"actions"`
}

type seRulesDoc struct {
	Version int        `json:"version"`
	Rules   []seRule   `json:"rules"`
	Always  []seAction `json:"always"`
}

// seGuardDir 返回佐制方的所在目录; 不在位时返回空串(开源仓库无 defense_system)。
func seGuardDir() string {
	if _, err := os.Stat(filepath.Join("defense_system", seGuardScript)); err != nil {
		return ""
	}
	return "defense_system"
}

func seRunGuard(t *testing.T, args ...string) (int, string) {
	t.Helper()
	dir := seGuardDir()
	if dir == "" {
		t.Skip("佐制方不在位 (开源仓库无 defense_system): skip")
	}
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	cmd := exec.Command(py, append([]string{filepath.Join(dir, seGuardScript)}, args...)...)
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("佐制方无法启动: %v\n%s", err, out)
		}
	}
	return code, string(out)
}

// TestSideEffectGuardVerify 判据真实性: 规则表引用的判据/脚本必须真实存在。
// 这是防「画出来的药」的那一条 —— 佐制清单名字对不上就等于没有佐制。
func TestSideEffectGuardVerify(t *testing.T) {
	code, out := seRunGuard(t, "--verify")
	if code != 0 {
		t.Fatalf("规则表校验未通过 rc=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "全部有效") {
		t.Fatalf("--verify 未给出有效结论:\n%s", out)
	}
}

// TestSideEffectGuardSelftest 鉴别力: 注入假改动面/假规则, 必须命中预期且抓出伪造。
// 全绿而不会报红的佐制清单是装饰品。
func TestSideEffectGuardSelftest(t *testing.T) {
	code, out := seRunGuard(t, "--selftest")
	if code != 0 {
		t.Fatalf("佐制方自检失败 rc=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "全部通过") {
		t.Fatalf("--selftest 未给出通过结论:\n%s", out)
	}
}

// TestSideEffectGuardScanRuns 扫描臂可用: 任意工作区状态下 rc=0 (扫描是只读, 不该失败)。
func TestSideEffectGuardScanRuns(t *testing.T) {
	code, out := seRunGuard(t)
	if code != 0 {
		t.Fatalf("扫描失败 rc=%d:\n%s", code, out)
	}
	if !strings.Contains(out, "佐制方") {
		t.Fatalf("扫描未产出佐制报告:\n%s", out)
	}
}

// seReadRules 读规则表(缺失即 Fatal, 因为佐制方本身要求它在位)。
func seReadRules(t *testing.T) seRulesDoc {
	t.Helper()
	dir := seGuardDir()
	if dir == "" {
		t.Skip("佐制方不在位 (开源仓库无 defense_system): skip")
	}
	raw, err := os.ReadFile(filepath.Join(dir, seRulesFile))
	if err != nil {
		t.Fatalf("规则表不可读: %v", err)
	}
	var d seRulesDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("规则表不可解析: %v", err)
	}
	return d
}

// TestSideEffectGuardCoversKnownSideEffects 防缩水: 已实测副作用类型必须全在表内。
// 只增不减的清单会腐化, 只减不增的清单同样会 —— 删规则必须连本清单一起改, 改动可见。
func TestSideEffectGuardCoversKnownSideEffects(t *testing.T) {
	d := seReadRules(t)
	have := map[string]bool{}
	for _, r := range d.Rules {
		have[r.ID] = true
	}
	for _, want := range seKnownSideEffects {
		if !have[want] {
			t.Errorf("已知副作用类型 %q 不在规则表内 -> 该防线被删除或改名(防缩水判据)", want)
		}
	}
	if len(d.Rules) < len(seKnownSideEffects) {
		t.Errorf("规则数 %d 少于已知副作用类型数 %d", len(d.Rules), len(seKnownSideEffects))
	}
}

// seRuleProblems 校验单条规则的完整性与留证 (纯函数, 便于注入变异自检)。
func seRuleProblems(rules []seRule) []string {
	var bad []string
	for _, r := range rules {
		if strings.TrimSpace(r.ID) == "" {
			bad = append(bad, "存在无 id 的规则")
			continue
		}
		for _, f := range []struct{ name, val string }{
			{"title", r.Title}, {"risk", r.Risk}, {"source", r.Source},
		} {
			if strings.TrimSpace(f.val) == "" {
				bad = append(bad, r.ID+": 缺 "+f.name+" (无实测来源的规则 = 画出来的药)")
			}
		}
		if len(r.Match) == 0 {
			bad = append(bad, r.ID+": match 为空 (永不命中的规则是装饰品)")
		}
		if len(r.Actions) == 0 {
			bad = append(bad, r.ID+": actions 为空 (只有风险没有佐制动作 = 只诊断不开方)")
		}
	}
	return bad
}

// TestSideEffectGuardRulesHaveEvidence 每条规则必须有实测来源 + 至少一个佐制动作。
func TestSideEffectGuardRulesHaveEvidence(t *testing.T) {
	d := seReadRules(t)
	if probs := seRuleProblems(d.Rules); len(probs) > 0 {
		t.Fatalf("规则表留证不足 %d 处:\n%s", len(probs), strings.Join(probs, "\n"))
	}
	if len(d.Always) == 0 {
		t.Error("always 段为空 -> 收口动作(验收单/全量测试)丢失")
	}
}

// TestSideEffectGuardRuleProblemsSelfCheck 判据自身的判据: 注入残缺规则, 校验必须报出。
func TestSideEffectGuardRuleProblemsSelfCheck(t *testing.T) {
	bad := []seRule{
		{ID: "no_source", Title: "t", Risk: "r", Match: []string{".*"},
			Actions: []seAction{{Kind: "note", Text: "x"}}},
		{ID: "no_match", Title: "t", Risk: "r", Source: "s",
			Actions: []seAction{{Kind: "note", Text: "x"}}},
		{ID: "no_action", Title: "t", Risk: "r", Source: "s", Match: []string{".*"}},
		{ID: "", Title: "t", Risk: "r", Source: "s", Match: []string{".*"}},
	}
	probs := seRuleProblems(bad)
	if len(probs) < 4 {
		t.Fatalf("残缺规则注入未被全部抓出 (得到 %d 条): %v", len(probs), probs)
	}
	joined := strings.Join(probs, "\n")
	for _, want := range []string{"no_source", "no_match", "no_action", "无 id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("校验漏掉 %q:\n%s", want, joined)
		}
	}
	// 反向: 健康输入不得误报
	good := []seRule{{ID: "ok", Title: "t", Risk: "r", Source: "s",
		Match: []string{".*"}, Actions: []seAction{{Kind: "note", Text: "x"}}}}
	if probs := seRuleProblems(good); len(probs) != 0 {
		t.Fatalf("健康规则被误报: %v", probs)
	}
}
