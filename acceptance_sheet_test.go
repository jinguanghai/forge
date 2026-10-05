package main

// acceptance_sheet_test.go — 验收单自身的判据 (P3-2, 20261005)
//
// 背景: 所有判据都是同一双手写、同一双手跑、同一双手报告。若某处【稳定地错】
// (不是崩溃, 是安静地算错), 没有任何东西会响 —— 因为"证据"本身也是自己产出的。
// defense_system/acceptance_sheet.py 把每个数字的来源换成外部真值 (Go 工具链 /
// git / 文件系统 / 操作系统调度器)。
//
// 但一份验收单最容易退化成装饰品: 永远说"一切正常"。所以它的价值必须被钉住:
//   ① 结构完整   —— 每项检查带 source 字段, 且 source 值域封闭 (防拼错/新值漂移)
//   ② 有鉴别力   —— --selftest 注入"错误的世界", 必须报红 (rc=1)。
//                   若注入的假数据未被抓到, 这份验收单就是废纸
//   ③ 真值外部   —— 外部真值占比 >= 90% (防它退回"读自产日志"的自证循环)
//   ④ 自身干净   —— --audit-self 必须 rc=0 (脚本自己不读自产日志)
//
// 判据必须能被测到, 否则它自己就是下一个盲区 (公理三)。

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const acceptanceSheetPath = "defense_system/acceptance_sheet.py"

// acceptanceSourceDomain 是验收单检查项 source 字段的封闭值域 ——
// 必须与 acceptance_sheet.py 的 SOURCE_DOMAIN 逐元素相同。
// 外部五类 (go/git/fs/os/py) + self (仅"真值来源自检"一项)。
var acceptanceSourceDomain = []string{"go", "git", "fs", "os", "py", "self"}

type acceptanceCheck struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Status   string `json:"status"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence"`
}

type acceptanceReport struct {
	OK            bool              `json:"ok"`
	RC            int               `json:"rc"`
	ExternalRatio float64           `json:"external_ratio"`
	Checks        []acceptanceCheck `json:"checks"`
}

// acceptanceSourceAllowed 判定 source 是否落在封闭值域内。
func acceptanceSourceAllowed(s string) bool {
	for _, ok := range acceptanceSourceDomain {
		if ok == s {
			return true
		}
	}
	return false
}

// runAcceptanceSheet 跑验收单一次, 返回 (rc, 合并输出)。工具不在位则 skip。
func runAcceptanceSheet(t *testing.T, args ...string) (int, string) {
	t.Helper()
	if _, err := os.Stat(acceptanceSheetPath); err != nil {
		t.Skipf("验收单不在位 (%v) —— 本哨兵只在完整炉体内生效", err)
	}
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	cmd := exec.Command(py, append([]string{"-u", acceptanceSheetPath}, args...)...)
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	out, err := cmd.CombinedOutput()
	rc := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("验收单启动失败: %v\n%s", err, string(out))
		}
		rc = ee.ExitCode()
	}
	return rc, string(out)
}

// TestAcceptanceSheetStructured: 验收单必须能跑, 且每项检查结构完整。
// 只判"结构"不判"内容" —— 内容取决于环境状态 (工作区是否干净等),
// 把环境状态写进判据 = 判据随环境随机报红。
func TestAcceptanceSheetStructured(t *testing.T) {
	rc, out := runAcceptanceSheet(t, "--json")
	if rc != 0 && rc != 1 {
		t.Fatalf("验收单应返回 0(全绿) 或 1(有报红), 实际 rc=%d\n%s", rc, out)
	}
	line := strings.TrimSpace(out)
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = strings.TrimSpace(line[i+1:])
	}
	var rep acceptanceReport
	if err := json.Unmarshal([]byte(line), &rep); err != nil {
		t.Fatalf("验收单 JSON 不可解析: %v\n%s", err, out)
	}
	if len(rep.Checks) < 10 {
		t.Fatalf("检查项只有 %d 项, 少于 10 项 (验收单被削薄?)", len(rep.Checks))
	}
	for _, c := range rep.Checks {
		if c.Name == "" || c.Detail == "" {
			t.Errorf("检查项结构不全: %+v", c)
		}
		if c.Status != "ok" && c.Status != "fail" && c.Status != "skip" {
			t.Errorf("%s 的 status 非法: %q", c.Name, c.Status)
		}
		if !acceptanceSourceAllowed(c.Source) {
			t.Errorf("%s 的 source=%q 不在封闭值域 %v (新值未经登记 = 静默漂移)",
				c.Name, c.Source, acceptanceSourceDomain)
		}
	}
	// rc 与 ok 必须自洽 (fail 项存在 <=> rc=1)
	hasFail := false
	for _, c := range rep.Checks {
		if c.Status == "fail" {
			hasFail = true
		}
	}
	if hasFail != (rc == 1) || rep.OK != (rc == 0) {
		t.Fatalf("rc=%d 与判定不一致 (hasFail=%v, ok=%v)", rc, hasFail, rep.OK)
	}
}

