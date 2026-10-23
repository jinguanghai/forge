package main

// version_bump_log_sentinel_test.go — 发版动作臂「失败证据落盘」的接线判据 (T60-②, 20261017)
//
// 为什么需要: 发版 (发版.cmd -> defense_system/version_bump.py) 失败时只把
// go build / go test / git commit 的输出打到控制台 —— 窗口一关,「哪一步失败、
// 哪条用例红」全部消失, 与 T57 (upgrade.ps1) 完全同型。T59 普查把 发版.cmd 列成
// 「恢复类」, 而它的证据其实产生在 python 侧 —— 判据口径与语义打架 (T60-②)。
// 本判据的处置选择是【证据下沉】: 落盘落在真正产生证据的地方 (version_bump.py),
// 转发入口 (发版.cmd / vps_sync.cmd) 归「运维类」, 不再要求它自己写日志。
//
// 三层判据:
//
//	V1 接线: .forge/logs 路径 + 带时间戳文件名 + 逐行 flush + 轮转 + 失败全文落盘
//	   —— 光「打开文件写」不够: 整块缓冲在进程被中断时照样落空文件 (T57 教训);
//	   光「落盘」也不够: 只留尾部 800 字符会丢掉真正指出哪条用例红的 '--- FAIL' 行,
//	   故必须有 log_full (完整输出) 而非只 log (摘要)。
//	V2 同数: 脚本 LOG_KEEP == manifest subdirs[logs].patterns[version_bump_*.log].keep
//	   —— 单一数字两个落点, 改一处忘另一处 = .forge 哨兵常驻报红 (狼来了)。
//	V3 元判据: 判定函数对「摘掉某条臂」的样例必须报红, 且注释里提到不算接线。
//
// 判据自身也要有判据 (公理三): 见 TestVersionBumpLogProblemSelfCheck。

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	// versionBumpSrc 是判据的对照对象 (缺失即 fail-closed)。
	versionBumpSrc = "defense_system/version_bump.py"
	// versionBumpLogGlob 必须与 manifest subdirs[logs].patterns[].glob 一致。
	versionBumpLogGlob = "version_bump_*.log"
)

// versionBumpCodeLines 剥掉 Python 整行注释 (只剥 # 开头的行 —— 行内 # 可能是
// 字符串里的井号, 判据宁可多算也不少算)。
func versionBumpCodeLines(src string) []string {
	var out []string
	for _, ln := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// versionBumpFuncBody 取 Python 函数体 (从 "def name(" 到下一个顶格声明)。
//
// 为什么按函数取而不是全文 Contains: 本脚本里 _LOG_F.flush() 出现两处 (log / log_full),
// 全文匹配会在「只摘掉其中一处」时仍判绿 —— 实测变异 version_bump_log_unwired
// 把 log 的 flush 摘掉, 判据照绿 (静默放行)。判据必须按函数定位。
func versionBumpFuncBody(src, name string) string {
	lines := versionBumpCodeLines(src)
	head := "def " + name + "("
	var out []string
	inside := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, head) {
			inside = true
			out = append(out, ln)
			continue
		}
		if !inside {
			continue
		}
		if ln != "" && !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") {
			break // 顶格非空行 = 函数结束
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// versionBumpLogProblem 判定发版动作臂的落盘接线 (空串 = 合规)。
func versionBumpLogProblem(src string) string {
	code := strings.Join(versionBumpCodeLines(src), "\n")
	must := []struct{ frag, why string }{
		{`os.path.join(FORGE, ".forge", "logs")`, "缺日志目录: 失败证据无处可落 (只打屏 = 窗口一关就丢)"},
		{"LOG_TAG", "缺日志标签: 无法按前缀轮转与检索"},

		{"files[:-LOG_KEEP]", "缺轮转: 日志无上限增长"},
		{"def log_full(label, text):", "缺 log_full 定义: 只有调用没有实现 = 未接线"},
		{"log_full(", "缺完整输出落盘: 只留尾部摘要会丢掉 '--- FAIL' 行"},
		{"log_path()", "缺失败时的日志路径输出: 有日志但没人知道在哪 = 等于没有"},
	}
	for _, c := range must {
		if !strings.Contains(code, c.frag) {
			return "未接线 " + c.frag + " —— " + c.why
		}
	}
	// 逐行 flush 必须逐个函数验 (全文 Contains 抓不到「只摘一处」的变异)。
	for _, fn := range []string{"log", "log_full"} {
		body := versionBumpFuncBody(code, fn)
		if body == "" {
			return "缺 " + fn + " 函数定义 (只有调用没有实现 = 未接线)"
		}
		if !strings.Contains(body, "_LOG_F.flush()") {
			return fn + " 缺逐行 flush: 输出攒在缓冲里, 进程被中断时落空文件 (T57 实测)"
		}
	}
	// 失败分支必须真的调用 log_full (定义了不用 = 愿望不是接线)。
	if !regexp.MustCompile(`log_full\('go build'`).MatchString(code) {
		return "go build 失败分支未调用 log_full (编译错误全文仍会丢)"
	}
	if !regexp.MustCompile(`log_full\('go test'`).MatchString(code) {
		return "go test 失败分支未调用 log_full (FAIL 行仍会丢)"
	}
	return ""
}

// versionBumpSource 读脚本, 读不到即 fail-closed。
func versionBumpSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(versionBumpSrc)
	if err != nil {
		t.Fatalf("读取 %s 失败, 落盘判据失去对照对象 (fail-closed): %v", versionBumpSrc, err)
	}
	return string(b)
}

