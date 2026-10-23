package main

// recover_log_sentinel_test.go — 恢复类入口「动作留痕」接线判据 (T59-②, 20261017)
//
// 为什么需要 (T59 普查出数): 15 个入口里只有 upgrade.ps1 把失败输出落盘, 其余只打屏 ——
// 而最需要证据的恰是恢复类 (回滚/重启/恢复): 失败时窗口一关, 「哪一步失败」全部消失,
// 只剩一句『没成功』。与 T57 同型: 判据的输出没落盘 = 失败不可追溯。
// 恢复类还有第二重代价: 它本身就是安全网 —— 安全网失效而无人知晓, 等于没有安全网。
//
// 为什么动态扫描而非写死清单: 本仓库已多次复发「清单式判据漏登即盲区」—— T59-③ 的
// upgrade.ps1 就是活例 (含 22 处 go 命令却不在清单里, 长期无人测到)。故本判据按
// 【行为特征】识别恢复类入口, 新增脚本自动纳入:
//
//	特征 = 代码行(剥注释)里出现进程级操作 (forge_proc.ps1 / pkill)
//	       且不含 go test (含 go test 的是验证类入口, 其证据契约归 go_entry_sentinel_test.go)
//
// 三层判据:
//
//	R1 接线: 必须有 .forge/logs/recover.log 路径 + mkdir 目录兜底 + run_start 事件
//	R2 失败可回溯: 事件名里至少有一个失败类 (fail/reject/missing/dead/timeout/no_/abort/error)
//	   —— 只写 run_start 只证明「跑过」, 不证明「失败可回溯」
//	R3 格式契约: 落盘行 = "[ts] [tag] event" 且全 ASCII, 由 recoverLogLineRe 钉住,
//	   并对真实产物 .forge/logs/recover.log 逐行校验 (存在即验, 不存在则 Skip 说明)
//
// 验证等级 (T60-③, 20261017 —— 别把「判据绿」读成「已守住」):
//
//	Windows 侧 (.cmd/.bat): 真机跑过, recover.log 里有真实产物 —— R1/R2/R3 全验。
//	Linux 侧 (.sh): 本机无 Linux 执行环境, 只能做到
//	  ① 文本接线 (R1/R2, 本判据)
//	  ② bash -n 语法检查 (T59-② 实测 4/4 rc=0, 一次性, 非判据)
//	  ③ rlog 定义行的 printf 格式与 date 格式 —— 本文件 TestRecoverShEntriesLogFormat 静态钉住
//	  **未验**: Linux 真机全流程 (含 pkill / pgrep 分支) —— 这条风险敞口是显式的,
//	  不因本文件全绿而消失。补齐需要一台 Linux 环境真跑一次 restart.sh / rollback.sh。
//
// 判据自身也要有判据 (公理三): 反向向量 —— 注释里提到 recover.log 不算接线 / 验证类入口
// 不得被误收 / 只有成功事件必须报红 —— 都要跑, 否则「剥得过狠」与「判据无鉴别力」都发现不了。

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// recoverLogRel 是留痕落盘位置 (须与 manifest subdirs[logs].patterns[recover.log] 一致)。
const recoverLogRel = ".forge/logs/recover.log"

// recoverProcFrags 是「进程级操作」特征: Windows 侧走 forge_proc.ps1, shell 侧走 pkill。
var recoverProcFrags = []string{"forge_proc.ps1", "pkill"}

// recoverEventRe 抓事件名: cmd 侧 "call :rlog xxx" 与 sh 侧 "rlog xxx" 同形。
// 不匹配 ":rlog" 定义行与 "rlog() {" 函数头 (其后不是空白+词)。
var recoverEventRe = regexp.MustCompile(`\brlog\s+([A-Za-z0-9_]+)`)

// recoverLogLineRe 钉住落盘行格式 "[20261017_092158] [restore] run_start"。
// 全 ASCII 是硬要求: 恢复脚本在 chcp 65001/936 下都可能运行, 非 ASCII 行有解析错乱
// 前科 (quick_test.cmd 实测: chcp 之后的非 ASCII 行吞掉了后续命令参数)。
var recoverLogLineRe = regexp.MustCompile(`^\[\d{8}_\d{6}\] \[[A-Za-z0-9_.-]+\] [A-Za-z0-9_.-]+$`)

