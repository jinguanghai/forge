package main

// netroute.go — GitHub 出口分流与红线拒绝权 (20261003)
//
// 动机: agent_memory.go 早已写「gh-proxy 是第三方服务器, 只可用于公开内容, 登录 GitHub /
// 私有仓库 / token 绝不可经它」—— 与重活判据同构的失效: 建议类约束对 LLM 无效。
// 实测全仓 grep 该镜像域名: 仅命中记忆文本, 代码里 0 处判据 => 红线 100% 靠自觉, 物理上零拦截。
// 处置(同 sh gate 退役 / 重活拒绝权): 把建议升级为 gate 层前置拒绝权 —— 命中即当场拒绝并附
// 替代写法。拒绝可判定且当场截断, 猜测要烧一整轮重试。
//
// 为什么独立成文件: forge.go 是核心编排文件, 顶层声明数受分形守卫 F1 约束。
// 判据 / 文本 / 开关三者同源同文件, 避免"改判据漏改文本"的漂移。
//
// 判据 (必须建立在「物理不可能 / 逻辑矛盾」上, 不得建立在「概率相关」上):
//
//	R1 凭据写入镜像 URL —— 凭据落在镜像 URL 的 userinfo 位置
//	   (https://user:pass@mirror/... ) 镜像服务器的请求行必然含该凭据 => 第三方必然看到
//	R2 同行镜像 + 凭据   —— 同一行内镜像域名与凭据强特征同时出现
//	   (同一行基本等价于同一个请求构造; 跨行共现静态不可判定, 见降级说明)
//	R3 同行镜像 + 写操作 —— 镜像只做 GET 转发(实测不支持 push), 故镜像 + push 属能力上不存在
//
// 降级(不拦, 只记审计字段): 同文件跨行共现走 netWarnCrossLine —— 静态不可判定数据流,
// 误伤代价高于收益(heavy_replay 实证: 概率型判据精确率 1.6%~13.5%, 必然误伤成灾)。
//
// 凭据特征只取「强特征」不取泛词: "token" 这类词在说明文字里高频出现, 取泛词必误伤
// (实测: docstring 里写一句红线说明就会被自己的判据拦下)。注释与三引号块在判定前剥离。
//
// 豁免: FORGE_NET_REDLINE=0 关闭(调试用, 关闭仍写审计)。
// 配套臂: .forge/forge-tools/netroute.py(route/fetch/git 三个子命令)—— 让正确做法比
// 错误做法省事。拒绝必须自带出路(sh_retired.go 同款教训: 只拒绝不给替代 = 断路)。
//
// 哨兵: netroute_test.go 正例/反例双向量表 + Build 层接线断言; 回放门槛 netroute_replay.py
// (在历史合规样本上假阳性必须为 0), 接线由 netroute_replay_wiring_test.go 钉住。
import (
	"os"
	"regexp"
	"strings"
)

// ─── 路由常量 (单一数据源: 出口工具 netroute.py 的判据与之同构) ───
const (
	netRouteMirror = "mirror" // 第三方镜像直连(公开内容专用, 须 --noproxy)
	netRouteDirect = "direct" // 直连(api.github.com: 经代理超时)
	netRouteSSH    = "ssh"    // SSH 直连(git 操作, 不经任何第三方)
	netRouteProxy  = "proxy"  // VPS SOCKS5(其他境外目标)
	netRouteDeny   = "deny"   // 红线拒绝
)

// netRedlineText 拒绝回告 —— 拒绝必须自带出路。
const netRedlineText = `GitHub 红线: 第三方镜像只可用于公开内容, 登录态 / 私有仓库 / 凭据绝不可经它。
镜像服务器会看到完整 URL 与请求头 —— 凭据一旦经它, 第三方必然留存。这不是"风险较高", 是物理必然。

正确做法(出口工具, 一行搞定):
  python .forge/forge-tools/netroute.py route <url>          # 查该走哪条出口
  python .forge/forge-tools/netroute.py fetch <url> -o <path>  # 公开内容: 自动镜像直连
  python .forge/forge-tools/netroute.py git ls-remote <repo>   # git 操作: 自动 SSH 直连

分流规则(20261003 实测, 双路径对照):
  · 公开下载路径(github.com/<o>/<r>/raw|archive|releases, raw.githubusercontent.com)
       -> gh-proxy 镜像直连(须 --noproxy): raw 直连 000 被阻断, 镜像 0.62s
  · api.github.com           -> 直连: 经代理超时, 直连 1.23s
  · git ls-remote/clone/push -> SSH 直连 git@github.com:22, GIT_SSH_COMMAND 指定密钥
  · 其他境外目标             -> VPS SOCKS5 127.0.0.1:1080

确需凭据时: 走 SSH 直连(不经任何第三方), 或改走 api.github.com 直连。`

