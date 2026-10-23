package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bingReachable() bool {
	conn, err := net.DialTimeout("tcp", "cn.bing.com:443", 5*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func TestBrowserGateIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实网络集成测试")
	}
	if !bingReachable() {
		t.Skipf("外网不可达(cn.bing.com:443 连不通), 跳过真实网络集成测试")
	}
	cfg := &Config{MaxConcurrent: 4, CacheMaxSize: 100, RetryMax: 0}
	root := testRoot(t)
	f := NewForge(root, cfg)
	defer f.Shutdown()

	// 批处理: 必应搜索->提取->截图->关闭 (真实网络)
	steps := []map[string]interface{}{
		{"action": "navigate", "url": "https://cn.bing.com", "timeout": 30000, "wait_until": "commit"},
		{"action": "type", "selector": "input[name='q']", "value": "铸剑炉 智能体", "enter": true, "wait_ms": 3000},
		{"action": "extract", "selector": "h2"},
		{"action": "screenshot", "path": filepath.Join(root, ".forge-temp", "browser_test.png"), "full": false},
		{"action": "close"},
	}
	req, _ := json.Marshal(map[string]interface{}{"headless": true, "steps": steps, "nonce": time.Now().UnixNano()})
	_, res, err := f.Build(string(req), "browser", "")
	if err != nil || res == nil || !res.OK {
		// 网络层不可用(代理/链路抖动)与代码质量无关: 降级为跳过并留痕, 不判 FAIL。
		// 见 net_flake_test.go —— 只认网络特征, 业务错误照旧 FAIL。
		if hit, pat := isNetFlake(gateOutText(res, err)); hit {
			t.Skipf("网络不可用(特征 %s): err=%v res=%+v", pat, err, res)
		}
		t.Fatalf("browser steps failed: err=%v res=%+v", err, res)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	results, _ := out["result"].([]interface{})
	fmt.Printf("  browser批处理步数=%d 总耗时=%dms\n", len(results), res.Duration)
	for i, st := range results {
		m, _ := st.(map[string]interface{})
		b, _ := json.Marshal(m)
		fmt.Printf("    step%d=%s\n", i+1, string(b))
	}
	if len(results) < 5 {
		t.Fatalf("want >=5 steps, got %d", len(results))
	}
	for i, st := range results {
		m := st.(map[string]interface{})
		ok, _ := m["ok"].(bool)
		fmt.Printf("    step%d: ok=%v\n", i+1, ok)
		if !ok {
			// 单步网络失败同属链路抖动, 不判代码缺陷 (但必须留下原始错误)。
			if hit, pat := isNetFlake(fmt.Sprintf("%v", m["error"])); hit {
				t.Skipf("step%d 网络不可用(特征 %s): %v", i+1, pat, m["error"])
			}
			t.Errorf("step%d failed: %v", i+1, m["error"])
		}
	}
	// 裸文本自动包装: URL -> navigate
	_, res2, err2 := f.Build("https://cn.bing.com", "browser", "")
	if err2 != nil || res2 == nil || !res2.OK {
		if hit, pat := isNetFlake(gateOutText(res2, err2)); hit {
			t.Skipf("网络不可用(特征 %s): %v %+v", pat, err2, res2)
		}
		t.Fatalf("plain URL auto-wrap failed: %v %+v", err2, res2)
	}
	fmt.Printf("  裸URL自动包装 → %s\n", res2.Stdout[:150])
}

// dsKeyConfigured 判断 DeepSeek API Key 是否可得 (环境变量优先, 回落仓库根 .env)。
// 无 key 的机器(发布环境/新克隆)应跳过而非 FAIL: 缺凭据是环境差异, 不是代码缺陷。
func dsKeyConfigured() bool {
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) != "" {
		return true
	}
	b, err := os.ReadFile(".env")
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "DEEPSEEK_API_KEY=")
}

