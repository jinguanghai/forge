package main

import (
	"encoding/json"
	"fmt"
	"net"
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
	if !bingReachable() {
		t.Skipf("外网不可达(cn.bing.com:443 连不通), 跳过真实网络集成测试")
	}
	cfg := &Config{MaxConcurrent: 4, CacheMaxSize: 100, RetryMax: 0}
	f := NewForge("D:\\forge", cfg)
	defer f.Shutdown()

	// 批处理: 必应搜索->提取->截图->关闭 (真实网络)
	steps := []map[string]interface{}{
		{"action": "navigate", "url": "https://cn.bing.com", "timeout": 30000, "wait_until": "commit"},
		{"action": "type", "selector": "input[name='q']", "value": "铸剑炉 智能体", "enter": true, "wait_ms": 3000},
		{"action": "extract", "selector": "h2"},
		{"action": "screenshot", "path": "D:\\forge\\.forge-temp\\browser_test.png", "full": false},
		{"action": "close"},
	}
	req, _ := json.Marshal(map[string]interface{}{"headless": true, "steps": steps, "nonce": time.Now().UnixNano()})
	_, res, err := f.Build(string(req), "browser", "")
	if err != nil || res == nil || !res.OK {
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
			t.Errorf("step%d failed: %v", i+1, m["error"])
		}
	}
	// 裸文本自动包装: URL -> navigate
	_, res2, err2 := f.Build("https://cn.bing.com", "browser", "")
	if err2 != nil || res2 == nil || !res2.OK {
		t.Fatalf("plain URL auto-wrap failed: %v %+v", err2, res2)
	}
	fmt.Printf("  裸URL自动包装 → %s\n", res2.Stdout[:150])
}
