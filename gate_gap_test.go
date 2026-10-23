package main

// ============================================================================
// gate_gap_test.go —— 判据缺口检测的接线与鉴别力哨兵 (20261007)
//
// 动机 (反证法, 实测):
//   原假设「消耗层代码重复 -> 该沉淀」被数据推翻 —— 12213 次非空 gate 调用里,
//   跨日期重复的代码指纹仅 15 个 (0.12%), 且全是已沉淀项 (self deploy /
//   quick_test.cmd / forge_guard.py check)。消耗层是真一次性, 该通道无信号可收。
//   反向测量发现真信号在失败侧: 同一失败指纹跨 >=3 个不同日期的有 26 个,
//   最大项 python 超时 459 次 / 38 个日期 —— 「同一个坑踩了 38 天」= 判据没拦住。
//   watchdog 只按 lang 聚合失败率, 看不见「哪一类失败在反复发生」。
//
// 本文件钉住三件事 (接线只能由死程序判定):
//   1. 判据向量全通过 (gate_gap.py --selftest, 判据本身可测)
//   2. 接线真存在 (watchdog 源码剥注释后必须真调用 gate_gap.check —— 注释里
//      提一句不算接线, 这是实测过的漏检形态)
//   3. 鉴别力: 沙箱里改坏判据 -> selftest 必须报红
//      (「判据全绿」不等于「判据有效」—— 唯一证据是改坏生产代码后判据报红)
// ============================================================================

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const gateGapPy = "defense_system/gate_gap.py"
const gateGapWatchdog = "defense_system/forge_watchdog.py"

// gateGapRun 在指定脚本路径上跑 --selftest (死程序判定)。
func gateGapRun(t *testing.T, script string) (string, error) {
	t.Helper()
	cmd := exec.Command(guardGatePython(), script, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestGateGapSelftest 判据自身可测: 8 向量必须全过。
func TestGateGapSelftest(t *testing.T) {
	if _, err := os.Stat(gateGapPy); err != nil {
		t.Skip("gate_gap.py 未就位")
	}
	out, err := gateGapRun(t, gateGapPy)
	if err != nil {
		t.Fatalf("--selftest 失败: %v\n%s", err, out)
	}
	if !strings.Contains(out, "自检: 通过") || strings.Contains(out, "FAIL") {
		t.Fatalf("--selftest 输出异常: %s", out)
	}
}

// TestGateGapWired watchdog 必须真调用 gate_gap.check (剥注释后)。
//
// 为什么剥注释: 解释接线理由的注释里同样含 gate_gap.check —— 只做子串包含
// 会漏检 (实测教训: 删掉接线行保留注释, 哨兵静默通过)。
func TestGateGapWired(t *testing.T) {
	raw, err := os.ReadFile(gateGapWatchdog)
	if err != nil {
		t.Fatalf("读 watchdog 失败: %v", err)
	}
	callSeen := false
	for _, line := range strings.Split(string(raw), "\n") {
		code := line
		if i := strings.Index(code, "#"); i >= 0 {
			code = code[:i]
		}
		if strings.Contains(code, "gate_gap.check(") {
			callSeen = true
		}
	}
	if !callSeen {
		t.Fatal("watchdog 未真调用 gate_gap.check —— 判据接线已断 (注释提及不算接线)")
	}
}

// TestGateGapDiscrimination 鉴别力: 沙箱改坏判据 -> selftest 必须报红。
//
// 两个变异, 覆盖两个独立分支:
//
//	MIN_DAYS 归零     -> 「天数不足被拒」向量失效 (判据变成什么都收)
//	拒绝标记清空      -> 「判据生效」被当缺口报 (成功当失败, 告警贬值)
//	超时标记清空      -> 超时(预算判据生效)被当缺口报 (20261010 实测缺陷形态:
//	                     python 超时 337 次/26 天自动升 warn, 常驻启动横幅)
//	判据生效stage清空 -> 编译/解析/前置校验类(判据生效)被当缺口报 (20261013 实测
//	                     形态: 8 项缺口候选里 5 项 115 次全是 LLM 写错代码被编译器
//	                     拦下, 每天往告警流写 4-7 条 info = 狼来了)
//	判定体标记清空    -> math/logic 的「输入不合法」JSON 应答被当缺口报 (同批实测)
func TestGateGapDiscrimination(t *testing.T) {
	base, err := os.ReadFile(gateGapPy)
	if err != nil {
		t.Skip("gate_gap.py 未就位")
	}
	// 沙箱须保持 defense_system/ 相对结构: _wired() 找同目录的 forge_watchdog.py
	muts := []struct {
		name string
		from string
		to   string
	}{
		{"MIN_DAYS归零", "MIN_DAYS = 3", "MIN_DAYS = 0"},
		{"拒绝标记清空", "REFUSAL_MARKS = ('拒绝', '被拒', '已退役')", "REFUSAL_MARKS = ()"},
		{"超时标记清空", "TIMEOUT_MARKS = ('deadline exceeded', 'timed out', 'timeout', '超时')", "TIMEOUT_MARKS = ()"},
		{"判据生效stage清空", "GATE_REFUSAL_STAGES = ('rejected', 'retired', 'parse', 'compile')", "GATE_REFUSAL_STAGES = ()"},
		{"判定体标记清空", `RESULT_PROTOCOL_MARKS = ('"ok":false', '"ok": false')`, "RESULT_PROTOCOL_MARKS = ()"},
	}
	for _, m := range muts {
		t.Run(m.name, func(t *testing.T) {
			dir := t.TempDir()
			ds := filepath.Join(dir, "defense_system")
			if err := os.MkdirAll(ds, 0o755); err != nil {
				t.Fatalf("建沙箱失败: %v", err)
			}
			mutated := strings.Replace(string(base), m.from, m.to, 1)
			if mutated == string(base) {
				t.Fatalf("变异未命中: %q 不存在 —— 判据文本已改, 请同步本测试", m.from)
			}
			if err := os.WriteFile(filepath.Join(ds, "gate_gap.py"), []byte(mutated), 0o644); err != nil {
				t.Fatalf("写沙箱脚本失败: %v", err)
			}
			wd, err := os.ReadFile(gateGapWatchdog)
			if err != nil {
				t.Fatalf("读 watchdog 失败: %v", err)
			}
			if err := os.WriteFile(filepath.Join(ds, "forge_watchdog.py"), wd, 0o644); err != nil {
				t.Fatalf("写沙箱 watchdog 失败: %v", err)
			}
			out, err := gateGapRun(t, filepath.Join(ds, "gate_gap.py"))
			if err == nil {
				t.Fatalf("变异 %s 未被判据抓到 —— 判据形同虚设:\n%s", m.name, out)
			}
			if !strings.Contains(out, "FAIL") {
				t.Fatalf("变异 %s 报错但未标 FAIL: %s", m.name, out)
			}
		})
	}
}