// ─── 红线判据 (单一数据源: 新增判据只加一行, 文本与哨兵自动跟随) ───

// netRule 一条红线判据。Name 同时进拒绝文本与审计, 便于按判据度量命中率。
type netRule struct {
	Name string
	Hit  func(code string) (string, bool)
}

// mirrorHosts 第三方镜像域名 —— 只可用于公开内容, 绝不可承载凭据。
var mirrorHosts = []string{"gh-proxy.com", "ghfast.top", "ghproxy.net"}

// credMarks 凭据强特征。刻意不含泛词 "token"/"key"/"auth": 说明文字里高频出现,
// 取泛词会让判据在 docstring 上误伤(判据假红与告警误报同源: 判据与语义脱节)。
var credMarks = []string{
	"ghp_", "github_pat_", "gho_", "ghs_", "ghu_",
	"Authorization", "x-access-token", "oauth2:", "Bearer ",
	"access_token", "GITHUB_TOKEN", "GH_TOKEN", "--token",
}

// writeMarks 写操作特征。第三方镜像只做 GET 转发(实测不支持 push), 故"镜像 + 写"是
// 能力上不存在(物理不可能型), 不是"风险较高"(概率型)。
var writeMarks = []string{"git push", "git-push", "receive-pack", "--upload-pack"}

// userinfoRE 匹配 URL 里的 userinfo(user:pass@ 形态)。
// 与 R1 判据同源: 凭据落在 authority 位置, 镜像的请求行必然含它。
var userinfoRE = regexp.MustCompile(`(?i)[A-Za-z0-9_.%~+-]{1,64}:[^\s/@]{1,128}@`)

var netRules = []netRule{
	{"R1 凭据写入镜像 URL", hitMirrorUserinfo},
	{"R2 同行镜像+凭据", hitMirrorCredSameLine},
	{"R3 同行镜像+写操作", hitMirrorWriteSameLine},
}

// mirrorHostAlt 镜像域名的正则片段(集中一处, 与 mirrorHosts 同层)。
var mirrorHostAlt = `gh-proxy\.com|ghfast\.top|ghproxy\.net`

// mirrorUserinfoRE R1: 凭据落在镜像 URL 的 userinfo 位置。
// 形态 https://user:pass@mirror/... —— 请求行必然含凭据。
var mirrorUserinfoRE = regexp.MustCompile(`(?i)[A-Za-z0-9_.%~+-]{1,64}:[^\s/@]{1,128}@(?:` + mirrorHostAlt + `)`)

// mirrorNestedCredRE R1 第二形态: 镜像 URL 内嵌带凭据的原 URL
// (https://mirror/https://user:pass@github.com/...) —— 凭据同样落在镜像收到的请求行里。
// 排除引号与反引号, 避免跨字符串边界误判; 中间的 "://" 因 [^\s/@] 不含 '/' 而天然不匹配。
var mirrorNestedCredRE = regexp.MustCompile(`(?i)(?:` + mirrorHostAlt + `)[^\s"'` + "`" + `]{0,200}:[^\s/@]{1,128}@`)

// isMirrorHost 判定 host 是否第三方镜像(含子域)。
func isMirrorHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, m := range mirrorHosts {
		if host == m || strings.HasSuffix(host, "."+m) {
			return true
		}
	}
	return false
}

