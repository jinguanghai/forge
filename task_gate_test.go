package main

// task_gate_test.go — 长任务通道哨兵 (P0-2, 20261002)
//
// 动机: 重任务物理上塞不进 30s 预算(全量 go test 实测 113.9s)。近 14 天实测
// 超时 435 条 = 全部失败的 50.6%, python 失败 87.7% 是超时, 白耗 6486s。
// 修法 = 把「提交 → task_id → 轮询」升为一等公民。本文件钉住三条不变量:
//
//	① 提交必须秒回, 且后台任务必须活过 gate 进程退出。后者是最脆的假设:
//	   Go 的 exec.Cmd.Wait 要等管道写端全部关闭, 孙进程若继承了 gate 的管道
//	   句柄, Wait 会一直阻塞到任务结束 —— submit 耗时≈任务时长, 通道名存实亡
//	   (且必然触发 25s 超时, 自相矛盾)。故断言 submit 耗时 < 3s。
//	② 状态查询绝不可缓存。缓存 = 同一 task_id 永远返回第一次的 running,
//	   轮询永不前进。由 CompilerDef.NoCache 豁免, 此处断言 CachedAt==0。
//	③ 接线四处齐全(清单/编译器表/注册表/分派), 缺一处静默失效。
//
// 隔离: 任务目录经 FORGE_ROOT 落 t.TempDir(), 生产 .forge/tasks 零污染
// (教训: 测试用 wd := "." 直写真实数据 —— 开关名无害 != 动作无害)。
import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// taskTestForge 构造隔离的 Forge: toolsDir 指回包目录(gate 脚本所在),
// 任务目录经 FORGE_ROOT 落到 t.TempDir()。
func taskTestForge(t *testing.T) *Forge {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wd, ForgeToolsDir, "task_gate.py")); err != nil {
		t.Skipf("task_gate.py 未安装: %v", err)
	}
	root := t.TempDir()
	t.Setenv("FORGE_ROOT", root)
	_, cfg, _ := newHandleCmdAgent(t)
	f := NewForge(root, cfg)
	f.toolsDir = filepath.Join(wd, ForgeToolsDir)
	t.Cleanup(f.Shutdown)
	return f
}

// taskStatusReply 解析 status/wait 回执。断言走 JSON 字段而非子串匹配:
// 子串断言对分隔符空格敏感(实测 `"state":"done"` 匹配不到 `"state": "done"`),
// 会把「格式微调」误报成「通道坏了」—— 判据必须咬语义不咬字节。
type taskStatusReply struct {
	OK     bool `json:"ok"`
	Result struct {
		TaskID   string  `json:"task_id"`
		State    string  `json:"state"`
		ExitCode int     `json:"exit_code"`
		Elapsed  float64 `json:"elapsed_sec"`
		TimedOut bool    `json:"timed_out"`
		Tail     string  `json:"tail"`
	} `json:"result"`
}

type taskSubmitReply struct {
	OK     bool `json:"ok"`
	Result struct {
		TaskID string `json:"task_id"`
		Log    string `json:"log"`
	} `json:"result"`
}

func taskSubmit(t *testing.T, f *Forge, cmd string) taskSubmitReply {
	t.Helper()
	out, res, err := f.Build(cmd, "task", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("提交失败: err=%v res=%+v out=%s", err, res, covTrunc(out, 400))
	}
	var sub taskSubmitReply
	if uerr := json.Unmarshal([]byte(res.Stdout), &sub); uerr != nil {
		t.Fatalf("提交回执非合法 JSON: %v / %s", uerr, covTrunc(res.Stdout, 400))
	}
	if !sub.OK || sub.Result.TaskID == "" {
		t.Fatalf("提交回执缺 task_id: %s", covTrunc(res.Stdout, 400))
	}
	return sub
}

// TestTaskGate_SubmitReturnsFastAndSurvivesGateExit 是通道存在的硬证据。
func TestTaskGate_SubmitReturnsFastAndSurvivesGateExit(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 task gate 子进程端到端")
	}
	f := taskTestForge(t)
	const marker = "TASKPROBE_OK"
	cmd := `python -c "import time;print('start',flush=True);time.sleep(6);print('` + marker + `')"`
	start := time.Now()
	sub := taskSubmit(t, f, cmd)
	submitDur := time.Since(start)
	t.Logf("submit 耗时 %v (后台任务本身 6s) —— 秒回证明孙进程未继承 gate 管道", submitDur)
	if submitDur > 3*time.Second {
		t.Fatalf("submit 未秒回: %v —— 孙进程继承了 gate 管道句柄, Wait 被阻塞到任务结束", submitDur)
	}
	deadline := time.Now().Add(18 * time.Second)
	last := ""
	var st taskStatusReply
	for time.Now().Before(deadline) {
		_, r2, _ := f.Build(`{"action":"status","id":"`+sub.Result.TaskID+`"}`, "task", "")
		if r2 == nil {
			t.Fatal("status 返回 nil")
		}
		last = r2.Stdout
		if json.Unmarshal([]byte(last), &st) == nil && st.Result.State != "running" {
			break
		}
		time.Sleep(700 * time.Millisecond)
	}
	if st.Result.State != "done" {
		t.Fatalf("任务未达 done: state=%q out=%s", st.Result.State, covTrunc(last, 500))
	}
	if st.Result.ExitCode != 0 {
		t.Errorf("exit_code=%d, want 0", st.Result.ExitCode)
	}
	if !strings.Contains(st.Result.Tail, marker) {
		t.Errorf("日志 tail 缺标记 %q (日志未落盘/未捕获): %s", marker, covTrunc(st.Result.Tail, 400))
	}
}

