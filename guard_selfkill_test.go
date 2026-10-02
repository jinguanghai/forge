package main

// guard_selfkill_test.go — 20261002「炉体自杀」事故的回归钉。
//
// 事故: 15:17:02 模型写的清理代码含 os.system('taskkill /F /IM forge.exe')。
// 检测层**正确命中**「自杀」并打印了提示, 但委托直通档只拿到档位、拿不到类别,
// 无条件放行 → 炉体被自己杀死 (events.jsonl 在 approved 那一行硬截断,
// memory.json 落后 78 分钟, 无任何收尾)。
//
// 本文件钉四件事:
//  ① 19 条历史「自杀」告警逐条分类正确 —— 只有 1 条真自杀, 其余不得再冒用该名;
//  ② 委托档不得代批不可逆自毁类与对象不明类;
//  ③ 端到端: 直通档开着, 自毁类也必须被拒且不执行;
//  ④ 接线: 拒绝分支必须真的在 confirmDangerous 体内 (写了没接 = 等于没写)。

import (
	"os"
	"strings"
	"testing"
)

// TestSelfKillSamples_History19 用逐条核对过的历史告警做回归。
//
// want 语义: "" = 不应判危险; 其它 = 期望类别。
// 关键判据: 真自杀必须命中「自杀」; 只读查询 / 启动自身 / 读文件属性 一律不得命中。
// 样本取自 .forge/approvals/index.jsonl 里 kind=自杀 的全部 19 条实证代码。
func TestSelfKillSamples_History19(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		// ── 真·自杀: 唯一真正杀死炉体的那条 (788661b57d0c) ──
		{"实测·致命 taskkill /F /IM forge", `os.system('taskkill /F /IM forge.exe 2>NUL')`, "自杀"},
		{"/IM 与 /F 顺序调换", `taskkill /IM forge.exe /F`, "自杀"},
		{"按名停进程(PowerShell)", `Stop-Process -Name forge -Force`, "自杀"},
		{"杀自身 pid", `syscall.Kill(os.Getpid(), 9)`, "自杀"},

		// ── 进程终止(对象不明): 保留审批, 但不得冒充「自杀」 ──
		{"实测·杀测试子进程(三元分支)", `subprocess.run(["taskkill","/F","/T","/PID",str(p.pid)] if ISNT else ["kill","-9",str(p.pid)], capture_output=True)`, "进程终止"},
		{"实测·taskkill /PID 硬编码", `subprocess.run(["taskkill","/PID","17480","/F","/T"], capture_output=True)`, "进程终止"},
		{"实测·taskkill 字符串形式", `subprocess.run("taskkill /F /T /PID 13808", shell=True, capture_output=True, text=True)`, "进程终止"},

		// ── 历史误报: 这些当年全被记成「自杀」, 是告警贬值的来源 ──
		{"实测·tasklist 只读查询", `subprocess.run(["tasklist","/FI","IMAGENAME eq forge.exe","/V","/FO","LIST"], capture_output=True, text=True, encoding="gbk")`, ""},
		{"实测·tasklist 简版查询", `r2 = subprocess.run(["tasklist","/FI","IMAGENAME eq forge.exe"], capture_output=True)`, ""},
		{"杀别的服务不算自杀", `os.system('taskkill /F /IM gateway.exe 2>NUL')`, ""},
		{"实测·读 forge.exe 字节", `b = open("forge.exe","rb").read()`, ""},
		{"实测·stat forge.exe", `st = os.stat("forge.exe")`, ""},
		{"实测·gateway 首参不是 forge", `subprocess.run([r"D:\forge\web\gateway\gateway.exe", '-listen', '127.0.0.1:18443', '-forge', r'D:\forge\forge.exe'], capture_output=True)`, ""},
		{"实测·str(路径) 形态(启动新进程, 无害)", `subprocess.run([str(ROOT/"forge.exe"), "--version"], capture_output=True)`, ""},

		// ── 包一层(引导项): 只有程序位是 forge.exe 才命中 ──
		{"实测·forge.exe --version 冒烟", `subprocess.run(["forge.exe","--version"], capture_output=True, text=True, encoding="utf-8")`, "包一层"},
		{"实测·原始路径形态", `subprocess.run([r"D:\forge\forge.exe", "/self", "deploy"])`, "包一层"},
		{"普通 subprocess 不误伤", `subprocess.run(["ls","-la"], capture_output=True)`, ""},

		// ── 正常开发不得被牵连 ──
		{"普通代码", `print(sum(range(10)))`, ""},
		{"删单文件(非受保护目标)", `os.Remove("old.txt")`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, hit, danger := checkDangerousCode(c.code)
			if c.want == "" {
				if danger {
					t.Fatalf("不应判危险: kind=%s hit=%s", kind, hit)
				}
				return
			}
			if !danger {
				t.Fatalf("应判危险 [%s], 未命中", c.want)
			}
			if kind != c.want {
				t.Fatalf("类别 got %q want %q (hit=%q)", kind, c.want, hit)
			}
		})
	}
}