// recoverFailWords 是失败类事件的特征词。
var recoverFailWords = []string{"fail", "reject", "missing", "dead", "timeout", "no_", "abort", "error"}

// recoverIsRecoveryEntry 按行为特征判定脚本是否为恢复类入口 (不写死文件名清单)。
func recoverIsRecoveryEntry(src string) bool {
	if isGoTestEntry(src) {
		return false
	}
	code := strings.Join(scriptCodeLines(src), "\n")
	for _, f := range recoverProcFrags {
		if strings.Contains(code, f) {
			return true
		}
	}
	return false
}

// recoverEvents 返回脚本里全部 rlog 事件名 (剥注释后, 按出现顺序)。
func recoverEvents(src string) []string {
	var out []string
	for _, m := range recoverEventRe.FindAllStringSubmatch(strings.Join(scriptCodeLines(src), "\n"), -1) {
		out = append(out, m[1])
	}
	return out
}

// recoverHasFailureEvent 判定事件集合里是否存在失败类事件。
func recoverHasFailureEvent(events []string) bool {
	for _, e := range events {
		low := strings.ToLower(e)
		for _, w := range recoverFailWords {
			if strings.Contains(low, w) {
				return true
			}
		}
	}
	return false
}

// recoverEntryProblem 判定单个恢复类入口的接线问题 (空串 = 合规)。
func recoverEntryProblem(src string) string {
	code := strings.Join(scriptCodeLines(src), "\n")
	if !strings.Contains(code, "recover.log") {
		return "未接线: 代码里没有 recover.log 落盘路径 (只打屏 = 失败不可追溯)"
	}
	if !strings.Contains(code, "mkdir") {
		return "缺目录兜底: .forge/logs 不存在时追加重定向会静默失败"
	}
	events := recoverEvents(src)
	if len(events) == 0 {
		return "没有任何 rlog 事件调用 (留痕是愿望, 不是接线)"
	}
	hasStart := false
	for _, e := range events {
		if e == "run_start" {
			hasStart = true
		}
	}
	if !hasStart {
		return "缺 run_start 事件 (无法判断这次恢复到底跑没跑)"
	}
	if !recoverHasFailureEvent(events) {
		return "缺失败类事件: 只有成功路径留痕 = 失败仍然不可回溯"
	}
	return ""
}

// recoverEntryFiles 读根目录全部脚本入口 (扩展名复用 goEntryExtOK, 避免两处口径漂移)。
func recoverEntryFiles(t *testing.T) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取仓库根失败 (fail-closed, 不放行): %v", err)
	}
	out := map[string]string{}
	for _, e := range ents {
		if e.IsDir() || !goEntryExtOK(e.Name()) {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Errorf("读取入口脚本 %s 失败: %v", e.Name(), err)
			continue
		}
		out[e.Name()] = string(b) // GBK 脚本按字节读入: 判据只看 ASCII 特征, 不依赖中文解码
	}
	return out
}

// TestRecoverEntrypointsWireLog 哨兵: 每个恢复类入口必须接上留痕 (R1 + R2)。
func TestRecoverEntrypointsWireLog(t *testing.T) {
	files := recoverEntryFiles(t)
	var found []string
	for name, src := range files {
		if !recoverIsRecoveryEntry(src) {
			continue
		}
		found = append(found, name)
		if prob := recoverEntryProblem(src); prob != "" {
			t.Errorf("%s: %s\n  恢复类入口失败时最需要证据: 接线 = recover.log + mkdir + run_start + 失败类事件", name, prob)
		}
	}
	sort.Strings(found)
	if len(found) == 0 {
		t.Fatal("扫描未发现任何恢复类入口 —— 判据失效 (特征词与脚本漂移?): 空集恒绿比没有判据更危险")
	}
	if len(found) < 5 {
		t.Fatalf("只扫出 %d 个恢复类入口: %v —— 少于已知规模, 疑似特征词失效", len(found), found)
	}
	t.Logf("恢复类入口 %d 个: %s", len(found), strings.Join(found, ", "))
}