// TestAcceptanceSheetSelftestHasTeeth: 鉴别力实证。
//
// --selftest 在临时目录里造一个"错误的世界"(记忆声称的 gate 面数比源码少一面),
// 验收单必须当场报红 (rc=1)。若 rc=0 —— 注入的假数据没被抓到,
// 这份验收单就会稳定地说"一切正常", 正是 P3-2 要防的东西。
func TestAcceptanceSheetSelftestHasTeeth(t *testing.T) {
	rc, out := runAcceptanceSheet(t, "--selftest")
	if rc != 1 {
		t.Fatalf("鉴别力自检应报红 (rc=1), 实际 rc=%d —— 假数据未被抓到\n%s", rc, out)
	}
	if !strings.Contains(out, "鉴别力自检通过") {
		t.Fatalf("自检未确认通过: %s", out)
	}
	if !strings.Contains(out, "13 面") || !strings.Contains(out, "14 面") {
		t.Fatalf("自检未打印注入的假声称细节: %s", out)
	}
}

// TestAcceptanceSheetTruthIsExternal: 真值外置率下限。
//
// 这条是验收单的存在理由: 若它的数字也来自铸剑炉自己写的日志, 它只是把
// 自证循环包装得更漂亮。结构断言要求外部来源 (go/git/fs/os/py) 占比 >= 90%。
func TestAcceptanceSheetTruthIsExternal(t *testing.T) {
	rc, out := runAcceptanceSheet(t, "--json")
	if rc != 0 && rc != 1 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	line := strings.TrimSpace(out)
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = strings.TrimSpace(line[i+1:])
	}
	var rep acceptanceReport
	if err := json.Unmarshal([]byte(line), &rep); err != nil {
		t.Fatalf("JSON 不可解析: %v", err)
	}
	if rep.ExternalRatio < 0.90 {
		t.Fatalf("外部真值占比 %.0f%% 低于下限 90%% —— 验收单退化为自证循环",
			rep.ExternalRatio*100)
	}
	// self 类只允许出现在"真值来源自检"这一项上
	for _, c := range rep.Checks {
		if c.Source == "self" && !strings.Contains(c.Name, "自检") {
			t.Errorf("%s 用了 self 来源 —— 只有真值来源自检可以", c.Name)
		}
	}
}

// TestAcceptanceSheetDoesNotReadSelfLogs: 脚本自身不得读自产日志。
//
// 这是"判据的判据"里最容易被自己绕过的一条: 脚本一边宣称"真值外部",
// 一边偷偷读 gate_audit.jsonl 对答案。--audit-self 扫源码证明它没这么干。
func TestAcceptanceSheetDoesNotReadSelfLogs(t *testing.T) {
	rc, out := runAcceptanceSheet(t, "--audit-self")
	if rc != 0 {
		t.Fatalf("验收单真值来源自检失败 (rc=%d): %s", rc, out)
	}
	if !strings.Contains(out, "0 处引用自产日志") {
		t.Fatalf("自检未确认干净: %s", out)
	}
}
