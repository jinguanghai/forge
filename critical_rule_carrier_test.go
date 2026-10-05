package main

// critical_rule_carrier_test.go — critical_rules → 载体映射哨兵 (20261004)。
//
// 根因 (TODO 主线第 5 条): 公理有映射表(axiom_carriers.json)而**规则没有** ——
// 「12 条规则里哪些已下沉、哪些还是纯 prompt」根本无法回答, 于是漏项被当成没有
// (TODO 一度写着"当前无剩余待办")。没有清单就没有待办。
//
// 判据:
//  ① 清单可解析, 且 prompt 的 <critical_rules> 段能解析出规则号 (fail-closed)
//  ② 规则号双向一致: prompt 里有的清单必须有, 清单有的 prompt 里必须有 ——
//     prompt 加了规则而清单没跟上(或反之)必须报红, 这正是本哨兵存在的理由
//  ③ 每条规则至少 1 个载体, role 非空
//  ④ 载体文件必须存在; anchor 非空时必须在该文件内命中
//  ⑤ 已下沉规则数(至少 1 个 anchor 命中的 judge 载体) >= 水位, 只升不降
//  ⑥ 打印"仍未下沉"规则清单 —— 让待办随时可查, 不靠人记
//
// 下沉状态不单列字段而由载体推导: 状态字段会与代码脱节, 载体 anchor 不会
// (与 axiom_carriers.json 的 ⑧ 描述真实性判据同源)。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const criticalRulesManifestPath = "defense_system/critical_rules_carriers.json"

// criticalRuleSinkFloor 已下沉规则数水位 (只升不降)。
//
// 20261004 建档实测: 12 条规则中 10 条已有 anchor 命中的死程序判据;
// 规则 2(中文输出) 与规则 9(进度管理格式) 仍是纯 prompt。
// 每把一条规则真正下沉(代码强制 + 判据), 就把这里 +1 —— 水位是"下沉进度"的硬刻度。
const criticalRuleSinkFloor = 10

type criticalRuleCarrier struct {
	File   string `json:"file"`
	Kind   string `json:"kind"`
	Anchor string `json:"anchor"`
	Role   string `json:"role"`
}

type criticalRuleItem struct {
	Num      int                   `json:"num"`
	Theme    string                `json:"theme"`
	Carriers []criticalRuleCarrier `json:"carriers"`
}

type criticalRulesManifest struct {
	Version    int                `json:"version"`
	PromptFile string             `json:"promptFile"`
	Rules      []criticalRuleItem `json:"rules"`
}

// criticalRuleLineRe prompt 里规则定义行的形态 ("1. xxx")。
var criticalRuleLineRe = regexp.MustCompile(`(?m)^(\d+)\. `)

