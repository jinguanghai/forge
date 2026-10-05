package main

// netroute_test.go — GitHub 红线判据哨兵 (20261003)
//
// 架构即测试(公理三): "把红线写进记忆"不是约束, 死程序判据才是。本文件钉住七件事:
//   V1 路由向量  —— 分流规则按目标判定(镜像/直连/SSH/代理/拒绝), 含边界样本
//   V2 判据向量  —— R1/R2/R3 正例必须命中, 反例必须放行
//   V3 防误伤    —— 注释 / docstring / 块注释里的说明文字不得命中(判据假红与告警误报同源)
//   V4 降级语义  —— 跨行共现只告警不拦(静态不可判定数据流, 误伤代价高于收益)
//   V5 接线      —— Build 里拒绝必须发生在执行之前, 且拒绝分支带 stage=rejected
//   V6 出路      —— 拒绝文本必须含出口工具用法(拒绝必须自带出路)
//   V7 判据表    —— 判据/域名/特征表不得被清空(判据被一起改坏的典型形态)
//   V10 端到端   —— 走 Build 入口提交真实代码: 违规必拒且不执行, 正常代码必放行(防误伤)
//
// 变异自检(判据的判据): 删掉 netroute.go 里的任一判据分支, V2 必报红; 删掉 Build 里的
// 接线, V5 必报红; 把 mirrorHosts/credMarks 清空, V7 必报红。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dq3 Python 三引号定界符。拼接构造: 本文件由生成器写出, 生成器自身不得出现完整定界符
// (与判据"剥离文档串防误伤"同源: 元工具必须能描述判据而不触发判据)。
const dq3 = "\"" + "\"" + "\""

// ── V1 路由向量表 ──
func TestNetRoute_URLVectors(t *testing.T) {
	const m = "gh-proxy.com"
	cases := []struct{ url, want, why string }{
		{"https://" + m + "/https://github.com/o/r/archive/main.zip", netRouteMirror, "公开下载路径 => 镜像"},
		{"https://" + m + "/https://github.com/o/r/raw/main/a.py", netRouteMirror, "raw 文件 => 镜像"},
		{"https://raw.githubusercontent.com/o/r/main/a.go", netRouteMirror, "raw 域名 => 镜像"},
		{"https://codeload.github.com/o/r/zip/main", netRouteMirror, "codeload => 镜像"},
		{"https://github.com/o/r/releases/download/v1/x.zip", netRouteMirror, "release 下载 => 镜像"},
		{"https://api.github.com/repos/o/r", netRouteDirect, "API => 直连(经代理超时)"},
		{"git@github.com:o/r.git", netRouteSSH, "git over SSH => SSH 直连"},
		{"ssh://git@github.com/o/r.git", netRouteSSH, "ssh scheme => SSH 直连"},
		{"https://github.com/o/r", netRouteProxy, "非下载路径 => 代理(保守, 可能含登录态)"},
		{"https://github.com/settings/tokens", netRouteProxy, "登录态路径 => 代理, 绝不经镜像"},
		{"https://example.com/x", netRouteProxy, "其他境外目标 => 代理"},
		{"", netRouteProxy, "空目标 => 代理"},
		{"https://user:pass@" + m + "/https://github.com/o/r", netRouteDeny, "凭据进镜像 URL => 拒绝"},
		{"https://" + m + "/https://user:pass@github.com/o/r", netRouteDeny, "镜像内嵌带凭据 URL => 拒绝"},
	}
	for _, c := range cases {
		got, why := routeForURL(c.url)
		if got != c.want {
			t.Errorf("routeForURL(%q) = %q(%s); 期望 %q —— %s", c.url, got, why, c.want, c.why)
		}
	}
}