// TestBrowserGateFetchSearch 钉住 DeepSeek Fetch/Search 快路径 (20261018 上线)。
//
// 为什么必须存在这条哨兵: 实测(20261018 A/B, 同一批 URL) Playwright 抓正文 0/5 有效
// —— 政府站 ERR_CONNECTION_RESET / 百度百科撞滑块验证 / GitHub 只拿到 "Skip to content";
// 同一批 URL 走 Fetch API 4/5 有效且 0.2-0.4s。该能力是 browser gate 的主力取正文路径,
// 若无人钉住, 下次重构删掉 `fetch` 分支不会有任何红灯, 只会在真实抓取时才暴露。
func TestBrowserGateFetchSearch(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实网络集成测试")
	}
	if !dsKeyConfigured() {
		t.Skip("未配置 DEEPSEEK_API_KEY, 跳过 Fetch/Search 集成测试")
	}
	cfg := &Config{MaxConcurrent: 4, CacheMaxSize: 100, RetryMax: 0}
	root := testRoot(t)
	f := NewForge(root, cfg)
	defer f.Shutdown()

	// ① fetch: 取网页正文 Markdown, 不应拉起浏览器(耗时远低于 Playwright)
	req, _ := json.Marshal(map[string]interface{}{
		"action": "fetch", "url": "https://api-docs.deepseek.com/quick_start/pricing",
	})
	_, res, err := f.Build(string(req), "browser", "")
	if err != nil || res == nil || !res.OK {
		if hit, pat := isNetFlake(gateOutText(res, err)); hit {
			t.Skipf("网络不可用(特征 %s): err=%v res=%+v", pat, err, res)
		}
		t.Fatalf("fetch 动作失败: err=%v res=%+v", err, res)
	}
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			Title  string `json:"title"`
			Body   string `json:"body"`
			Source string `json:"source"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &out); err != nil {
		t.Fatalf("fetch 返回非法 JSON: %v :: %s", err, res.Stdout)
	}
	if !out.OK {
		t.Fatalf("fetch ok=false: %s", res.Stdout)
	}
	if out.Result.Source != "deepseek_fetch" {
		t.Errorf("source 应为 deepseek_fetch, 实际 %q (回落链被打断?)", out.Result.Source)
	}
	if len(out.Result.Body) < 500 {
		t.Errorf("正文过短(%d 字), Fetch 路径疑似失效: %q", len(out.Result.Body), out.Result.Title)
	}
	fmt.Printf("  fetch: %q body=%d字 耗时=%dms\n", out.Result.Title, len(out.Result.Body), res.Duration)

	// ② search: 结构化结果必须带 url 与 authority 权威度
	req2, _ := json.Marshal(map[string]interface{}{
		"action": "search", "query": "DeepSeek V4 release", "count": 3,
	})
	_, res2, err2 := f.Build(string(req2), "browser", "")
	if err2 != nil || res2 == nil || !res2.OK {
		if hit, pat := isNetFlake(gateOutText(res2, err2)); hit {
			t.Skipf("网络不可用(特征 %s): err=%v res=%+v", pat, err2, res2)
		}
		t.Fatalf("search 动作失败: err=%v res=%+v", err2, res2)
	}
	var sres struct {
		OK     bool                     `json:"ok"`
		Result []map[string]interface{} `json:"result"`
	}
	if err := json.Unmarshal([]byte(res2.Stdout), &sres); err != nil {
		t.Fatalf("search 返回非法 JSON: %v :: %s", err, res2.Stdout)
	}
	if !sres.OK || len(sres.Result) == 0 {
		t.Fatalf("search 无结果: %s", res2.Stdout)
	}
	for i, r := range sres.Result {
		if u, _ := r["url"].(string); !strings.HasPrefix(u, "http") {
			t.Errorf("结果%d 缺 url: %v", i, r)
		}
		if _, ok := r["authority"]; !ok {
			t.Errorf("结果%d 缺 authority 权威度字段: %v", i, r)
		}
	}
	fmt.Printf("  search: %d 条结果, 首条=%v\n", len(sres.Result), sres.Result[0]["url"])
}