// criticalRuleNumsFromPrompt 从 system prompt 的 <critical_rules> 段解析规则号。
// 只认该段内的定义行 —— 段外正文里的 "1. " 不算规则。
func criticalRuleNumsFromPrompt(sp string) []int {
	i := strings.Index(sp, "<critical_rules>")
	j := strings.Index(sp, "</critical_rules>")
	if i < 0 || j <= i {
		return nil
	}
	var out []int
	for _, mm := range criticalRuleLineRe.FindAllStringSubmatch(sp[i:j], -1) {
		if n, err := strconv.Atoi(mm[1]); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// criticalRuleProblems 返回清单的全部问题; 空切片 = 健康。
// 纯函数(输入=字节/文本/读取器, 无全局状态) —— 变异自检得以喂伪造输入。
func criticalRuleProblems(manifestJSON []byte, sp string, readFile func(string) (string, bool)) []string {
	var probs []string
	var mf criticalRulesManifest
	if err := json.Unmarshal(manifestJSON, &mf); err != nil {
		return []string{"清单不可解析: " + err.Error()}
	}
	if len(mf.Rules) == 0 {
		return []string{"清单 rules 为空 (fail-closed)"}
	}
	fromPrompt := criticalRuleNumsFromPrompt(sp)
	if len(fromPrompt) == 0 {
		return []string{"prompt 未解析出任何规则定义 (格式变化? fail-closed 中止)"}
	}
	promptSet := map[int]bool{}
	for _, n := range fromPrompt {
		promptSet[n] = true
	}
	seen := map[int]bool{}
	sunk := 0
	for _, r := range mf.Rules {
		tag := fmt.Sprintf("规则%d", r.Num)
		if r.Num <= 0 {
			probs = append(probs, "清单条目 num 非法: "+strconv.Itoa(r.Num))
			continue
		}
		if seen[r.Num] {
			probs = append(probs, "重复登记: "+tag)
		}
		seen[r.Num] = true
		if !promptSet[r.Num] {
			probs = append(probs, "幽灵规则: "+tag+" 不在 prompt 的 <critical_rules> 段内")
		}
		if strings.TrimSpace(r.Theme) == "" {
			probs = append(probs, tag+" theme 为空")
		}
		if len(r.Carriers) == 0 {
			probs = append(probs, "无载体: "+tag+" 载体清单为空")
			continue
		}
		anchoredJudge := 0
		for i, c := range r.Carriers {
			ctag := fmt.Sprintf("%s 载体#%d[%s]", tag, i+1, c.File)
			if strings.TrimSpace(c.File) == "" {
				probs = append(probs, ctag+" file 为空")
				continue
			}
			if strings.TrimSpace(c.Role) == "" {
				probs = append(probs, ctag+" role 为空 (占位载体)")
			}
			if c.Kind != "judge" && c.Kind != "process" {
				probs = append(probs, ctag+" kind 非法 (须 judge|process): "+c.Kind)
			}
			body, ok := readFile(c.File)
			if !ok {
				probs = append(probs, ctag+" 载体文件不存在 (改名/删除腐化)")
				continue
			}
			if c.Anchor == "" {
				continue // 显式承认的隐式载体: 只校验存在性
			}
			if !strings.Contains(body, c.Anchor) {
				probs = append(probs, ctag+" 文件内找不到锚 "+c.Anchor)
				continue
			}
			if c.Kind == "judge" {
				anchoredJudge++
			}
		}
		if anchoredJudge > 0 {
			sunk++
		}
	}
	var missing []string
	for n := range promptSet {
		if !seen[n] {
			missing = append(missing, strconv.Itoa(n))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		probs = append(probs, "未登记规则: "+strings.Join(missing, "/")+
			" 在 prompt 里存在而清单没有 (清单腐烂 = 漏项被当成没有)")
	}
	if sunk < criticalRuleSinkFloor {
		probs = append(probs, fmt.Sprintf("已下沉规则数 %d 低于水位 %d (水位只升不降)",
			sunk, criticalRuleSinkFloor))
	}
	sort.Strings(probs)
	return probs
}

func repoCriticalRuleReader(wd string) func(string) (string, bool) {
	return func(rel string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(wd, filepath.FromSlash(rel)))
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

// TestCriticalRuleCarriers_Healthy 真清单必须零问题, 并打印双向映射 + 未下沉清单。
// 未下沉清单是人工独立验收的抓手: 它把"还有哪些规则只是文字建议"变成可读的待办。
func TestCriticalRuleCarriers_Healthy(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, criticalRulesManifestPath))
	if err != nil {
		t.Skipf("清单不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	read := repoCriticalRuleReader(wd)
	if probs := criticalRuleProblems(raw, systemPrompt, read); len(probs) > 0 {
		t.Fatalf("规则↔载体映射存在问题 (共 %d 条):\n  %s", len(probs), strings.Join(probs, "\n  "))
	}
	var mf criticalRulesManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	var sunk, pending []string
	for _, r := range mf.Rules {
		var names []string
		judged := 0
		for _, c := range r.Carriers {
			names = append(names, c.File)
			if c.Kind == "judge" && c.Anchor != "" {
				judged++
			}
		}
		mark := "已下沉"
		if judged == 0 {
			mark = "纯 prompt"
			pending = append(pending, fmt.Sprintf("%d(%s)", r.Num, r.Theme))
		} else {
			sunk = append(sunk, strconv.Itoa(r.Num))
		}
		t.Logf("  规则%-2d [%s] %-8s judge锚 %d: %s", r.Num, r.Theme, mark, judged, strings.Join(names, ", "))
	}
	t.Logf("已下沉 %d 条 (规则 %s) / 水位 %d", len(sunk), strings.Join(sunk, ","), criticalRuleSinkFloor)
	if len(pending) > 0 {
		t.Logf("仍未下沉 (纯 prompt, 待办): %s", strings.Join(pending, " | "))
	} else {
		t.Log("全部规则均已下沉")
	}
}

// TestCriticalRuleCarriers_MutationSelfCheck 判据的判据: 五类注入必须各自报红。
func TestCriticalRuleCarriers_MutationSelfCheck(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, criticalRulesManifestPath))
	if err != nil {
		t.Skipf("清单不在位 (%v)", err)
	}
	read := repoCriticalRuleReader(wd)

	if probs := criticalRuleProblems(raw, systemPrompt, read); len(probs) != 0 {
		t.Fatalf("健康输入被判有问题 (基线不绿, 其余变异无意义): %v", probs)
	}

	mutate := func(f func(*criticalRulesManifest)) []byte {
		var mf criticalRulesManifest
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
	// ① 删掉一条规则 → 未登记规则
	m1 := mutate(func(m *criticalRulesManifest) { m.Rules = m.Rules[1:] })
	if probs := criticalRuleProblems(m1, systemPrompt, read); len(probs) == 0 {
		t.Error("① 删规则条目未被捕获")
	} else {
		t.Logf("① 删规则条目 → 报红 %d 条: %s", len(probs), probs[0])
	}
	// ② prompt 里新增规则而清单没跟上 → 未登记规则 (本哨兵存在的理由)
	sp2 := strings.Replace(systemPrompt, "</critical_rules>", "13. 新增但未登记\n</critical_rules>", 1)
	if probs := criticalRuleProblems(raw, sp2, read); len(probs) == 0 {
		t.Error("② prompt 加规则未被捕获 —— 清单腐烂正是要防的病")
	} else {
		t.Logf("② prompt 加规则 → 报红 %d 条: %s", len(probs), probs[0])
	}
	// ③ 载体文件改名 → 载体文件不存在
	m3 := mutate(func(m *criticalRulesManifest) { m.Rules[0].Carriers[0].File = "no_such_carrier_2099.go" })
	if probs := criticalRuleProblems(m3, systemPrompt, read); len(probs) == 0 {
		t.Error("③ 载体改名未被捕获")
	} else {
		t.Logf("③ 载体改名 → 报红 %d 条: %s", len(probs), probs[0])
	}
	// ④ anchor 抹掉 → 文件内找不到锚 (把"有判据"降级成"文件存在")
	m4 := mutate(func(m *criticalRulesManifest) {
		m.Rules[0].Carriers[0].Anchor = "不存在的锚_2099"
	})
	if probs := criticalRuleProblems(m4, systemPrompt, read); len(probs) == 0 {
		t.Error("④ anchor 抹掉未被捕获")
	} else {
		t.Logf("④ anchor 抹掉 → 报红 %d 条: %s", len(probs), probs[0])
	}
	// ⑤ 把 judge 全降级为 process → 下沉水位下降
	m5 := mutate(func(m *criticalRulesManifest) {
		for i := range m.Rules[0].Carriers {
			m.Rules[0].Carriers[i].Kind = "process"
		}
	})
	if probs := criticalRuleProblems(m5, systemPrompt, read); len(probs) == 0 {
		t.Error("⑤ 下沉水位下降未被捕获 (水位必须只升不降)")
	} else {
		t.Logf("⑤ judge 降级为 process → 报红: %s", probs[len(probs)-1])
	}
}

// TestCriticalRuleCarriers_Tracked 清单必须被 git 跟踪 —— 否则改动不可见/不可回滚。
func TestCriticalRuleCarriers_Tracked(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, criticalRulesManifestPath)); err != nil {
		t.Skipf("清单不在位: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("无 git: %v", err)
	}
	out, err := exec.Command("git", "-C", wd, "ls-files", "--error-unmatch", criticalRulesManifestPath).CombinedOutput()
	if err != nil {
		t.Fatalf("清单未被 git 跟踪 (改动不可见/不可回滚, 违反公理四): %v\n%s", err, out)
	}
	t.Logf("清单已被 git 跟踪: %s", criticalRulesManifestPath)
}