// ── V2 判据向量表(正例必中 / 反例必放) ──
func TestNetRoute_RedlineVectors(t *testing.T) {
	const (
		m    = "gh-proxy.com"
		m2   = "ghfast.top"
		tok  = "ghp_AbCdEf0123456789"
		auth = "Authorization"
	)
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"R1 凭据在镜像 userinfo", `u = "https://user:pass@` + m + `/https://github.com/o/r"`, true},
		{"R1 token 在镜像 userinfo", `u = "https://` + tok + `@` + m + `/x"`, true},
		{"R1 镜像内嵌带凭据原 URL", `u = "https://` + m + `/https://user:pass@github.com/o/r"`, true},
		{"R2 同行镜像+授权头", `h = {"` + auth + `": "Bearer z"}; u = "https://` + m + `/x"`, true},
		{"R2 同行镜像+token 前缀", `u = "https://` + m + `/x?` + tok + `"`, true},
		{"R2 备胎镜像同判", `u = "https://` + m2 + `/x"; t = "` + tok + `"`, true},
		{"R3 同行镜像+push", `cmd = "git push https://` + m + `/o/r.git"`, true},
		{"R3 同行镜像+receive-pack", `cmd = "git " + "` + m + `/o/r receive-pack"`, true},

		{"反例: 纯公开镜像下载", `u = "https://` + m + `/https://github.com/o/r/raw/main/a.py"`, false},
		{"反例: 镜像 URL 无凭据", `u = "https://` + m + `/https://github.com/o/r/archive/main.zip"`, false},
		{"反例: 凭据走 API 直连", `u = "https://api.github.com/x"; h = {"` + auth + `": t}`, false},
		{"反例: git push 走 SSH", `cmd = "git push git@github.com:o/r.git"`, false},
		{"反例: 镜像 + 说明文字(token 泛词)", `note = "` + m + ` 只可下载公开内容, 不可带 token 或 key"`, false},
		{"反例: 注释里说明红线", "# " + `"` + m + ` 不可带 ` + auth + ` 头"`, false},
		{"反例: C 风格注释", "// " + `"` + m + ` 不可带 ` + auth + `"`, false},
	}
	for _, c := range cases {
		_, ev, hit := checkNetRedline(c.code)
		if hit != c.want {
			t.Errorf("%s: checkNetRedline 命中=%v(证据 %q), 期望 %v\ncode=%s",
				c.name, hit, ev, c.want, c.code)
		}
	}
}

// ── V3 防误伤: 说明文字(注释/文档串/块注释)一律不得命中 ──
//
// 这是本判据最容易翻车的地方: 20261003 实测过"分析脚本被自己的判据拦下"(正则里含关键字)。
// 判定前必须剥离注释与文档串, 且凭据特征只取强特征(不含泛词 token/key/auth)。
func TestNetRoute_NoFalsePositiveOnProse(t *testing.T) {
	const (
		m    = "gh-proxy.com"
		auth = "Authorization"
	)
	prose := []struct{ name, code string }{
		{"python 行注释", "# " + m + " 是第三方服务器, 不可带 " + auth + " 头"},
		{"python 缩进行注释", "    # " + m + " 不可带 " + auth},
		{"go 行注释", "// " + m + " 不可带 " + auth},
		{"go 行尾注释", `u := "https://example.com" // 对比 ` + m + ` 的 ` + auth},
		{"python 文档串(单行)", dq3 + m + " 不可带 " + auth + " 头" + dq3},
		{"python 文档串(多行)", "def f():\n    " + dq3 + "\n    " + m + " 不可带 " + auth + "\n    " + dq3 + "\n    return 1"},
		{"C 风格块注释", "/* " + m + " 不可带 " + auth + " */\nx := 1"},
		{"泛词不算凭据", `u = "https://` + m + `/x"; k = "my_token"; a = "api_key"`},
		{"URL 里的 :// 不是注释", `u = "https://` + m + `/https://github.com/o/r/raw/main/a.py"`},
	}
	for _, c := range prose {
		if _, ev, hit := checkNetRedline(c.code); hit {
			t.Errorf("误伤: %s 被判为红线(证据 %q)\ncode=%q", c.name, ev, c.code)
		}
	}
}

// ── V4 降级语义: 跨行共现只告警不拦 ──
func TestNetRoute_CrossLineIsWarnOnly(t *testing.T) {
	const (
		m    = "gh-proxy.com"
		auth = "Authorization"
	)
	code := `mirror = "https://` + m + `/"
headers = {"` + auth + `": "Bearer z"}`
	if _, ev, hit := checkNetRedline(code); hit {
		t.Errorf("跨行共现不得拦(静态不可判定数据流): 命中=%q", ev)
	}
	if w := netWarnCrossLine(code); w == "" {
		t.Error("跨行共现必须记降级告警 —— 不拦不等于静默")
	}
	// 反例: 两者都不出现的代码不得产生告警(告警贬值是真实杀伤链)
	if w := netWarnCrossLine(`u = "https://example.com/x"`); w != "" {
		t.Errorf("无共现却产生告警: %q", w)
	}
}

