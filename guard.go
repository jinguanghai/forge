// guard.go: 三层防护守卫: 记录 / 阻断 / 守门人报警 的判定逻辑

package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ─── 输入护栏 (三级分级 + L1-L4 反制对齐) ───
// 级别:
//   medium   → L1 记录 + L2 阻断 (注入/规则覆盖企图)
//   high     → L1 记录 + L2 阻断 (明显越权)
//   critical → L1 记录 + L2 阻断 + LLM 独立二级裁决 (破坏性/攻击性, 裁决通过才放行)
//
// 反制体系对齐: L1=guard_log.jsonl 全量记录 / L2=确定性词库阻断 / L3=critical 拒绝时守门人报警 / L4=日志即取证档案
// 设计原则: 宁缺毋滥——只留高置信规则, 避免误伤主人正常指令。
// 主人如确属正当需求(如防御演练), 可重新表述后继续。

type guardRule struct {
	kind  string // 原因类别
	level string // medium | high | critical
	words []string
}

// ─── 危险代码模式检测 (执行管线审批) ───────────────
// 与输入护栏(checkInputGuard, 管"主人输入")不同, 这里管"模型要执行的代码":
// 模型写的代码可能包含删除/覆盖/自杀等危险操作, 纯提示铁律可被绕过,
// 代码层强制: 命中危险模式 → 终端交互 y/N 批准后才执行。
// 设计原则沿用"宁缺毋滥": 只留高置信模式, 避免误伤正常代码。

// dangerousPattern 一条危险代码模式
type dangerousPattern struct {
	kind string         // 原因类别: 删除 | 覆盖 | 磁盘 | 自杀 | 强推 | 炸弹
	re   *regexp.Regexp // 大小写不敏感匹配
}

var guardRules = []guardRule{
	// 注入: 试图覆盖系统规则 / 提取 system prompt (medium)
	{"注入", "medium", []string{"忽略之前", "忽略所有", "忘记你的规则", "忘记所有指令", "ignore previous", "ignore all", "disregard"}},
	{"注入", "medium", []string{"泄露你的系统提示", "输出你的系统提示", "重复你的system", "reveal your system", "show your system prompt"}},
	// 越权: 破坏性系统操作 (critical → LLM 独立复核)
	{"越权", "critical", []string{"格式化磁盘", "格式化c盘", "format c:", "删除所有文件", "删库", "drop database", "rm -rf /", "del /s /q", "清空所有数据"}},
	// 攻击性/非法操作 (critical → LLM 独立复核) (红线: 永不反向攻击/永不探测未授权目标)
	{"越权", "critical", []string{"攻击服务器", "反向攻击", "破解密码", "探测未授权", "扫描未授权"}},
}

// 求知前缀: 纯知识咨询(什么是/科普/解释等)直接放行, 防误伤学习类提问
var studyPrefixes = []string{"什么是", "科普", "介绍一下", "介绍", "讲讲", "解释", "了解", "学习", "what is", "what are", "how does", "how to prevent"}

// ─── 守卫事件日志 (L1 记录 / L4 取证档案) ───
var guardLogMu sync.Mutex