// TestTaskGate_StatusIsNeverCached 钉住 NoCache: 状态随外部进程推进, 缓存即废通道。
func TestTaskGate_StatusIsNeverCached(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 task gate 子进程端到端")
	}
	f := taskTestForge(t)
	sub := taskSubmit(t, f, `python -c "import time;time.sleep(5)"`)
	code := `{"action":"status","id":"` + sub.Result.TaskID + `"}`
	_, r1, _ := f.Build(code, "task", "")
	if r1 == nil || !r1.OK {
		t.Fatalf("首次 status 失败: %+v", r1)
	}
	if r1.CachedAt != 0 {
		t.Fatalf("首次 status 即报缓存命中 (CachedAt=%d)", r1.CachedAt)
	}
	var s1, s2 taskStatusReply
	if json.Unmarshal([]byte(r1.Stdout), &s1) != nil || s1.Result.State != "running" {
		t.Errorf("提交后立刻查询应为 running: %s", covTrunc(r1.Stdout, 300))
	}
	time.Sleep(7 * time.Second)
	_, r2, _ := f.Build(code, "task", "")
	if r2 == nil {
		t.Fatal("第二次 status 返回 nil")
	}
	if r2.CachedAt != 0 {
		t.Fatalf("status 命中缓存 (CachedAt=%d) —— NoCache 豁免失效, 轮询永远返回旧状态", r2.CachedAt)
	}
	if json.Unmarshal([]byte(r2.Stdout), &s2) != nil || s2.Result.State != "done" {
		t.Errorf("7s 后应为 done: %s", covTrunc(r2.Stdout, 400))
	}
}

// TestTaskGate_UnknownIDFails 拒绝/失败不得静默成功 (同 media gate 教训)。
func TestTaskGate_UnknownIDFails(t *testing.T) {
	f := taskTestForge(t)
	r := f.forgeGate(`{"action":"status","id":"t_no_such_task"}`, "task", "")
	if r.OK {
		t.Fatalf("未知 task_id 必须失败, 不得静默成功: %s", covTrunc(r.Stdout, 200))
	}
	if msg := r.Error + r.Stdout; !strings.Contains(msg, "未知 task_id") {
		t.Errorf("失败信息应说明未知 task_id, 实际: %s", covTrunc(msg, 300))
	}
}

// TestTaskGate_RegistryWired 接线哨兵: 清单/表/注册表/判定输出四处必须都有 task。
func TestTaskGate_RegistryWired(t *testing.T) {
	found := false
	for _, g := range 铸剑炉_GATES {
		if g == "task" {
			found = true
		}
	}
	if !found {
		t.Fatal("铸剑炉_GATES 缺 task")
	}
	comp, ok := 铸剑炉_COMPILERS["task"]
	if !ok {
		t.Fatal("铸剑炉_COMPILERS 缺 task (超时/执行配置将落默认)")
	}
	if !comp.SelfHosted {
		t.Error("task 必须标 SelfHosted")
	}
	if comp.ExecTimeout <= 0 || comp.ExecTimeout > 28*time.Second {
		t.Errorf("task ExecTimeout=%v, 须落在 (0, 28s]: 本 gate 只做提交/查询, "+
			"调大只是把贴边线一起抬高", comp.ExecTimeout)
	}
	if !comp.NoCache {
		t.Error("task 必须标 NoCache (状态随外部进程推进, 缓存=通道失效)")
	}
	regFound := false
	for _, g := range gateRegistry {
		if g.Name == "task" {
			regFound = true
		}
	}
	if !regFound {
		t.Fatal("gateRegistry 缺 task (专家路由 prompt 不会提到它 → 通道无人使用)")
	}
	if !mustHaveOutputGates["task"] {
		t.Error("task 必须列入 mustHaveOutputGates (空输出=判定缺席, 不可当成功)")
	}
}

// TestTaskGate_ScriptPresent gate 脚本必须存在且非空。
func TestTaskGate_ScriptPresent(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(wd, ForgeToolsDir, "task_gate.py")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("task_gate.py 缺失 —— 分派到死边界直接失败: %v", err)
	}
	if st.Size() == 0 {
		t.Fatal("task_gate.py 为空")
	}
}