// TestVersionBumpLogsFailures 哨兵: 发版动作臂必须把失败证据落盘 (V1)。
func TestVersionBumpLogsFailures(t *testing.T) {
	src := versionBumpSource(t)
	if prob := versionBumpLogProblem(src); prob != "" {
		t.Errorf("%s: %s\n  发版失败最需要证据: 接线 = .forge/logs + 时间戳 + flush + 轮转 + 全文 + 路径输出", versionBumpSrc, prob)
	}
	// dry-run 契约: 演练不得写任何文件 (日志也算文件)。
	if !regexp.MustCompile(`if not dry:\s*\n\s*open_log\(\)`).MatchString(src) {
		t.Error("open_log 未受 dry-run 保护 —— --dry-run 声明『不写任何文件』却会写日志")
	}
	t.Logf("%s 落盘接线完整", versionBumpSrc)
}

// TestVersionBumpLogKeepInSync 哨兵: 脚本轮转份数 == 清单 keep (V2)。
func TestVersionBumpLogKeepInSync(t *testing.T) {
	src := versionBumpSource(t)
	mt := regexp.MustCompile(`(?m)^LOG_KEEP\s*=\s*(\d+)\s*$`).FindStringSubmatch(src)
	if mt == nil {
		t.Fatalf("%s 找不到顶层 'LOG_KEEP = N' —— 轮转被摘掉, 日志将无上限增长", versionBumpSrc)
	}
	n, err := strconv.Atoi(mt[1])
	if err != nil {
		t.Fatalf("解析 LOG_KEEP 失败: %v", err)
	}
	m := loadForgeManifest(t)
	got, found := -1, false
	for _, sd := range m.Subdirs {
		if sd.Path != "logs" {
			continue
		}
		for _, p := range sd.Patterns {
			if p.Glob == versionBumpLogGlob {
				got, found = p.Keep, true
			}
		}
	}
	if !found {
		t.Fatalf("manifest 无 subdirs[logs].patterns[%s] —— 日志文件会被 .forge 哨兵判为未登记项", versionBumpLogGlob)
	}
	if got != n {
		t.Errorf("清单 keep %d != 脚本 LOG_KEEP %d —— 脚本轮转与 .forge 上限漂移", got, n)
	}
}

// TestVersionBumpLogProblemSelfCheck 元判据: 判定函数必须有鉴别力 (V3, 双向)。
func TestVersionBumpLogProblemSelfCheck(t *testing.T) {
	const okBody = `LOGS = os.path.join(FORGE, ".forge", "logs")
LOG_KEEP = 10
LOG_TAG = "version_bump"
def open_log(tag=LOG_TAG):
    path = os.path.join(LOGS, "%s_%s.log" % (tag, ts))
    _rotate_logs(tag)
def _rotate_logs(tag):
    for old in files[:-LOG_KEEP]:
        os.remove(old)
def log(msg=""):
    _LOG_F.write(str(msg))
    _LOG_F.flush()
def log_full(label, text):
    _LOG_F.write(text)
    _LOG_F.flush()
def log_path():
    return _LOG_PATH
def bump(new, dry=False):
    if not dry:
        open_log()
    log_full('go build', out)
    log_full('go test', out)
`
	if prob := versionBumpLogProblem(okBody); prob != "" {
		t.Fatalf("合规样例被判违规 (判据无鉴别力): %s", prob)
	}
	cases := []struct{ name, src string }{
		// 只摘 log 的 flush (log_full 仍在) —— 实测变异 version_bump_log_unwired 的形态:
		// 全文 Contains 会误判绿, 必须按函数定位才抓得到。
		{"log-no-flush", strings.Replace(okBody, "    _LOG_F.write(str(msg))\n    _LOG_F.flush()\n", "    _LOG_F.write(str(msg))\n", 1)},
		{"logfull-no-flush", strings.Replace(okBody, "    _LOG_F.write(text)\n    _LOG_F.flush()\n", "    _LOG_F.write(text)\n", 1)},
		{"no-rotate", strings.Replace(okBody, "files[:-LOG_KEEP]", "files", 1)},
		{"no-logfull", strings.Replace(okBody, "def log_full(label, text):", "def _unused(label, text):", 1)},
		{"no-path", strings.Replace(okBody, "def log_path():", "def _path():", 1)},
		{"no-dir", strings.Replace(okBody, `os.path.join(FORGE, ".forge", "logs")`, `os.path.join(FORGE, "tmp")`, 1)},
		{"defined-not-called", strings.Replace(okBody, "    log_full('go test', out)\n", "", 1)},
		{"commented-out", "# " + strings.ReplaceAll(okBody, "\n", "\n# ")},
		{"empty", ""},
	}
	for _, c := range cases {
		if prob := versionBumpLogProblem(c.src); prob == "" {
			t.Errorf("%s: 应报红却判绿 —— 判据对『摘掉这条臂』无鉴别力", c.name)
		}
	}
}
