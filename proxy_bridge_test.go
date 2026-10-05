package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestProxyBridgeWiring 钉住 HTTP 代理桥的接线 (20261004)
//
// 背景: HTTP_PROXY=socks5h://... 只有 requests/curl/Go 认; Python 标准库 urllib
// 把该值当 HTTP 代理用, 直接把 CONNECT 发给 SOCKS 服务器 -> 0.02s 秒败。
// 修法 = 本地起一个标准 HTTP 代理(1081), 上游走 socks5(1080), 全客户端兼容。
// browser_gate.py 早已写好 1081 探测与回退逻辑, 但桥本身从未实现 —— 一个只被
// 探测、无人实现的端口, 四套守卫全绿也发现不了。
//
// 本判据把三处端口钉成一处: 桥(proxy_bridge.py) / 守护(proxy_guard.py) /
// 浏览器gate(browser_gate.py)。任何一处漂移或守护不再拉起桥, 立刻报红。
func TestProxyBridgeWiring(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(".", rel))
		if err != nil {
			t.Fatalf("读 %s 失败: %v", rel, err)
		}
		return string(b)
	}
	bridge := read(filepath.Join("defense_system", "proxy_bridge.py"))
	guard := read(filepath.Join("defense_system", "proxy_guard.py"))
	browser := read(filepath.Join(".forge", "forge-tools", "browser_gate.py"))

	// 1) 桥自身: 监听端口 + 上游 socks 端口
	reListen := regexp.MustCompile(`LISTEN_PORT\s*=\s*(\d+)`)
	ml := reListen.FindStringSubmatch(bridge)
	if ml == nil {
		t.Fatal("proxy_bridge.py 缺少 LISTEN_PORT 定义")
	}
	if _, err := strconv.Atoi(ml[1]); err != nil {
		t.Fatalf("LISTEN_PORT 非数字: %s", ml[1])
	}
	reSocks := regexp.MustCompile(`SOCKS_PORT\s*=\s*(\d+)`)
	ms := reSocks.FindStringSubmatch(bridge)
	if ms == nil {
		t.Fatal("proxy_bridge.py 缺少 SOCKS_PORT 定义")
	}
	if ms[1] != "1080" {
		t.Fatalf("桥上游 socks 端口 = %s, 期望 1080", ms[1])
	}

	// 2) 守护必须定义并真的调用 (备而未用 = 没有)
	for _, decl := range []string{"def ensure_bridge(", "def ensure_proxy_env(", "def _backup_proxy_env("} {
		if !strings.Contains(guard, decl) {
			t.Errorf("proxy_guard.py 缺少定义 %s", decl)
		}
	}
	i := strings.Index(guard, "def main():")
	if i < 0 {
		t.Fatal("proxy_guard.py 缺少 main()")
	}
	mainBody := guard[i:]
	for _, call := range []string{"ensure_bridge()", "ensure_proxy_env()"} {
		if !strings.Contains(mainBody, call) {
			t.Errorf("proxy_guard.py main() 未调用 %s —— 桥/环境变量校正备而未用", call)
		}
	}

	// 3) 守护的端口与代理指向必须与桥一致
	reBp := regexp.MustCompile(`BRIDGE_PORT\s*=\s*(\d+)`)
	mb := reBp.FindStringSubmatch(guard)
	if mb == nil {
		t.Fatal("proxy_guard.py 缺少 BRIDGE_PORT 定义")
	}
	if mb[1] != ml[1] {
		t.Fatalf("守护 BRIDGE_PORT=%s 与桥 LISTEN_PORT=%s 不一致", mb[1], ml[1])
	}
	wantHTTP := "http://127.0.0.1:" + ml[1]
	if !strings.Contains(guard, wantHTTP) {
		t.Errorf("proxy_guard.py 的 PROXY_HTTP 未指向 %s", wantHTTP)
	}
	if !strings.Contains(guard, "socks5h://127.0.0.1:1080") {
		t.Error("proxy_guard.py 缺少 socks5h 回退值 (桥不在时应优雅降级)")
	}

	// 4) browser_gate 探测的端口必须与桥一致 (防漂移)
	reProbe := regexp.MustCompile(`create_connection\(\("127\.0\.0\.1",\s*(\d+)\)`)
	mpr := reProbe.FindStringSubmatch(browser)
	if mpr == nil {
		t.Fatal("browser_gate.py 缺少 127.0.0.1 端口探测")
	}
	if mpr[1] != ml[1] {
		t.Fatalf("browser_gate 探测端口=%s 与桥 LISTEN_PORT=%s 不一致", mpr[1], ml[1])
	}
}