// TestTaskGate_RejectsSubcommandStyleArgs 钉住「多余参数 fail-closed」(20261002 就位验证实测)。
//
// 事故形态: 子命令式误用 `task_gate.py status <task_id>` —— argv[1]="status" 非 JSON,
// 旧版按「裸文本 = 提交命令」处理并静默忽略 argv[2:], 于是:
//
//	① 回 ok:true + 新 task_id (假成功, 上层以为查到了状态)
//	② 入队一个注定 exit 1 的垃圾任务 (实测一次误用刷 3 个, 无任何报错)
//
// 裸文本=submit 是设计(方便直接提交命令), 故只对「多余参数」拒绝。
// 三向量判据: ① 非零退出 ② 错误文本指出正确写法 ③ 零副作用(任务目录项数不变)。
// 反例向量: 单参数 JSON 调用必须照常工作 —— 防「一律拒绝」把通道打死。
// 变异自检: 删掉 main() 的 len(sys.argv)>2 分支 -> 向量①③必红。
func TestTaskGate_RejectsSubcommandStyleArgs(t *testing.T) {
	f := taskTestForge(t)
	script := filepath.Join(f.toolsDir, "task_gate.py")
	taskDir := filepath.Join(os.Getenv("FORGE_ROOT"), ".forge", "tasks")
	// 目录尚未创建也算 0 项 —— 且「误用不得把它创建出来」是比「项数不变」更强的判据
	// (实测首版把「目录不存在」当 Fatal, 隔离根下必然踩中: 目录只在 submit 时创建)。
	count := func() int {
		es, rerr := os.ReadDir(taskDir)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				return 0
			}
			t.Fatalf("任务目录不可读: %v", rerr)
		}
		return len(es)
	}
	before := count()

	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		// 展开切片时, 其余实参必须全部对应固定形参 —— 故先拼 argv 再展开(否则 too many arguments)。
		cmd := f.newCmd(ctx, "python", append([]string{"-u", script}, args...)...)
		stdout := newLimitedWriter(gateCaptureLimit())
		stderr := newLimitedWriter(gateCaptureLimit())
		cmd.Stdout, cmd.Stderr = stdout, stderr
		rerr := runWithTimeout(ctx, cmd)
		out := stdout.String()
		if out == "" {
			out = stderr.String()
		}
		return out, rerr
	}

	// 向量①②: 子命令式调用必须被拒并指路
	out, rerr := run("status", "t20261002_000000_fake")
	if rerr == nil {
		t.Fatalf("子命令式调用未被拒绝(旧行为: 静默忽略 argv[2:], 把 argv[1] 当命令提交): %s", covTrunc(out, 300))
	}
	if !strings.Contains(out, "参数过多") || !strings.Contains(out, "action") {
		t.Errorf("拒绝文本未指路(缺「参数过多」或正确写法): %s", covTrunc(out, 300))
	}
	// 向量③: 零副作用
	if after := count(); after != before {
		t.Errorf("拒绝却已入队垃圾任务: 任务目录 %d -> %d 项", before, after)
	}
	// 反例: 单参数 JSON 调用照常工作
	out2, err2 := run(`{"action":"list","limit":1}`)
	if err2 != nil {
		t.Fatalf("单参数 JSON 调用被误拒: %v / %s", err2, covTrunc(out2, 300))
	}
	if !strings.Contains(out2, `"ok":true`) {
		t.Errorf("list 回执非 ok: %s", covTrunc(out2, 300))
	}
}

// TestTaskGate_RotateKeepsTotalUnderLimit 钉住 _rotate 的不变式:
// 任意提交序列后, .forge/tasks 内 t*.json / t*.log 份数必须 <= KEEP_TASKS。
//
// 缺陷 (20261003 实测): hygiene manifest 的 tasks 容器 patterns 口径是
// 「t*.json 文件数 <= 30」—— 数的是全部任务, 含运行中那份。而旧版 _rotate
// 只把「终态」裁到 30 份, 运行中不在管辖内 -> 稳态恒为 31 > 30, 判据永不满足,
// 任何经 task gate 跑的全量测试必红 (判据是对的, 机制是错的)。
//
// 判据脚本: defense_system/task_gate_rotate_test.py —— 自带隔离 (FORGE_ROOT 落
// 临时目录) 与反例自检 (把旧口径注入回去必须报红, 防判据退化成空壳)。
// 为何是 Go 测试而非 Python 自检: 接线是包级属性, 不进 go test 回归就没人跑。
func TestTaskGate_RotateKeepsTotalUnderLimit(t *testing.T) {
	t.Parallel()
	script := filepath.Join("defense_system", "task_gate_rotate_test.py")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("轮转哨兵脚本缺失 (fail-closed): %v", err)
	}
	cmd := exec.Command(guardGatePython(), script)
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("_rotate 不变式哨兵失败: %v\n%s", err, covTrunc(string(out), 1200))
	}
	if !strings.Contains(string(out), "ALL PASS") {
		t.Errorf("哨兵未报 ALL PASS (判据可能退化):\n%s", covTrunc(string(out), 1200))
	}
}