// TestRecoverEntrypointProblemSelfCheck 元判据: 接线判据自身必须有鉴别力 (双向)。
func TestRecoverEntrypointProblemSelfCheck(t *testing.T) {
	// okBody 必须同时满足两个独立判定: ①是恢复类入口 (含进程级操作特征)
	// ②接线合规 (recover.log + mkdir + run_start + 失败类事件) —— 少任一半, 自检就是自欺。
	const okBody = "powershell -File \"%~dp0defense_system\\forge_proc.ps1\" kill -AppDir \"%~dp0.\"\n" +
		"set \"RLOG=%~dp0.forge\\logs\\recover.log\"\n" +
		"if not exist \"%~dp0.forge\\logs\" mkdir \"%~dp0.forge\\logs\" 2>nul\n" +
		"call :rlog run_start\ncall :rlog kill_failed\n"
	cases := []struct {
		name    string
		src     string
		wantRed bool
	}{
		{"ok.cmd", okBody, false},
		{"nolog.cmd", "call :rlog run_start\ncall :rlog kill_failed\n", true},
		{"nomkdir.cmd", "set RLOG=recover.log\ncall :rlog run_start\ncall :rlog kill_failed\n", true},
		{"nostart.cmd", "set RLOG=recover.log\nmkdir x\ncall :rlog kill_failed\n", true},
		{"onlysuccess.cmd", "set RLOG=recover.log\nmkdir x\ncall :rlog run_start\ncall :rlog restart_ok\n", true},
		{"noevent.cmd", "set RLOG=recover.log\nmkdir x\n", true},
		{"commented.cmd", "rem set RLOG=recover.log\nrem mkdir x\ncall :rlog run_start\ncall :rlog kill_failed\n", true},
		{"sh-ok.sh", "RLOG=.forge/logs/recover.log\nmkdir -p .forge/logs\nrlog run_start\nrlog no_backup\n", false},
		{"sh-noevent.sh", "RLOG=.forge/logs/recover.log\nmkdir -p .forge/logs\n", true},
	}
	for _, c := range cases {
		prob := recoverEntryProblem(c.src)
		if c.wantRed && prob == "" {
			t.Errorf("%s: 应报红却判绿 —— 判据无鉴别力", c.name)
		}
		if !c.wantRed && prob != "" {
			t.Errorf("%s: 应判绿却报红: %s", c.name, prob)
		}
	}
	// 入口识别也必须双向: 恢复类认得出, 验证类与普通脚本不得被收。
	if !recoverIsRecoveryEntry(okBody) {
		t.Error("恢复类入口未被识别 (特征词失效)")
	}
	if recoverIsRecoveryEntry(okBody + "go test -c -o b .\nb -test.timeout=600s\n") {
		t.Error("验证类入口 (含 go test) 被误收进恢复类")
	}
	if recoverIsRecoveryEntry("echo hello\n") {
		t.Error("普通脚本被误收进恢复类")
	}
	if recoverIsRecoveryEntry("rem forge_proc.ps1\nrem pkill\n") {
		t.Error("注释里的进程操作被当成真接线 (剥注释失效)")
	}
}

