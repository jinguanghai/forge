package main

// net_flake_test.go — 网络抖动判定 (20261003)
//
// 动机 (实测, 非推断):
//   full 档偶发红。同一份代码, 13:44 那次 PASS, 13:57 那次 FAIL,
//   唯一差异是 browser gate 抓 example.com 返回 net::ERR_CONNECTION_CLOSED。
//   该用例走代理(SOCKS5 127.0.0.1:1080), 代理抖动即失败 —— 与代码质量无关。
//
// 处置: 命中网络层特征 → 降级为「跳过(网络不可用)」并保留原始错误摘要, 不算 FAIL。
// 降级必须留痕(测试日志 + bench 报告), 不得静默 —— 静默降级 = 真缺陷隐身。
//
// 形态族化 (T40 · 20261010 实测):
//   旧表只收固定错误串, 而源码注释自陈「唯一差异是 net::ERR_CONNECTION_CLOSED」——
//   那句"唯一"就是盲区本身: 把「我见过的那一种」当成了「全部」。
//   实测反例: 同一份代码第 4 次跑报 `Page.goto: Timeout 30000ms exceeded`,
//   超时族一个都不在旧表里 -> 降级失效 -> 假红(用例其实已标 net:true)。
//   修法: 按【形态族】收 (超时族/连接族/DNS族/代理族), 不再按固定串。
//   放宽的代价用两条防线抵住: ① 判定权仍窄 —— 只对显式 net:true 用例生效;
//   ② 降级必入 note 不静默。判据侧同步加反例向量 (见 TestNetFlakeDetectorSelfCheck),
//   钉住「放宽不得连业务/成本类错误一起降级」。
//
// 为什么不用「开跑前探测可达性」:
//   browser_gate_test.go 的 bingReachable() 探的是 cn.bing.com(中国域名, 直连),
//   而被测的 example.com 走代理 —— 探的链路与实际链路不同, 预检通过仍会失败
//   (判据与语义脱节的典型)。事后特征匹配才能覆盖「预检通过但中途抖动」。
//
// 为什么只对显式声明 net:true 的用例生效:
//   宽口子会把真缺陷一起降级掉。判定权必须窄, 且由用例自己声明依赖外网。

import (
	"strings"
	"testing"
)

// netFlakePatterns 网络层不可用特征 (playwright/chromium 措辞 + Go net 措辞)。
// 只收「链路不可用」语义, 不收业务错误 —— 过宽即掩盖真缺陷。
var netFlakePatterns = []string{
	"net::err_", // playwright 统一前缀: ERR_CONNECTION_CLOSED / ERR_TIMED_OUT / ERR_PROXY_CONNECTION_FAILED / ERR_NAME_NOT_RESOLVED / ERR_EMPTY_RESPONSE ...
	"err_connection",
	"err_timed_out",
	"err_proxy",
	"err_tunnel",
	"err_name_not_resolved",
	"err_internet_disconnected",
	"err_empty_response",
	"connection refused",
	"connection reset",
	"actively refused it", // Windows 措辞, 不含 "connection refused" 字面
	"proxyconnect",
	"no such host",
	"i/o timeout",
	"dial tcp",
	"tls handshake timeout",
	"context deadline exceeded",
	// --- T40 (20261010) 超时族补齐: playwright 的 Timeout 措辞不在上面任何一种字面里 ---
	"ms exceeded",     // playwright 统一措辞: "Page.goto: Timeout 30000ms exceeded"
	"timeout 30000ms", // 同上, 防错误文本被截断到不含 "ms exceeded" (同一族的不同切面, 冗余是安全的)
	"timed out",       // 通用超时措辞: "connection timed out" / "request timed out"
}

// isNetFlake 判定输出是否为网络层不可用, 返回 (命中, 命中的特征)。
func isNetFlake(out string) (bool, string) {
	low := strings.ToLower(out)
	for _, p := range netFlakePatterns {
		if strings.Contains(low, p) {
			return true, p
		}
	}
	return false, ""
}