var dangerousPatterns = []dangerousPattern{
	// 递归/批量删除
	{"删除", regexp.MustCompile(`(?i)\brm\s+-(?:r|f|rf|fr)\b`)},
	{"删除", regexp.MustCompile(`(?i)\bdel\s+/[sq]`)},
	{"删除", regexp.MustCompile(`(?i)Remove-Item\s+-(?:Recurse|Force)`)},
	{"删除", regexp.MustCompile(`(?i)\bos\.RemoveAll\s*\(`)},
	{"删除", regexp.MustCompile(`(?i)\bshutil\.rmtree\s*\(`)},
	{"删除", regexp.MustCompile(`(?i)\bos\.removedirs\s*\(`)},
	{"删除", regexp.MustCompile(`(?i)rd\s+/[sq]`)},
	// 覆盖源码 / 记忆 (铸剑炉本体与记忆数据)
	// 注意: open 读操作不算覆盖, 仅 Python open 带写模式('w'/'a') 才拦。
	{"覆盖", regexp.MustCompile(`(?i)(?:write|touch|append)\s*\(?\s*["']?memory\.json`)},
	{"覆盖", regexp.MustCompile(`(?i)open\s*\(\s*["']memory\.json["']\s*,\s*["'][wa]`)},
	{"覆盖", regexp.MustCompile(`(?i)(?:os\.)?WriteFile\s*\([^)]*(?:memory\.json|forge\.go|agent\.go|main\.go)`)},
	// 磁盘格式化
	{"磁盘", regexp.MustCompile(`(?i)\bformat\s+[a-z]:`)},
	{"磁盘", regexp.MustCompile(`(?i)Format-Volume`)},
	{"磁盘", regexp.MustCompile(`(?i)\bmkfs\b`)},
	// ─── 自杀: 结束 forge 自身进程 (不可逆自毁类, 见 approval_delegate.go) ───
	// 只留「必然作用于炉体本体」的形态: 按镜像名杀全体 / 杀自身 pid / 按名停进程。
	// 这三种没有正当用途: 外部强杀跳过全部收尾(审计落盘/记忆归档/自替换备份),
	// 20261002 实测把炉体杀成静默消失 (events.jsonl 在 approved 那行硬截断)。
	// /IM 不限定 /F 位置: `taskkill /IM forge.exe /F` 与 `/F /IM forge.exe` 都要命中。
	{"自杀", regexp.MustCompile(`(?i)taskkill[^\n]*?/\s*im\s+["']?forge`)},
	{"自杀", regexp.MustCompile(`(?i)kill\s*\(\s*os\.Getpid`)},
	{"自杀", regexp.MustCompile(`(?i)stop-process[^\n]*?-name\s+["']?forge`)},
	// ─── 进程终止: 任何 taskkill /PID X ───
	// 无法辨别父子进程, 故保留审批; 但**不得随委托直通档自动放行**(见 humanOnlyKinds)。
	// 归类为「进程终止」而非「自杀」: 20261002 复核 19 条历史告警, 同类 10 条实为
	// 杀测试自己的子进程 —— 动作确有风险(20261001 实战: /PID 把主进程干掉过),
	// 但对象不明, 冒用「自杀」之名会让真自杀信号失去分量(名实不符=告警贬值)。
	{"进程终止", regexp.MustCompile(`(?i)taskkill.*?/\s*pid\b`)},
	// ─── 包一层: python→subprocess 调 forge.exe ───
	// 意图不是"危险", 而是引导: 走 self gate 应用 selfHostedSelf, 别包一层
	// (包一层会撞 python gate 的 30s 上限, 而 self gate 需要 60s+)。
	// 收窄(20261002): 原判据 [^)]*forge\.exe 会命中 tasklist /FI "IMAGENAME eq
	// forge.exe" 这类**只读查询**, 19 条告警里 2 条是它 —— 纯误报。新判据要求
	// forge.exe 出现在 subprocess 的**程序位**(首参字符串), 查询/传参不再命中。
	{"包一层", regexp.MustCompile(`(?i)subprocess\.[A-Za-z_]+\s*\(\s*(?:\[\s*)?(?:[rbfu]{0,2})["'][^"']*forge\.exe`)},
	// git 强推/硬重置 (不可逆历史操作)
	{"强推", regexp.MustCompile(`(?i)git\s+push\s+.*--force`)},
	{"强推", regexp.MustCompile(`(?i)git\s+reset\s+--hard`)},
	// fork 炸弹
	{"炸弹", regexp.MustCompile(`:\(\s*\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)},
}

// ─── 第二道防线: 受保护目标 × 破坏谓词 共现检测 (语义兜底) ───
// 黑名单(checkDangerousCode)只认"字面危险词", 拦不住
// os.remove("memory.json") 这类单文件删除, 也拦不住 subprocess.run(['rm','-rf',...])
// 这类数组拆分绕过。此处补一道语义层: 目的路径是受保护目标 + 同一窗口内出现破坏谓词
// → 判定危险, 仍走终端 y/N 审批 (多提示一次而非自动阻断, 宁缺毋滥)。
//
// 设计边界: 保护集只含"不可覆盖/删除的关键资产"(工作目录全部 *.go + 记忆/缓存/审计/密钥),
// 不含工作根目录本身 —— 避免把临时文件删除/编译产物清理频繁误报。
// destructiveVerbs 刻意排除只读/复制/存在性检查, 只留删除/覆盖/改名/写坏/强推。

// protectedNonGo: 非源码类关键资产(记忆/缓存/审计/密钥/目录)。
// 源码清单不在此列 —— 见 protectedTargets 的扫描式实现。
var protectedNonGo = []string{
	"memory.json", "forge_cache.gob", "anchor_audit.jsonl", "gate_audit.jsonl",
	".env", ".forge", "defense_system",
}

var (
	protectedMu    sync.Mutex
	protectedCache = map[string][]string{}
)

var destructiveVerbs = []string{
	"os.remove(", "os.removeall(", "os.removedirs(", "os.unlink(",
	"shutil.rmtree(", "shutil.move(", "shutil.removedirs(",
	"path.unlink(", "path.rmdir(", "path.rename(",
	"remove-item", "rd /s ", "rd /q ", "del /s", "del /q",
	"rm -r", "rm -f", "rm -rf", "rm -fr",
	"'rm'", "\"rm\"",
	"os.writefile(", "os.rename(",
	"git push", "git reset --hard",
}

func isStudyQuery(input string) bool {
	low := strings.ToLower(strings.TrimSpace(input))
	for _, p := range studyPrefixes {
		if strings.HasPrefix(low, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// checkInputGuard 返回 (是否阻断, 原因类别, 命中词, 级别)
func checkInputGuard(input string) (bool, string, string, string) {
	// 学习类问句仅标记, 不再整句放行 (旧: HasPrefix 整句放行 → 包裹攻击绕过黑名单)
	// 修正: medium(注入/规则覆盖)词在学习前缀下豁免; critical(破坏/攻击)词一律拦截(红线)
	study := isStudyQuery(input)
	low := strings.ToLower(input)
	for _, r := range guardRules {
		for _, w := range r.words {
			if strings.Contains(low, strings.ToLower(w)) {
				if study && r.level != "critical" {
					continue
				}
				return true, r.kind, w, r.level
			}
		}
	}
	return false, "", "", ""
}

// guardVerdictStr 将复核结果映射为日志用裁决值
func guardVerdictStr(allowed bool, err error) string {
	if err != nil {
		return "error"
	}
	if allowed {
		return "allow"
	}
	return "deny"
}

// logGuardEvent 追加一条守卫事件到 .forge/guard_log.jsonl (运行时文件, 不入基线)
func logGuardEvent(workDir, level, kind, hit, verdict, note, input string) {
	if workDir == "" {
		return
	}
	guardLogMu.Lock()
	defer guardLogMu.Unlock()
	dir := filepath.Join(workDir, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	// 输入脱敏: 只记录前 80 字
	in := input
	if r := []rune(in); len(r) > 80 {
		in = string(r[:80]) + "..."
	}
	// 用 json.Marshal 而非手拼 %q: %q 产出 Go 字面量, 遇无效 UTF-8 会写出 \xXX
	// (非法 JSON 转义) → 取证日志出现无法解析的行。Marshal 会把非法字节替为 U+FFFD。
	entry, merr := json.Marshal(map[string]string{
		"ts":      time.Now().Format("2006-01-02T15:04:05"),
		"level":   level,
		"kind":    kind,
		"hit":     hit,
		"verdict": verdict,
		"note":    note,
		"input":   in,
	})
	if merr != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "guard_log.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if _, werr := f.Write(append(entry, '\n')); werr != nil {
		return
	}
}

// checkDangerousCode 检测代码中的危险操作模式。
// 返回 (类别, 命中模式原文, 是否危险)。未命中返回 ("","",false)。
func checkDangerousCode(code string) (string, string, bool) {
	for _, p := range dangerousPatterns {
		if m := p.re.FindString(code); m != "" {
			return p.kind, m, true
		}
	}
	return "", "", false
}

// parseApproval 解析终端批准输入 (y/yes/是/批准 = 批准)。
func parseApproval(line string) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "是", "批准", "ok", "允许":
		return true
	}
	return false
}

// summarizeCode 危险代码摘要: 去空白后取前 120 字 (终端展示用)。
func summarizeCode(code string) string {
	var sb strings.Builder
	for _, r := range code {
		if r == '\n' || r == '\r' || r == '\t' {
			sb.WriteRune(' ')
		} else {
			sb.WriteRune(r)
		}
	}
	s := strings.Join(strings.Fields(sb.String()), " ")
	runes := []rune(s)
	if len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return s
}

// protectedTargets 返回「不可覆盖/删除的关键资产」清单(按 dir 缓存)。
//
// 为什么扫描而不手写: 手写清单会腐化且无人发现。2026-09 审计实测手写版只覆盖
// 19/53 个 .go —— nosword.go / cache_stats.go / memory_fold.go / memory_recall.go /
// main_commands.go / session.go 等核心文件全部裸奔, 且含 3 条磁盘上已不存在的
// 幽灵路径(gh_token.txt / id_ed25519_vultr / forge_baseline.json)。
// 改为「扫描 dir 下全部 *.go + 显式非源码资产」→ 腐化在结构上不可能。
func protectedTargets(dir string) []string {
	protectedMu.Lock()
	defer protectedMu.Unlock()
	if cached, ok := protectedCache[dir]; ok {
		return cached
	}
	set := map[string]bool{}
	for _, t := range protectedNonGo {
		set[strings.ToLower(t)] = true
	}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				set[strings.ToLower(e.Name())] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	protectedCache[dir] = out
	return out
}

func checkDangerousTarget(code, dir string) (string, string, bool) {
	low := strings.ToLower(code)
	hasVerb := false
	for _, v := range destructiveVerbs {
		if strings.Contains(low, strings.ToLower(v)) {
			hasVerb = true
			break
		}
	}
	if !hasVerb {
		return "", "", false
	}
	for _, t := range protectedTargets(dir) {
		lt := strings.ToLower(t)
		idx := 0
		for {
			p := strings.Index(low[idx:], lt)
			if p < 0 {
				break
			}
			s := idx + p
			lo := s - 80
			hi := s + len(lt) + 80
			if lo < 0 {
				lo = 0
			}
			if hi > len(low) {
				hi = len(low)
			}
			window := low[lo:hi]
			for _, v := range destructiveVerbs {
				if strings.Contains(window, strings.ToLower(v)) {
					return "保护目标", t, true
				}
			}
			idx = s + len(lt)
		}
	}
	return "", "", false
}

// ─── 审批提示的信息完整性 (2026-10 安全测试) ─────────────────────
//
// 缺陷(实测): confirmDangerous 只展示 summarizeCode 的前 120 字, 且不解码任何
// 编码块 —— 危险载荷 base64/hex 编码后, 主人在提示里看到的是一串无意义字符,
// 按 y 等于盲批。实测样本(绕过黑名单后进入审批):
//
//	os.system(__import__("base64").b64decode("aW1wb3J0IG9zOyBvcy5zeXN0ZW0oJ3JtIC1yZiAvdG1wL3gnKQ=="))
//
// 提示只呈现那串字母数字, 真实命令 import os; os.system('rm -rf /tmp/x') 完全不可见。
//
// 修法(纯展示层, 不改任何判定逻辑): 审批前把编码块解码出来一并展示, 并在摘要被
// 截断时标注代码总长度。危险判定仍由 checkDangerousCode / checkDangerousTarget 负责。
//
// 补充(缺口 1, 同日): 只标注"共 N 字"仍不够 —— 实测 205 字代码把
// os.remove('memory.json') 放在末尾, 提示里完全看不到, 主人仍是在盲批。
// 现改为: 命中处若落在首 120 字之外(或跨边界), 追加命中位置前后 60 字的窗口。

var (
	// reB64Block 长 base64 块。hex 串是其字符集子集, 会被一并捕获, 但按 base64
	// 解码得二进制乱码 -> looksLikeText 过滤, 随后仍由 reHexBlock 正确解码。
	reB64Block = regexp.MustCompile(`[A-Za-z0-9+/]{16,}={0,2}`)
	// reHexBlock 长 hex 块。
	reHexBlock = regexp.MustCompile(`[0-9a-fA-F]{32,}`)
)

// looksLikeText 判定解码结果是否「像人话」(合法 UTF-8 + 可打印占比 > 0.85 + ≥8 字节)。
// 判据的存在意义: 普通长标识符解码后是二进制乱码, 若不过滤, 审批提示会变成噪音,
// 主人就会重新开始盲按 —— 噪音化的警示等于没有警示。
func looksLikeText(b []byte) bool {
	if len(b) < 8 || !utf8.Valid(b) {
		return false
	}
	printable, total := 0, 0
	for _, r := range string(b) {
		total++
		if r == '\n' || r == '\r' || r == '\t' || (r >= 32 && r != 127) {
			printable++
		}
	}
	if total == 0 {
		return false
	}
	return float64(printable)/float64(total) > 0.85
}

// flattenCode 把代码压成单行(连续空白合并为一个空格)。
// 与 summarizeCode 同一口径 —— 两者字数必须一致, 否则"共 N 字"与窗口下标会互相矛盾。
// (勿与 agent_pure.go 的 normalizeCode 混用: 那个删空白+去注释, 口径不同。)
func flattenCode(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// hitContext 在规范化代码中定位命中串, 返回 (窗口文本, 命中起始 rune 下标, 命中 rune 长度, 是否找到)。
// 窗口 = 命中串前后各 ctx 字, 两端按需加省略号。定位大小写不敏感兜底(第二道防线
// checkDangerousTarget 返回的目标名与代码里的实际大小写可能不同)。
// 找不到(空 hit / 命中串被规范化改变)时 ok=false, 调用方应静默跳过 —— 宁可不显示,
// 也不显示错位的窗口。
func hitContext(norm, hit string, ctx int) (string, int, int, bool) {
	h := flattenCode(hit)
	if h == "" {
		return "", 0, 0, false
	}
	byteIdx := strings.Index(norm, h)
	if byteIdx < 0 {
		byteIdx = strings.Index(strings.ToLower(norm), strings.ToLower(h))
	}
	if byteIdx < 0 {
		return "", 0, 0, false
	}
	runes := []rune(norm)
	at := len([]rune(norm[:byteIdx]))
	hlen := len([]rune(h))
	lo := at - ctx
	if lo < 0 {
		lo = 0
	}
	hi := at + hlen + ctx
	if hi > len(runes) {
		hi = len(runes)
	}
	w := string(runes[lo:hi])
	if lo > 0 {
		w = "…" + w
	}
	if hi < len(runes) {
		w = w + "…"
	}
	return w, at, hlen, true
}

// clipRunes 按 rune 截断, 超出加省略号 (避免切断多字节字符)。
func clipRunes(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}

// decodeEmbedded 扫描代码中的 base64/hex 编码块并尝试解码, 按出现顺序返回可读结果。
// 同一串只展示一次(shown)。
func decodeEmbedded(code string) []string {
	var out []string
	shown := map[string]bool{}
	for _, m := range reB64Block.FindAllString(code, -1) {
		if shown[m] || len(m)%4 != 0 {
			continue
		}
		dec, err := base64.StdEncoding.DecodeString(m)
		if err != nil || !looksLikeText(dec) {
			continue
		}
		shown[m] = true
		out = append(out, "base64 解码 → "+clipRunes(string(dec), 200))
	}
	for _, m := range reHexBlock.FindAllString(code, -1) {
		if shown[m] || len(m)%2 != 0 {
			continue
		}
		dec, err := hex.DecodeString(m)
		if err != nil || !looksLikeText(dec) {
			continue
		}
		shown[m] = true
		out = append(out, "hex 解码 → "+clipRunes(string(dec), 200))
	}
	return out
}

// approvalExtraText 生成审批提示的附加警示文本 (无附加内容时返回 "")。
// 抽成纯函数是为了可判定: 提示的「信息完整性」本身必须有测试钉住, 否则日后
// 改一行摘要逻辑, 载荷就能重新隐身, 而所有既有测试仍然全绿。
func approvalExtraText(code, hit string) string {
	var sb strings.Builder
	norm := flattenCode(code)
	total := len([]rune(norm))
	if total > 120 {
		fmt.Fprintf(&sb, "  ⚠ 代码共 %d 字, 上面只显示前 120 字\n", total)
	}
	// 命中处若落在首 120 字之外, 上面那行摘要里看不到 —— 主人按 y 时看不到"到底
	// 命中了什么"。把命中位置前后窗口一并展示(与摘要同一规范化口径, 故字数可比)。
	if w, at, hlen, ok := hitContext(norm, hit, 60); ok {
		if at+hlen > 120 {
			where := "截断处之外"
			if at < 120 {
				where = "跨截断边界"
			}
			fmt.Fprintf(&sb, "  ⚠ 命中处上下文(第 %d 字起, %s):\n    %s\n", at+1, where, w)
		}
	}
	if hints := decodeEmbedded(code); len(hints) > 0 {
		sb.WriteString("  ⚠ 检测到编码块, 解码如下(请核对真实意图再决定):\n")
		for _, h := range hints {
			fmt.Fprintf(&sb, "    %s\n", h)
		}
	}
	return sb.String()
}