// ── V5 接线断言: Build 里拒绝必须发生在执行之前 ──
func TestNetRoute_WiredInBuild(t *testing.T) {
	raw, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatalf("读 forge.go 失败: %v", err)
	}
	src := string(raw)
	iCall := strings.Index(src, "f.preflightDeny(code, lang)")
	iRetry := strings.Index(src, "f.retryGate(code, lang, input)")
	if iCall < 0 {
		t.Fatal("Build 未接线 preflightDeny —— 红线退化为纯文本约束(这正是本次要修的缺陷)")
	}
	if iRetry < 0 {
		t.Fatal("未找到 retryGate 调用点: 接线顺序无法判定")
	}
	if iCall > iRetry {
		t.Error("拒绝必须发生在执行之前(先拒后执行): 接线位置在 retryGate 之后")
	}
	// 执行臂 (20261003 迁至 preflight.go 以守住主路径行数预算): 入口不得被换成空壳。
	pf, err := os.ReadFile("preflight.go")
	if err != nil {
		t.Fatalf("读 preflight.go 失败: %v", err)
	}
	psrc := string(pf)
	if !strings.Contains(psrc, "checkNetRedline(code)") {
		t.Error("netRedlineDeny 未调用 checkNetRedline —— 红线判据被架空")
	}
	if !strings.Contains(psrc, "f.netRedlineDeny(code, lang)") {
		t.Error("preflightDeny 未收拢 netRedlineDeny —— 入口漏臂")
	}
	for _, want := range []string{`Stage: "rejected"`, "netRedlineText", "netRedlineEnabled()"} {
		if !strings.Contains(psrc, want) {
			t.Errorf("拒绝分支缺 %q —— 分支被拆散或未接线", want)
		}
	}
}

// ── V6 出路: 拒绝必须自带替代做法 ──
func TestNetRoute_RefusalTextHasExit(t *testing.T) {
	for _, want := range []string{"netroute.py", "route", "fetch", "git ls-remote", "api.github.com", "SSH"} {
		if !strings.Contains(netRedlineText, want) {
			t.Errorf("拒绝文本缺出路要素 %q —— 只拒绝不给替代 = 断路", want)
		}
	}
}

// ── V6b 出口工具必须真实存在(文本指路指向空气 = 假出路) ──
func TestNetRoute_ExitToolExists(t *testing.T) {
	p := filepath.Join(".forge", "forge-tools", "netroute.py")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("出口工具缺失: %v —— 拒绝文本在指路, 路却不存在", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("出口工具为空文件: %s", p)
	}
}

