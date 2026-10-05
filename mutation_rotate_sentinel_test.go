package main

// ============================================================================
// mutation_rotate_sentinel_test.go —— 变异探针的轮转覆盖面与还原判据 (20261004)
//
// 背景: defense_system/mutation_probe.py 是「判据鉴别力探针」—— 改坏生产代码,
// 判据必须报红。它 20261003 就进了仓库, 但直到 20261004 都不在 hourly TASKS 里:
// 留痕 25 条全是人工 --only 跑的单条, 等于「探针只在想起来时才存在」。
// 接进调度后冒出两个新的失效形态, 二者原本都没有判据:
//
//   ① 轮转漏条目: 探针每次只跑 N 条(游标环形推进)。manifest 加了新 target,
//      若轮转逻辑有洞 -> 该条目永远轮不到, 而探针每次跑都全绿。
//      (同型缺陷已发生过: 3 个端到端用例从未进变异 run, 清单哨兵却全绿)
//   ② 还原失败静默: 探针临时改的是**生产源码**。还原失败若不影响退出码,
//      rc=0 静默通过, 留痕里也看不出「代码已被改坏」。
//
// 本文件把这两条钉成死程序判据。判据自身也要有判据:
//   - 覆盖面判据必须有鉴别力: 「少跑一次」时它必须报「覆盖不全」(否则恒真)。
//   - Go 侧复刻的轮转算法必须与**真实 Python 实现**逐条一致 (两侧同判据)。
//     只写 Go 复刻 = 哨兵验证的是「我以为的算法」, 探针改算法时哨兵会一直绿着。
// ============================================================================

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	mutationProbePyFile    = "defense_system/mutation_probe.py"
	mutationHourlyPyFile   = "defense_system/hourly.py"
	mutationRotateStepMax  = 5  // 单次调用最多改几条生产源码
	mutationRotateCycleMax = 48 // 全清单覆盖周期上限(次调用, 约 2 天)
)

// mutationRotateSeq 复刻 mutation_probe.pick_rotate: 游标从 0 起, 每次取 step 条
// **连续**区间 (不是等距抽样), 游标 += step 后取模。
func mutationRotateSeq(total, step, calls int) []int {
	out := []int{}
	if total <= 0 || calls <= 0 {
		return out
	}
	if step < 1 {
		step = 1
	}
	if step > total {
		step = total
	}
	cur := 0
	for i := 0; i < calls; i++ {
		for j := 0; j < step; j++ {
			out = append(out, (cur+j)%total)
		}
		cur = (cur + step) % total
	}
	return out
}

// mutationRotateCalls 覆盖全清单所需的调用次数。
func mutationRotateCalls(total, step int) int {
	if total <= 0 || step <= 0 {
		return 0
	}
	if step > total {
		step = total
	}
	return (total + step - 1) / step
}

// mutationRotateCovered 判定序列是否把 0..total-1 每条都取到过。
func mutationRotateCovered(seq []int, total int) bool {
	if total <= 0 {
		return false
	}
	seen := make([]bool, total)
	for _, v := range seq {
		if v < 0 || v >= total {
			return false
		}
		seen[v] = true
	}
	for _, b := range seen {
		if !b {
			return false
		}
	}
	return true
}

func TestMutationRotateCoverage(t *testing.T) {
	cases := [][2]int{{29, 1}, {29, 2}, {29, 3}, {29, 5}, {6, 3}, {6, 4}, {1, 1}, {7, 7}}
	for _, c := range cases {
		total, step := c[0], c[1]
		need := mutationRotateCalls(total, step)
		if !mutationRotateCovered(mutationRotateSeq(total, step, need), total) {
			t.Errorf("total=%d step=%d: %d 次调用未覆盖全集 —— 轮转会永久跳过条目",
				total, step, need)
		}
		// 判据的判据: 少跑一次必须判「覆盖不全」, 否则本判据恒真、无鉴别力
		if need > 1 && mutationRotateCovered(mutationRotateSeq(total, step, need-1), total) {
			t.Errorf("total=%d step=%d: 少跑一次仍判「全覆盖」—— 覆盖面判据无鉴别力",
				total, step)
		}
	}
	// fail-closed: 畸形输入不得被当成「覆盖 OK」
	if mutationRotateCovered(mutationRotateSeq(0, 1, 1), 0) {
		t.Error("total=0 被判为覆盖全集 —— fail-closed 失效")
	}
	if mutationRotateCovered([]int{0, 1, 5}, 3) {
		t.Error("越界索引被判为覆盖全集 —— fail-closed 失效")
	}
}