// urlHost 从 URL / git 远端 / 裸 host 中取主机名。容错: 解析失败返回空串
// (宁可判不出也不误判 —— 空串会走默认代理路由, 不会进镜像分支)。
func urlHost(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// 顺序不可颠倒(实测两个反例都踩过):
	//   ① https://user:pass@mirror/... —— 先切 @ 再切 "://" 会把 host 解析成别的东西
	//   ② https://mirror/https://user:pass@github.com/... —— 不先按 authority 边界截断,
	//      会把嵌套 URL 里的 userinfo 当成自己的, host 解析成 github.com, 于是
	//      "凭据进镜像 URL"被误判成正常公开内容(判据漏检 = 红线失守)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 { // authority 到此为止
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 { // user:pass@host / git@host
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 { // host:port / git@host:owner(scp 语法)
		s = s[:i]
	}
	return s
}

// hasCredential 判定文本是否含凭据强特征。
func hasCredential(s string) bool {
	for _, m := range credMarks {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// hasUserinfo 判定文本是否含 user:pass@ 形态的 userinfo。
func hasUserinfo(s string) bool {
	return userinfoRE.MatchString(s)
}

// isGitSSH 判定是否 git over SSH 形态。
func isGitSSH(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(l, "git@") || strings.HasPrefix(l, "ssh://")
}

// isPublicGitHubPath GitHub 公开内容路径(可安全经第三方镜像)。
// 刻意只认"下载类"路径: /raw/ /archive/ /releases/download/。网页(blob/tree)与
// 登录态路径一律走代理, 保守优先 —— 误判成镜像会把登录态送出去, 误判成代理只是慢。
func isPublicGitHubPath(low string) bool {
	for _, seg := range []string{"/raw/", "/archive/", "/releases/download/", "/releases/"} {
		if strings.Contains(low, seg) {
			return true
		}
	}
	return false
}

// routeForURL 返回目标的出口路由与依据。纯函数: 无 IO 无全局状态, 便于向量化测试。
//
// 分流规则来源(20261003 实测, 双路径对照): raw 直连 000 被阻断而镜像 0.62s;
// api.github.com 经代理超时而直连 1.23s; git@github.com:22 直连认证成功。
// 结论只能是「按目标分流」而非「某条路更好」—— 同一目标两条路径可达性可以相反。
func routeForURL(raw string) (route, reason string) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return netRouteProxy, "空目标: 默认走 VPS 代理"
	}
	low := strings.ToLower(s)

	// 红线优先于一切分流: 凭据一旦进镜像 URL, 第三方必然留存(物理必然, 非风险)。
	if h := urlHost(low); isMirrorHost(h) {
		// 两种凭据形态都要拦: 强特征(ghp_/Authorization/...) 与 userinfo(user:pass@)。
		// 只拦前者会漏掉 https://user:pass@mirror/... 这类明文凭据。
		if hasCredential(low) || hasUserinfo(low) {
			return netRouteDeny, "镜像 URL 内含凭据: 第三方镜像必然看到 => 红线"
		}
		return netRouteMirror, "第三方镜像直连(公开内容): 须 --noproxy, 实测 0.62s"
	}
	if isGitSSH(s) {
		return netRouteSSH, "git over SSH: 直连 git@github.com:22, 不经第三方"
	}
	switch urlHost(low) {
	case "api.github.com":
		return netRouteDirect, "GitHub API: 经代理超时, 直连 1.23s"
	case "github.com", "www.github.com":
		if isPublicGitHubPath(low) {
			return netRouteMirror, "GitHub 公开下载路径 => 镜像(直连被阻断)"
		}
		return netRouteProxy, "GitHub 非下载路径(可能含登录态) => 代理, 绝不经第三方镜像"
	case "raw.githubusercontent.com", "codeload.github.com", "gist.githubusercontent.com",
		"objects.githubusercontent.com":
		return netRouteMirror, "GitHub 静态内容 => 镜像"
	}
	return netRouteProxy, "默认境外目标走 VPS SOCKS5"
}

// hitMirrorUserinfo R1: 物理不可能型 —— 凭据在 URL 里, 镜像必收。
func hitMirrorUserinfo(code string) (string, bool) {
	if m := mirrorUserinfoRE.FindString(code); m != "" {
		return m, true
	}
	if m := mirrorNestedCredRE.FindString(code); m != "" {
		return m, true
	}
	return "", false
}

// hitMirrorCredSameLine R2: 同一行内镜像域名与凭据强特征共现。
// 逐行判定(而非整篇共现): 整篇共现会误伤"同文件里两处不相关代码"。
func hitMirrorCredSameLine(code string) (string, bool) {
	for _, ln := range strings.Split(stripComments(code), "\n") {
		if !lineHasMirror(ln) || !hasCredential(ln) {
			continue
		}
		return strings.TrimSpace(snipLine(ln)), true
	}
	return "", false
}

// hitMirrorWriteSameLine R3: 同一行内镜像域名与写操作共现。
// 镜像只做 GET 转发(实测不支持 push), 故这是能力上不存在, 不是"风险较高"。
func hitMirrorWriteSameLine(code string) (string, bool) {
	for _, ln := range strings.Split(stripComments(code), "\n") {
		if !lineHasMirror(ln) {
			continue
		}
		l := strings.ToLower(ln)
		for _, w := range writeMarks {
			if strings.Contains(l, w) {
				return strings.TrimSpace(snipLine(ln)), true
			}
		}
	}
	return "", false
}

// lineHasMirror 判定一行内是否出现镜像域名。
func lineHasMirror(ln string) bool {
	l := strings.ToLower(ln)
	for _, m := range mirrorHosts {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// snipLine 截断证据行, 避免拒绝文本被超长行淹没(证据要能读)。
func snipLine(s string) string {
	if len(s) > 160 {
		return s[:160] + "..."
	}
	return s
}

// stripComments 剥离注释后再判定 —— 否则"说明红线的注释行"会被判据自己命中
// (20261003 同型事故: 分析脚本被自己的判据拦下)。
// 处理三种形态: 行注释(行首或空白后的 # 与 //)、Python 三引号块、C 风格块注释。
// 不剥字符串字面量: 真实请求构造正在字符串里, 剥了就漏检。
func stripComments(code string) string {
	code = stripBlockComments(code, "/*", "*/")
	code = stripBlockComments(code, `"""`, `"""`)
	code = stripBlockComments(code, "'''", "'''")
	var out []string
	for _, ln := range strings.Split(code, "\n") {
		out = append(out, stripLineComment(ln))
	}
	return strings.Join(out, "\n")
}

// stripBlockComments 单遍状态机移除成对的块定界符之间的内容(替换为空行, 保持行号)。
func stripBlockComments(code, open, close string) string {
	if open == close { // 三引号: 奇偶切换
		parts := strings.Split(code, open)
		for i := 1; i < len(parts); i += 2 {
			// 保留等量换行 —— 行号在剥离前后必须一致, 否则证据行号错位。
			parts[i] = strings.Repeat("\n", strings.Count(parts[i], "\n"))
		}
		return strings.Join(parts, open)
	}
	var b strings.Builder
	in := false
	for i := 0; i < len(code); {
		if !in && strings.HasPrefix(code[i:], open) {
			in = true
			i += len(open)
			continue
		}
		if in && strings.HasPrefix(code[i:], close) {
			in = false
			i += len(close)
			continue
		}
		if !in {
			b.WriteByte(code[i])
		} else if code[i] == '\n' {
			b.WriteByte('\n')
		}
		i++
	}
	return b.String()
}

// stripLineComment 去掉行注释。注释起始 = 行首的 # 或 //, 或空白之后的 # 或 //。
// 刻意不把 URL 里的 "://" 当注释: 其前一个字符是 ':' 而非空白。
func stripLineComment(ln string) string {
	for i := 0; i < len(ln); i++ {
		c := ln[i]
		atStart := i == 0 || ln[i-1] == ' ' || ln[i-1] == '\t'
		if !atStart {
			continue
		}
		if c == '#' {
			return ln[:i]
		}
		if c == '/' && i+1 < len(ln) && ln[i+1] == '/' {
			return ln[:i]
		}
	}
	return ln
}

// checkNetRedline 判定代码是否命中红线。返回 (判据名, 证据, 命中)。
func checkNetRedline(code string) (name, evidence string, hit bool) {
	for _, r := range netRules {
		if ev, ok := r.Hit(code); ok {
			return r.Name, ev, true
		}
	}
	return "", "", false
}

// netWarnCrossLine 降级告警: 同文件内镜像域名与凭据共现但不满足 R1~R3(跨行)。
// 静态不可判定数据流 —— 拒了会误伤(同一脚本里"镜像拉公开内容"与"token 调 api"是
// 合法组合), 所以只记审计字段供事后度量, 不拦。
func netWarnCrossLine(code string) string {
	c := stripComments(code)
	if !lineHasMirror(c) || !hasCredential(c) {
		return ""
	}
	return "R4 跨行镜像+凭据(降级告警, 未拦)"
}

// netRedlineEnabled 拒绝权开关。默认开; FORGE_NET_REDLINE=0/off/false 关闭。
// 关闭不等于静默: 关闭只跳过拦截, 审计与留痕路径不变(见 Build 的拒绝分支)。
func netRedlineEnabled() bool {
	v := strings.TrimSpace(os.Getenv("FORGE_NET_REDLINE"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "off", "false", "no":
		return false
	}
	return true
}
