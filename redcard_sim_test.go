package main

// ============================================================================
// redcard_sim_test.go —— 红牌机制「到期 -> 隔离 -> 回滚」路径的接线哨兵
//
// 缺陷 (20260928 实测):
//   hygiene_redcard.py 的到期处置路径在生产环境几乎走不到 —— 要真有垃圾在
//   根目录躺满 3 天才行。实测台账 4 行全是 open->resolved (人工删掉后销牌),
//   _archive/hygiene_quarantine 至今不存在。即: 到期判定 / 隔离移动 / 销牌 /
//   复活 / 占用跳过 / 目录不隔离 / 回滚 —— 覆盖全为零。
//   而「隔离移动」是唯一会动用户文件的一步, 出事代价最高。
//   「观察 1~2 周」等的是垃圾出现, 不是这些路径被走通 (两周可能只来 1 张红牌)。
//
// 修法: defense_system/redcard_sim.py 用 FORGE_REDCARD_ROOT 把根指向沙箱,
// 把 11 条路径真跑一遍。本文件把三件事钉死 (接线只能由死程序判定):
//   1. 真实跑一遍, 11 场景 + 3 条真实工作区对账必须全通过
//   2. 场景表与 case 函数一一对应 (从表里删一项 = 静默失去覆盖)
//   3. 沙箱注入 + preflight fail-closed 同时在场 (少一个就会拿真实根跑 --apply)
//
// 已知限制 (如实标注, 不假装没有):
//   go test 的结果缓存只认 Go 侧输入 —— 只改 .py 时, 不加 -count=1 可能直接复用
//   上次的 PASS, 哨兵根本没跑。故: 跑本哨兵一律带 -count=1
//   (仓库既有约定: fractal_check.py 用的就是 -count=1); 变异验证同理。
//
// 为什么断言走 Python AST 而不是字符串包含:
//   注释里同样会出现 "FORGE_REDCARD_ROOT" / "preflight" 这些词 ——
//   用 Contains 判定会把「解释性注释」当成「代码存在」(元语境假阳性, 踩过多次)。
//   AST 里没有注释, 于是「代码里真的写了」才为真。
// ============================================================================

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const redcardSimRel = "defense_system/redcard_sim.py"
const redcardScriptRel = "defense_system/hygiene_redcard.py"

// redcardProbe 用 Python AST 抽取事实: 注释不进 AST, 故不会被"注释里提到"骗过。
const redcardProbe = `
import ast, json

def load(p):
    with open(p, encoding='utf-8') as f:
        return ast.parse(f.read())

def strs(t):
    return [n.value for n in ast.walk(t)
            if isinstance(n, ast.Constant) and isinstance(n.value, str)]

def names(t):
    return [n.id for n in ast.walk(t) if isinstance(n, ast.Name)]

sim = load('defense_system/redcard_sim.py')
rc = load('defense_system/hygiene_redcard.py')

sim_funcs = sorted(n.name for n in sim.body if isinstance(n, ast.FunctionDef))
cases = []
for n in sim.body:
    if isinstance(n, ast.Assign) and any(getattr(t, 'id', None) == 'CASES' for t in n.targets):
        for el in n.value.elts:
            cases.append([el.elts[0].value, el.elts[1].id])

main_fn = [n for n in sim.body if isinstance(n, ast.FunctionDef) and n.name == 'main'][0]
pf = [n.lineno for n in ast.walk(main_fn) if isinstance(n, ast.Call)
      and isinstance(n.func, ast.Name) and n.func.id == 'preflight']
loop = [n.lineno for n in ast.walk(main_fn) if isinstance(n, ast.For)
        and isinstance(n.iter, ast.Name) and n.iter.id == 'CASES']
failclosed = False
for n in ast.walk(main_fn):
    if isinstance(n, ast.If) and isinstance(n.test, ast.UnaryOp) \
            and isinstance(n.test.operand, ast.Call):
        f = n.test.operand.func
        if isinstance(f, ast.Name) and f.id == 'preflight':
            for x in n.body:
                if isinstance(x, ast.Return) and isinstance(x.value, ast.Constant) \
                        and x.value.value == 2:
                    failclosed = True

print(json.dumps({
    'sim_funcs': sim_funcs,
    'cases': cases,
    'preflight_before_loop': bool(pf) and bool(loop) and min(pf) < min(loop),
    'failclosed': failclosed,
    'rc_strs': strs(rc),
    'rc_names': names(rc),
}, ensure_ascii=False))
`

// redcardFacts 是 AST 探针输出的事实子集。
type redcardFacts struct {
	SimFuncs            []string    `json:"sim_funcs"`
	Cases               [][2]string `json:"cases"`
	PreflightBeforeLoop bool        `json:"preflight_before_loop"`
	FailClosed          bool        `json:"failclosed"`
	RcStrs              []string    `json:"rc_strs"`
	RcNames             []string    `json:"rc_names"`
}