// gateOutText 把 gate 结果的各文本字段与 err 合成一段, 供特征匹配。
func gateOutText(res *ForgeGateResult, err error) string {
	var b strings.Builder
	if err != nil {
		b.WriteString(err.Error())
		b.WriteString("\n")
	}
	if res != nil {
		b.WriteString(res.Stdout)
		b.WriteString("\n")
		b.WriteString(res.Stderr)
		b.WriteString("\n")
		b.WriteString(res.Error)
		b.WriteString("\n")
		b.WriteString(res.Diagnostics)
	}
	return b.String()
}

// applyNetFlake 网络抖动降级: 仅对显式声明 net:true 的用例生效。
//
// 抽成纯函数是为了可单测 —— 直接写在 TestBenchSemantics 的循环里,
// 分支就没有任何判据钉住, 等于「有处置臂但无人验证」(教训: 检测到 != 拦得住)。
func applyNetFlake(c semCase, ok bool, note, out string) (bool, string) {
	if ok || !c.net {
		return ok, note
	}
	if hit, pat := isNetFlake(out); hit {
		return true, "跳过(网络不可用, 特征 " + pat + "): " + note
	}
	return ok, note
}

// TestApplyNetFlakeSelfCheck 覆盖降级臂的全部分支。
// 四条: 该降的降 / 业务错误不降 / 非 net 用例不降 / 已通过不改写。
// 第 2、3 条是「不掩盖真缺陷」的防线, 缺了它们, 降级就成了宽口子。
func TestApplyNetFlakeSelfCheck(t *testing.T) {
	netCase := semCase{gate: "browser", name: "抓取", net: true}
	localCase := semCase{gate: "math", name: "整数优先级"}
	netOut := `{"ok":false,"error":"net::ERR_CONNECTION_CLOSED"}`
	bizOut := `{"ok":false,"error":"selector not found: input[name='q']"}`

	if ok, note := applyNetFlake(netCase, false, "期望成功, 实际: xx", netOut); !ok || !strings.Contains(note, "网络不可用") {
		t.Errorf("net 用例遇网络错误应降级并留痕, 得 ok=%v note=%q", ok, note)
	}
	if ok, _ := applyNetFlake(netCase, false, "x", bizOut); ok {
		t.Error("net 用例遇业务错误不得降级: 会掩盖真缺陷")
	}
	if ok, _ := applyNetFlake(localCase, false, "x", netOut); ok {
		t.Error("非 net 用例不得享受降级: 判定权必须窄")
	}
	if ok, note := applyNetFlake(netCase, true, "原备注", netOut); !ok || note != "原备注" {
		t.Errorf("已通过不应改写 note, 得 ok=%v note=%q", ok, note)
	}
}

