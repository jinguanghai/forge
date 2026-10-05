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
		`{"ok":true,"stdout":"Example Domain"}`,
		``,
	}
	for _, s := range miss {
		if ok, p := isNetFlake(s); ok {
			t.Errorf("业务/代码错误不得判为网络抖动 (命中特征 %q): %s", p, s)
		}
	}
}
