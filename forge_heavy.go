package main

// forge_heavy.go — 重模式拒绝权 (P0-3, 20261002)
//
// 动机: agent_memory.go 早已写「长任务拆成 ≤30 秒」, 实测仍 214 次超时
// (python 失败 244 条里 214 条是超时, 87.7%), 白耗 6486s —— 建议类约束对 LLM 无效。
// 处置(同 sh gate 退役做法): 把建议升级为 gate 层前置拒绝权, 命中即当场拒绝并附
// 替代写法。拒绝可判定且当场截断, 猜测要烧一整轮重试。
//
// 为什么独立成文件: forge.go 是核心编排文件, 其顶层声明数受分形守卫 F1 约束。
// 判据/文本/开关三者同源同文件, 避免"改判据漏改文本"的漂移。
//
// 判据 (必须可判定, 不靠猜):
//
//	H1 os.walk 根目录        —— 扫 205MB knowledge 库必超时
//	H2 同步调用 go test/build —— 全量实测 113.9s, 物理塞不进 30s 预算
//	H3 time.sleep(N≥20)      —— 等待型重活
//	H4 pip install           —— 下载 + 编译, 时长不可控
//
// 豁免(否则自相矛盾): lang=task 本身即通道; 已用 Popen 的后台写法不在判据内;
// FORGE_HEAVY_GUARD=0 关闭(调试用, 关闭仍写审计)。
//
// 哨兵: forge_heavy_test.go 正例/反例双向量表 + Build 层接线断言。
import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// heavyRule 一条重活判据。Name 同时进拒绝文本与审计, 便于按判据度量命中率。
type heavyRule struct {
	Name string
	Hit  func(code string) (string, bool)
}

// heavyTaskText 拒绝回告 —— 拒绝必须自带出路。
// (sh_retired.go 同款教训: 只拒绝不给替代 = 断路, 实测模型会瞎试重写。)
const heavyTaskText = `这是重活: 耗时由外部决定, 物理上塞不进 30s 前台预算。
请改走长任务通道 lang="task" —— 提交秒回拿 task_id, 再轮询:

  {"action":"submit","cmd":"go test -count=1 .","cwd":"D:\\forge"}
      -> {"ok":true,"result":{"task_id":"t20261002_...", ...}}
  {"action":"status","id":"t20261002_..."}      # 轮询到 state=done/failed
  {"action":"wait","id":"...","timeout":20}     # 或等一次(单次上限 20s)
  {"action":"tail","id":"...","lines":60}       # 只看日志尾部

要点:
  · 提交与执行分离: 后台进程独立于 gate 存活, 不受 30s 预算约束
  · 不要在 python gate 里手搓 subprocess.run(go test) —— 必然超时且拿不到输出
  · 日志落 .forge/tasks/<id>.log, 终态写 .forge/tasks/<id>.json
  · 扫大目录同理: 提交到 task gate, 不要在前台 os.walk`

// heavyRules 判据表 (单一数据源: 新增判据只加一行, 文本与哨兵自动跟随)。
var heavyRules = []heavyRule{
	{"os.walk 根目录", hitWalkRoot},
	{"同步调用 go test/build", hitSyncGoBuild},
	{"time.sleep 过长", hitLongSleep},
	{"pip install", hitPipInstall},
}

// 判据正则集中一处 —— 与 heavyRules 同层同文件, 禁止散落到调用点。
var (
	// walkRootRE 匹配 os.walk 作用于根目录的字面写法: "." / ".." / "./" / "" / os.getcwd()
	walkRootRE = regexp.MustCompile(`os\.walk\s*\(\s*(?:["']\.{0,2}/?["']|os\.getcwd\s*\(\s*\))`)
	syncCallRE = regexp.MustCompile(`(?:subprocess\.(?:run|call|check_call|check_output|getoutput)|os\.system|os\.popen)\s*\(`)
	// goBuildRE 覆盖两种写法: shell 字符串("go test .") 与参数列表(["go", "test"])。
	// 列表形式漏判过一次(实测哨兵报红): "go", 后面跟的是引号逗号, 不是空白。
	goBuildRE    = regexp.MustCompile(`\bgo\s+(?:test|build|vet)\b|["']go["']\s*,\s*["'](?:test|build|vet)["']`)
	sleepRE      = regexp.MustCompile(`time\.sleep\s*\(\s*(\d+(?:\.\d+)?)\s*\)`)
	pipInstallRE = regexp.MustCompile(`(?i)\bpip3?\b[^\n]{0,16}\binstall\b|python\s+-m\s+pip\s+install`)
)

// hitWalkRoot H1: 扫根目录 = 扫全仓(含 205MB knowledge 库)。
func hitWalkRoot(code string) (string, bool) {
	if m := walkRootRE.FindString(code); m != "" {
		return m, true
	}
	return "", false
}

// hitSyncGoBuild H2: 同步进程调用 go test/build/vet。
//
// 只在「进程调用点之后的 200 字符窗口」内找 go 子命令 —— 整篇共现会误伤
// (文件里既有 subprocess.run(别的命令) 又提到 go test 属正常写法)。
// Popen 不在 syncCallRE 内: 那已是后台写法, 正是要引导的方向。
func hitSyncGoBuild(code string) (string, bool) {
	for _, loc := range syncCallRE.FindAllStringIndex(code, -1) {
		end := loc[1] + 200
		if end > len(code) {
			end = len(code)
		}
		if m := goBuildRE.FindString(code[loc[1]:end]); m != "" {
			return strings.TrimSpace(m), true
		}
	}
	return "", false
}

// hitLongSleep H3: time.sleep(N) 且 N ≥ 20 —— 等待型重活(纯白耗预算)。
func hitLongSleep(code string) (string, bool) {
	for _, m := range sleepRE.FindAllStringSubmatch(code, -1) {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil && v >= 20 {
			return m[0], true
		}
	}
	return "", false
}

// hitPipInstall H4: 下载 + 编译, 时长不可控(且会污染环境)。
func hitPipInstall(code string) (string, bool) {
	if m := pipInstallRE.FindString(code); m != "" {
		return strings.TrimSpace(m), true
	}
	return "", false
}

// checkHeavyTask 判定代码是否属重活。返回 (判据名, 证据, 命中)。
func checkHeavyTask(code string) (name, evidence string, hit bool) {
	for _, r := range heavyRules {
		if ev, ok := r.Hit(code); ok {
			return r.Name, ev, true
		}
	}
	return "", "", false
}

// heavyGuardEnabled 拒绝权开关。默认开; FORGE_HEAVY_GUARD=0/off/false 关闭。
// 关闭不等于静默: 关闭只跳过拦截, 审计与留痕路径不变(见 Build 的 rejected 分支)。
func heavyGuardEnabled() bool {
	v := strings.TrimSpace(os.Getenv("FORGE_HEAVY_GUARD"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "off", "false", "no":
		return false
	}
	return true
}