// TestNetFlakeDetectorSelfCheck 是判据自身的判据 (变异自检)。
// 若 isNetFlake 恒返回 false, 上面的降级分支永不生效(等于没做);
// 若恒返回 true, 所有失败都被降级(比没做更危险 —— 真缺陷隐身)。
// 两个方向都必须钉住。
func TestNetFlakeDetectorSelfCheck(t *testing.T) {
	hits := []string{
		`{"ok":false,"error":"navigate failed: net::ERR_CONNECTION_CLOSED"}`,
		`Get "https://example.com": context deadline exceeded`,
		`proxyconnect tcp: dial tcp 127.0.0.1:1080: connectex: No connection could be made because the target machine actively refused it.`,
		`net::ERR_NAME_NOT_RESOLVED at https://example.com`,
		`read tcp: i/o timeout`,
		// T40: 实测漏网的形态 —— 加了形态表却不加向量 = 「改了但没判据」
		`{"ok":false,"error":"Page.goto: Timeout 30000ms exceeded.\nCall log:\n  - navigating to \"https://example.com/\", waiting until \"load\""}`,
		`{"ok":false,"error":"Timeout 30000ms exceeded"}`,
		`dial tcp 203.0.113.7:443: connection timed out`,
		// T41: knowledge gate 外层 30s 超时的真实文本 (forge_gate_host.go 归一为 ctx.Err())
		`{"ok":false,"error":"knowledge_gate execution failed: context deadline exceeded","timeout":true}`,
	}
	for _, s := range hits {
		if ok, _ := isNetFlake(s); !ok {
			t.Errorf("应判为网络不可用却未命中: %s", s)
		}
	}
	miss := []string{
		`{"ok":false,"error":"step2 failed: selector not found: input[name='q']"}`,
		`{"ok":false,"error":"bad json: unexpected end of JSON input"}`,
		`{"ok":false,"error":"未知 action: zzz"}`,
		`{"ok":false,"error":"拒绝: 单次成本超上限"}`,
		// T40 放宽的代价防线: "exceeded" / 超时词不得被单独当作网络特征 ——
		// 否则成本护栏、本地 gate 超时都会被降级, 真缺陷隐身。
		`{"ok":false,"error":"single cost limit exceeded"}`,
		`{"ok":false,"error":"用例超时: gate 执行 30s 未返回"}`,
		`{"ok":true,"stdout":"Example Domain"}`,
		``,
	}
	for _, s := range miss {
		if ok, p := isNetFlake(s); ok {
			t.Errorf("业务/代码错误不得判为网络抖动 (命中特征 %q): %s", p, s)
		}
	}
}

// ── T41 (20261010): 外网依赖 gate 的用例必须声明 net:true ─────────────
//
// 动机 (实测): full 档跑 TestBenchSemantics 时 knowledge 用例 30146ms 撞上
// knowledge 的 ExecTimeout=30s (forge_lang.go) → 判 FAIL; 而同一份代码单独跑
// 只需 2.4s。同一类网络抖动, browser 被降级、knowledge 判 FAIL —— 差异不在
// 代码质量, 而在「用例有没有声明 net:true」。
//
// 根因不是「knowledge 慢」, 是判据自身不一致: 20261003 引入 net 机制时只观测到
// browser 抖动, 就把 browser 标了 net, knowledge 只标了 slow。教训同 T40 ——
// 「我见过的那一种」被当成了「全部」。
//
// externalNetGates 已知依赖真实外网的 gate (来源: 各自实现里写死的端点/目标):
//
//	browser   -> 抓取任意外部 URL (经代理)
//	knowledge -> knowledge_gate.go 的 endpoints{wikidata,dbpedia} SPARQL (经代理)
//
// 新增外网 gate 时必须同批登记此处, 否则本判据覆盖不到 (清单本身也要有反向断言)。
var externalNetGates = []string{"browser", "knowledge"}

// TestExternalGateCasesDeclareNet 双向断言:
//
//	正向 — 清单里的 gate 的每个用例都必须 net:true (漏标 = 网络抖动被判 FAIL);
//	反向 — 清单里的 gate 必须在 semCases() 里真实出现 (清单悬空 = 判据空转)。
//
// 只查单向会留下「清单写了但用例没了」或「用例加了但没进清单」两种盲区。
func TestExternalGateCasesDeclareNet(t *testing.T) {
	seen := map[string]int{}
	for _, c := range semCases() {
		seen[c.gate]++
		for _, g := range externalNetGates {
			if c.gate == g && !c.net {
				t.Errorf("[%s/%s] 依赖外网的用例必须声明 net:true — 否则网络抖动会被判 FAIL (T41)",
					c.gate, c.name)
			}
		}
	}
	for _, g := range externalNetGates {
		if seen[g] == 0 {
			t.Errorf("外网 gate 清单里的 %q 在 semCases() 中不存在 — 清单已悬空", g)
		}
	}
}