// ── V7 判据表不得被清空(判据被一起改坏的典型形态) ──
func TestNetRoute_RulesNotEmptied(t *testing.T) {
	if len(netRules) != 3 {
		t.Errorf("判据表应为 3 条(R1/R2/R3), 实际 %d —— 判据被清空或漏登", len(netRules))
	}
	names := map[string]bool{}
	for _, r := range netRules {
		names[r.Name] = true
		if r.Hit == nil {
			t.Errorf("判据 %q 的 Hit 为 nil", r.Name)
		}
	}
	for _, want := range []string{"R1", "R2", "R3"} {
		found := false
		for n := range names {
			if strings.HasPrefix(n, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("判据 %s 丢失", want)
		}
	}
	if len(mirrorHosts) == 0 {
		t.Error("镜像域名表被清空 —— 判据全部失效但用例照样绿")
	}
	if len(credMarks) == 0 {
		t.Error("凭据特征表被清空")
	}
	if len(writeMarks) == 0 {
		t.Error("写操作特征表被清空")
	}
	// 泛词不得进凭据表: 取泛词必在说明文字上误伤(V3 的根因)。
	for _, bad := range []string{"token", "key", "auth", "password"} {
		for _, m := range credMarks {
			if strings.EqualFold(strings.TrimSpace(m), bad) {
				t.Errorf("凭据表含泛词 %q —— 必在说明文字上误伤", m)
			}
		}
	}
}

// ── V8 开关语义: 关闭只跳过拦截, 不改变判定结果 ──
func TestNetRoute_GuardEnabled(t *testing.T) {
	t.Setenv("FORGE_NET_REDLINE", "")
	if !netRedlineEnabled() {
		t.Error("默认必须开启(建议类约束实测无效, 不能默认关)")
	}
	t.Setenv("FORGE_NET_REDLINE", "0")
	if netRedlineEnabled() {
		t.Error("FORGE_NET_REDLINE=0 必须关闭")
	}
	t.Setenv("FORGE_NET_REDLINE", "off")
	if netRedlineEnabled() {
		t.Error("FORGE_NET_REDLINE=off 必须关闭")
	}
	t.Setenv("FORGE_NET_REDLINE", "1")
	if !netRedlineEnabled() {
		t.Error("显式 1 必须开启")
	}
}

// ── V9 剥离器自身: 行号必须守恒(证据行号错位会让排查失去定位) ──
func TestNetRoute_StripKeepsLineCount(t *testing.T) {
	code := "a\n" + dq3 + "\n" + dq3 + "\nb\nc"
	if got, want := strings.Count(stripComments(code), "\n"), strings.Count(code, "\n"); got != want {
		t.Errorf("剥离后行数变化: %d -> %d(证据行号会错位)", want, got)
	}
}

// ── V10 端到端: 走 Build 入口(真实 gate 路径), 拒绝必须发生在执行之前 ──
//
// 为什么不能只测纯函数: checkNetRedline 返回 true 不等于"提交时会被拦" ——
// 接线位置错(在 retryGate 之后)、开关判反、返回值被忽略, 三种情况都能让纯函数
// 用例全绿而生产路径照放行。这三条用例走的就是生产路径。
func TestNetRoute_BuildRejectsRedlineEndToEnd(t *testing.T) {
	const (
		m   = "gh-proxy.com"
		tok = "ghp_AbCdEf0123456789"
	)
	f := &Forge{workDir: t.TempDir(), ctx: context.Background(),
		sem: make(chan struct{}, 1), cache: map[string]ForgeGateResult{}}
	code := `u = "https://` + m + `/x?` + tok + `"
print("SHOULD_NOT_RUN")`
	out, res, err := f.Build(code, "python", "")
	if err != nil {
		t.Fatalf("Build 返回硬错误(红线应是软拒绝): %v\n%s", err, out)
	}
	if res == nil || res.OK {
		t.Fatalf("红线代码必须被拒, 实际 res=%+v\n%s", res, out)
	}
	if res.Stage != "rejected" {
		t.Errorf("stage 应为 rejected, 实际 %q", res.Stage)
	}
	if strings.Contains(out, "SHOULD_NOT_RUN") {
		t.Error("拒绝之后代码仍被执行 —— 拒绝没有发生在执行之前")
	}
	if !strings.Contains(out, "netroute.py") {
		t.Errorf("拒绝文本缺出口工具(拒绝必须自带出路):\n%s", out)
	}
}

// TestNetRoute_BuildAllowsNormalCode 反例: 正常代码必须放行(防误伤)。
// 误伤会让人直接关掉守卫 —— 那是比没有守卫更糟的结果。
func TestNetRoute_BuildAllowsNormalCode(t *testing.T) {
	f := &Forge{workDir: t.TempDir(), ctx: context.Background(),
		sem: make(chan struct{}, 1), cache: map[string]ForgeGateResult{}}
	out, res, err := f.Build(`print("ok-netroute")`, "python", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("正常代码被拦(误伤): err=%v res=%+v\n%s", err, res, out)
	}
	if !strings.Contains(out, "ok-netroute") {
		t.Errorf("正常代码未被执行:\n%s", out)
	}
}

// TestNetRoute_BuildAllowsPublicMirrorDownload 反例: 公开内容走镜像是合法用法, 必须放行。
func TestNetRoute_BuildAllowsPublicMirrorDownload(t *testing.T) {
	const m = "gh-proxy.com"
	f := &Forge{workDir: t.TempDir(), ctx: context.Background(),
		sem: make(chan struct{}, 1), cache: map[string]ForgeGateResult{}}
	code := `u = "https://` + m + `/https://github.com/o/r/raw/main/a.py"
print("ok-mirror")`
	out, res, err := f.Build(code, "python", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("公开镜像下载被拦(误伤): err=%v res=%+v\n%s", err, res, out)
	}
	if !strings.Contains(out, "ok-mirror") {
		t.Errorf("代码未执行:\n%s", out)
	}
}