// TestRecoverLogLineContract 格式契约: 落盘行可被机器解析, 且真实产物逐行合规 (R3)。
func TestRecoverLogLineContract(t *testing.T) {
	good := []string{
		"[20261017_092158] [restore] run_start",
		"[20261017_092158] [restart-sh] rollback_failed",
		"[20261017_092158] [rollback] mem_reject",
	}
	for _, g := range good {
		if !recoverLogLineRe.MatchString(g) {
			t.Errorf("合规行被判非法: %q", g)
		}
	}
	bad := []string{
		"", "run_start", "[restore] run_start",
		"[20261017] [restore] run_start",
		"[20261017_092158] [restore] run start",
		"[20261017_092158] [restore] 启动失败",
		"[20261017_092158] [] run_start",
		"[bad] [restore] run_start",
	}
	for _, b := range bad {
		if recoverLogLineRe.MatchString(b) {
			t.Errorf("非法行被判合规 (契约形同虚设): %q", b)
		}
	}

	// 活证据: 真实产物存在即逐行校验 (不存在说明恢复类入口尚未跑过 —— Skip 并说明, 不静默)。
	b, err := os.ReadFile(recoverLogRel)
	if err != nil {
		t.Skipf("尚无 %s (恢复类入口还没跑过): 接线判据仍有效, 格式判据待首次运行后生效", recoverLogRel)
	}
	n := 0
	for i, ln := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		n++
		if !recoverLogLineRe.MatchString(ln) {
			t.Errorf("%s 第 %d 行不符合契约 (解析会失败, 事后回溯断在这里): %q", recoverLogRel, i+1, ln)
		}
	}
	if n == 0 {
		t.Errorf("%s 存在但 0 行有效记录 —— 写了空文件等于没写", recoverLogRel)
	}
	t.Logf("真实产物 %s: %d 行全部合规", recoverLogRel, n)
}

// recoverShRlogRe 抓 .sh 侧 rlog 定义的 printf 格式串。
var recoverShRlogRe = regexp.MustCompile(`rlog\(\)\s*\{\s*printf\s+'([^']*)'`)

// recoverShDateFormatRe 抓 .sh 侧 rlog 的时间戳来源。
var recoverShDateFormatRe = regexp.MustCompile(`date \+([A-Za-z0-9_%:-]+)`)

// TestRecoverShEntriesLogFormat 哨兵: .sh 侧留痕的格式契约 (T60-③)。
//
// 为什么需要: .sh 恢复入口在 Windows 上跑不起来, 上面那条 R3「逐行校验真实产物」
// 对本机永远只覆盖 .cmd/.bat 的产物。若不把 .sh 侧的格式单独钉住, 「判据全绿」
// 会被读成「.sh 也守住了」—— 实际只验了文本接线。本判据把可在 Windows 上静态判定的
// 部分 (printf 格式 + date 格式) 上收给死程序, 剩下的真机分支显式记为未验。
func TestRecoverShEntriesLogFormat(t *testing.T) {
	files := recoverEntryFiles(t)
	var sh []string
	for name, src := range files {
		if !strings.HasSuffix(name, ".sh") || !recoverIsRecoveryEntry(src) {
			continue
		}
		sh = append(sh, name)
		m := recoverShRlogRe.FindStringSubmatch(src)
		if m == nil {
			t.Errorf("%s: 找不到 rlog 的 printf 定义 —— .sh 侧留痕格式无契约可验", name)
			continue
		}
		if m[1] != "[%s] [%s] %s\\n" {
			t.Errorf("%s: printf 格式 %q != \"[%%s] [%%s] %%s\\n\" —— 与 recoverLogLineRe 契约不一致, 解析会失败", name, m[1])
		}
		d := recoverShDateFormatRe.FindStringSubmatch(src)
		if d == nil {
			t.Errorf("%s: 找不到 date +格式 —— 时间戳列无法对齐契约", name)
			continue
		}
		if d[1] != "%Y%m%d_%H%M%S" {
			t.Errorf("%s: date +%s != date +%%Y%%m%%d_%%H%%M%%S —— 时间戳列不合 recoverLogLineRe 契约", name, d[1])
		}
	}
	sort.Strings(sh)
	if len(sh) == 0 {
		t.Fatal("未发现任何 .sh 恢复入口 —— 判据失效 (特征词与脚本漂移?)")
	}
	t.Logf(".sh 恢复入口 %d 个: %s (逐项校验结果见上方 error —— 无 error 才算合规)", len(sh), strings.Join(sh, ", "))
	t.Logf("验证等级: 文本接线+格式契约=已验(本判据) | bash -n 语法=一次性实测 | Linux 真机全流程(含 pkill)=未验")
}