// redcardFactsOf 跑 AST 探针拿事实 (fail-closed: 跑不起来即 Fatal)。
func redcardFactsOf(t *testing.T) redcardFacts {
	t.Helper()
	cmd := exec.Command(guardGatePython(), "-c", redcardProbe)
	// PYTHONDONTWRITEBYTECODE: 别让探针在 defense_system/ 留 __pycache__。
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AST 探针执行失败 (fail-closed, 不放行): %v\n%s", err, out)
	}
	var f redcardFacts
	if err := json.Unmarshal(bytes.TrimSpace(out), &f); err != nil {
		t.Fatalf("探针输出不是合法 JSON (fail-closed): %v\n%s", err, out)
	}
	return f
}

// TestRedcardSimRealRun 跑真实模拟器: 11 场景 + 真实工作区对账必须全通过。
func TestRedcardSimRealRun(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(guardGatePython(), redcardSimRel)
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("模拟器未全通过 (fail-closed): %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "个场景通过") {
		t.Errorf("模拟器未报「N 个场景通过」:\n%s", s)
	}
	for _, want := range []string{"R1 真实根目录一级项未变", "R2 真实台账字节未变",
		"R3 真实告警无模拟残留", "R4 真实告警无红牌类新增"} {
		if !strings.Contains(s, "PASS  "+want) {
			t.Errorf("真实工作区对账缺失或失败: %s", want)
		}
	}
	if strings.Contains(s, "FAIL ") {
		t.Errorf("模拟器输出含 FAIL:\n%s", s)
	}
	t.Logf("红牌沙箱模拟: %s", redcardLastLine(s))
}

// redcardLastLine 取输出的结论行, 让日志可读。
func redcardLastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], "场景通过") {
			return strings.TrimSpace(lines[i])
		}
	}
	return "(无结论行)"
}

// TestRedcardSimCasesWired 场景表与 case 函数必须一一对应。
// 变异体: 从 CASES 里删一项 -> 本用例报红 (删表项 = 静默失去该路径覆盖)。
func TestRedcardSimCasesWired(t *testing.T) {
	t.Parallel()
	f := redcardFactsOf(t)
	if len(f.Cases) < 11 {
		t.Fatalf("场景仅 %d 个, 少于 11 —— 路径覆盖被削减", len(f.Cases))
	}
	defined := map[string]bool{}
	for _, n := range f.SimFuncs {
		defined[n] = true
	}
	seen := map[string]bool{}
	for _, c := range f.Cases {
		label, fn := c[0], c[1]
		if !defined[fn] {
			t.Errorf("场景 %q 指向未定义的函数 %s", label, fn)
		}
		if seen[fn] {
			t.Errorf("函数 %s 被重复登记", fn)
		}
		seen[fn] = true
	}
	// 反向: 定义了却不在表里的 case 函数 = 备而未用 (跑了不算数)。
	for _, n := range f.SimFuncs {
		if strings.HasPrefix(n, "c0") && !seen[n] {
			t.Errorf("case 函数 %s 未被 CASES 引用 —— 备而未用", n)
		}
	}
	t.Logf("场景 %d 个, 全部有对应实现", len(f.Cases))
}

// TestRedcardSimFailClosed 沙箱注入与 preflight 必须同时在场。
// 少了任何一个, 模拟器就可能带着**真实根**跑 --apply (--days 0 让一切立即到期),
// 把真实根目录的违规文件移走 —— 那时"场景全通过"最危险。
func TestRedcardSimFailClosed(t *testing.T) {
	t.Parallel()
	f := redcardFactsOf(t)
	if !f.FailClosed {
		t.Errorf("模拟器缺 fail-closed 闸门: preflight 失败时必须 return 2 且不跑场景")
	}
	if !f.PreflightBeforeLoop {
		t.Errorf("preflight 未排在场景循环之前 —— 顺序错了等于没有闸门")
	}
	if !redcardHas(f.SimFuncs, "preflight") {
		t.Errorf("模拟器未定义 preflight")
	}
	if !redcardHas(f.SimFuncs, "snapshot_real") {
		t.Errorf("模拟器缺 snapshot_real —— 无事后对账, 注入失效时无人发现")
	}
	if !redcardHas(f.RcStrs, "FORGE_REDCARD_ROOT") {
		t.Errorf("%s 未在代码里读 FORGE_REDCARD_ROOT (注释里提到不算)", redcardScriptRel)
	}
	if !redcardHas(f.RcNames, "SANDBOX") {
		t.Errorf("%s 缺 SANDBOX 标志 —— 无法区分真实根与沙箱", redcardScriptRel)
	}
	if !redcardHas(f.RcStrs, "simulation") {
		t.Errorf("%s 未给沙箱告警打 src=simulation —— 探针流量将无法从真实告警中剔除",
			redcardScriptRel)
	}
}

// redcardHas 判断切片是否含目标串。
func redcardHas(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