// TestDelegate_MayNotAutoApproveIrreversible 分级判定: 该拒的拒、该批的照批。
//
// 反向用例同样重要: 若改装成"委托整体失效", 主人会退回 16.4 小时空等 ——
// 那不是修复, 是把成本转嫁回主人身上。
func TestDelegate_MayNotAutoApproveIrreversible(t *testing.T) {
	for _, kind := range []string{"自杀", "磁盘", "炸弹", "进程终止"} {
		if delegateMayAutoApprove(kind) {
			t.Errorf("类别 %q 不得委托代批", kind)
		}
		if allowed, decided := confirmByDelegate("all", kind, "fp"); decided || allowed {
			t.Errorf("直通档对类别 %q 不得代批 (got allowed=%v decided=%v)", kind, allowed, decided)
		}
	}
	for _, kind := range []string{"删除", "覆盖", "保护目标", "强推", "自改", "测试"} {
		if !delegateMayAutoApprove(kind) {
			t.Errorf("类别 %q 应可委托代批", kind)
		}
		if allowed, decided := confirmByDelegate("all", kind, "fp"); !decided || !allowed {
			t.Errorf("直通档对类别 %q 应代批 (got allowed=%v decided=%v)", kind, allowed, decided)
		}
	}
	if !isIrreversibleKind("自杀") {
		t.Error("自杀 必须属不可逆自毁类")
	}
	if isIrreversibleKind("删除") || isIrreversibleKind("进程终止") {
		t.Error("不可逆自毁类只含自毁/毁盘/炸弹, 不得外扩")
	}
}

// TestConfirmDangerous_RefusesSelfKill 端到端: 复现事故当天的配置 (委托=all),
// 自毁类必须被拒 —— 这条测试若变红, 就说明事故可以重演。
func TestConfirmDangerous_RefusesSelfKill(t *testing.T) {
	old := os.Getenv("FORGE_DELEGATE")
	defer os.Setenv("FORGE_DELEGATE", old)
	os.Setenv("FORGE_DELEGATE", "all")

	f := &Forge{workDir: t.TempDir()} // 取证写到临时目录, 不碰真实 .forge
	if got := f.confirmDangerous(`os.system('taskkill /F /IM forge.exe 2>NUL')`,
		"自杀", "taskkill /F /IM forge"); got {
		t.Fatal("直通档下不可逆自毁类被放行 —— 20261002 事故会重演")
	}
	// 反向: 可代批类别在直通档下仍必须放行(证明拒的是类别, 不是把委托整体关掉)。
	if got := f.confirmDangerous(`os.Remove("tmp-out.txt")`, "删除", "os.Remove("); !got {
		t.Fatal("直通档对普通删除类应照常代批")
	}
}

// TestWiring_IrreversibleRefusalIsWired 接线钉: 拒绝分支必须落在 confirmDangerous 体内。
// 先例(本项目): 取证写了没接线 = 等于没写。
func TestWiring_IrreversibleRefusalIsWired(t *testing.T) {
	src, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	cd := strings.Index(s, "func (f *Forge) confirmDangerous(")
	if cd < 0 {
		t.Fatal("未找到 confirmDangerous —— 审批门被改名或删除?")
	}
	call := strings.Index(s, "isIrreversibleKind(kind)")
	if call < 0 {
		t.Fatal("forge.go 未调用 isIrreversibleKind(kind) —— 拒绝分支未接线")
	}
	next := strings.Index(s[cd+1:], "\nfunc ")
	if next > 0 && call > cd+1+next {
		t.Fatal("isIrreversibleKind 接到了 confirmDangerous 之外")
	}
}