func mutationPythonExe(t *testing.T) string {
	t.Helper()
	for _, c := range []string{"python", "python3", "py"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Fatalf("找不到 python 解释器 —— 轮转算法交叉验证无法执行 (fail-closed, 不静默跳过)")
	return ""
}

// mutationPythonSeq 调**真实** mutation_probe.pick_rotate, 返回逐条索引序列。
// 为什么必须交叉验证: Go 复刻只是「我以为的算法」。探针改了算法而哨兵不同步 ->
// 哨兵会一直绿着验证一个不存在的实现 (两侧判据必须同构, 见 selfheal/forge_guard 那次事故)。
func mutationPythonSeq(t *testing.T, total, step, calls int) []int {
	t.Helper()
	script := strings.Join([]string{
		"import json, os, sys",
		`sys.path.insert(0, os.path.join(os.getcwd(), "defense_system"))`,
		"import mutation_probe as mp",
		fmt.Sprintf("total, step, calls = %d, %d, %d", total, step, calls),
		`targets = [{"id": "i" + str(i)} for i in range(total)]`,
		"cur = 0",
		"seq = []",
		"for _ in range(calls):",
		"    mp._load_cursor = (lambda c: (lambda: c))(cur)",
		"    sel, nxt = mp.pick_rotate(targets, step)",
		`    seq += [int(x["id"][1:]) for x in sel]`,
		"    cur = nxt",
		"sys.stdout.write(json.dumps(seq))",
	}, "\n")
	cmd := exec.Command(mutationPythonExe(t), "-c", script)
	cmd.Dir = "."
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1", "PYTHONIOENCODING=utf-8")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("调用真实 pick_rotate 失败: %v (%s)", err, out)
	}
	var seq []int
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &seq); err != nil {
		t.Fatalf("解析 pick_rotate 输出失败: %v (原始: %q)", err, string(out))
	}
	return seq
}

func TestMutationRotateMatchesPython(t *testing.T) {
	for _, c := range [][3]int{{29, 1, 29}, {29, 3, 10}, {6, 3, 2}, {5, 5, 1}} {
		total, step, calls := c[0], c[1], c[2]
		want := mutationRotateSeq(total, step, calls)
		got := mutationPythonSeq(t, total, step, calls)
		if len(want) != len(got) {
			t.Fatalf("total=%d step=%d calls=%d: 长度不一致 go=%d py=%d",
				total, step, calls, len(want), len(got))
		}
		for i := range want {
			if want[i] != got[i] {
				t.Fatalf("total=%d step=%d: 第 %d 条不一致 go=%d py=%d —— 哨兵复刻与真实实现已漂移",
					total, step, i, want[i], got[i])
			}
		}
	}
}

// mutationStripPyComments 剥掉 # 之后的注释 —— 注释里提到接线不算接线。
func mutationStripPyComments(src string) string {
	var out []string
	for _, ln := range strings.Split(src, "\n") {
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

var mutationRotateArgRe = regexp.MustCompile(`--rotate['",\s]+(\d+)`)

// TestMutationRotateWiring 钉住 hourly 里 mutation 任务的必需参数与步长上界。
func TestMutationRotateWiring(t *testing.T) {
	raw, err := os.ReadFile(mutationHourlyPyFile)
	if err != nil {
		t.Fatalf("hourly.py 读取失败 (fail-closed): %v", err)
	}
	line := ""
	for _, ln := range strings.Split(mutationStripPyComments(string(raw)), "\n") {
		if strings.Contains(ln, "mutation_probe.py") {
			line = ln
			break
		}
	}
	if line == "" {
		t.Fatalf("hourly.py 未接线 mutation_probe.py —— 探针只能手动跑, 等于只在想起来时才存在")
	}
	for _, need := range []string{"--apply", "--rotate"} {
		if !strings.Contains(line, need) {
			t.Errorf("mutation 任务缺必需参数 %q: %s", need, strings.TrimSpace(line))
		}
	}
	m := mutationRotateArgRe.FindStringSubmatch(line)
	if m == nil {
		t.Fatalf("解析不出 --rotate 步长: %s", strings.TrimSpace(line))
	}
	step, _ := strconv.Atoi(m[1])
	if step < 1 || step > mutationRotateStepMax {
		t.Errorf("--rotate %d 越界 (允许 1..%d): 每次调用都要临时改生产源码, 步长必须有上界",
			step, mutationRotateStepMax)
	}
	total := len(loadMutationManifest(t).Targets)
	cycles := mutationRotateCalls(total, step)
	if cycles > mutationRotateCycleMax {
		t.Errorf("全清单 %d 条 / 步长 %d = %d 次调用才覆盖一遍, 超过上限 %d",
			total, step, cycles, mutationRotateCycleMax)
	}
	t.Logf("mutation 轮转: 每次 %d 条 / 清单 %d 条 -> 覆盖周期 %d 次调用", step, total, cycles)
}

// TestMutationRestoreJudgement 钉住「还原失败不得静默」的判据面。
// 探针临时改的是生产源码, 还原链条 = 单文件 sha 校验 -> atexit 兜底 -> final_verify 终检
// -> git checkout 第二道兜底。缺任何一环, 「代码被改坏」都能以 rc=0 通过。
func TestMutationRestoreJudgement(t *testing.T) {
	raw, err := os.ReadFile(mutationProbePyFile)
	if err != nil {
		t.Fatalf("mutation_probe.py 读取失败 (fail-closed): %v", err)
	}
	code := mutationStripPyComments(string(raw))
	must := []struct{ what, frag string }{
		{"还原终检函数缺失", "def final_verify("},
		{"终检未被调用", "restore_bad = final_verify()"},
		{"终检失败未影响退出码", "if restore_bad:\n        return 3"},
		{"缺 git 第二道还原兜底", "def _git_restore("},
		{"留痕缺 restore_ok 字段", `"restore_ok": not restore_bad`},
		{"还原失败仍推进游标(会跳过未验证条目)", "if rot_next is not None and not restore_bad:"},
		{"缺变异前 sha 台账", "_TOUCHED[path] = _sha(orig)"},
		{"游标非原子写(半截文件会让轮转退回 0)", "os.replace(tmp, CURSOR)"},
	}
	for _, m := range must {
		if !strings.Contains(code, m.frag) {
			t.Errorf("%s —— 找不到 %q", m.what, m.frag)
		}
	}
}
