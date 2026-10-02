package main

// net_hint_test.go — A3 出站网络特征埋点 (DSec 借鉴, 前向观测)
//
// 目的: 铸剑炉当前零网络策略, 但也没有数据说明风险面。先埋点, 有分布再决策,
// 避免凭想象造防御(白名单需驱动级拦截, 单机成本远超收益)。
// 本测试守两件事: 真特征必须命中(否则数据全空), 常见非网络代码必须不命中
// (误报会污染决策依据)。

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNetEgressHint_Positive(t *testing.T) {
	cases := []struct{ name, code, want string }{
		{"python socket", "import socket\ns = socket.socket()", "py-socket"},
		{"python from-import", "from socket import socket", "py-socket"},
		{"python requests", "import requests\nrequests.get('http://x')", "py-requests"},
		{"python urllib", "import urllib.request\nurllib.request.urlopen(u)", "py-urllib"},
		{"python http.client", "import http.client\nc = http.client.HTTPSConnection(h)", "py-http"},
		{"python httpx", "import httpx\nhttpx.get(u)", "py-http"},
		{"node require https", "const h = require('https')", "node-net"},
		{"node fetch", "const r = await fetch(url)", "node-net"},
		{"go net/http", "import \"net/http\"\nhttp.Get(u)", "go-net"},
		{"sh curl", "curl -s https://example.com", "sh-net"},
		{"sh wget", "wget -q http://x/y", "sh-net"},
	}
	for _, c := range cases {
		got := netEgressHint(c.code)
		if got == "" {
			t.Errorf("[%s] 未命中 — 埋点会漏掉真实网络调用", c.name)
			continue
		}
		if !containsCSV(got, c.want) {
			t.Errorf("[%s] 命中 %q, 期望含 %q", c.name, got, c.want)
		}
	}
}

func TestNetEgressHint_Negative(t *testing.T) {
	cases := []struct{ name, code string }{
		{"纯打印", "print('hello world')"},
		{"列表", "x = [1, 2, 3]\nprint(sum(x))"},
		{"变量名 requests", "requests = 5\nprint(requests)"},
		{"函数名含 fetch", "def fetchData(u):\n    return u\nfetchData(1)"},
		{"文件读写", "open('a.txt','w').write('x')"},
		{"正则模块", "import re\nprint(re.findall(r'\\d+', 'a1b2'))"},
		{"数学计算", "print(2**10)"},
	}
	for _, c := range cases {
		if got := netEgressHint(c.code); got != "" {
			t.Errorf("[%s] 误报 %q — 会污染风险面统计", c.name, got)
		}
	}
}

// containsCSV 精确 token 匹配: "py-http" 不得被 "py-https" 命中。
func containsCSV(csv, want string) bool {
	for _, p := range strings.Split(csv, ",") {
		if p == want {
			return true
		}
	}
	return false
}

// TestAuditGate_NewFields 落盘验证: 埋点字段必须真的写进 gate_audit.jsonl。
// 教训(best-effort 静默失败): 写入失败不可见, 所以断言「字段存在」而非「函数返回」。
func TestAuditGate_NewFields(t *testing.T) {
	f := newTestForge(t)
	defer f.Shutdown()
	path := auditFilePath(f.workDir)

	hit := ForgeGateResult{Lang: "python", OK: true, CachedAt: time.Now().Unix(),
		DroppedBytes: 4096, OutputTruncated: true}
	f.auditGate("python", false, false, time.Now(), 0, 10, 0, "py-requests", &hit)

	fresh := ForgeGateResult{Lang: "python", OK: true}
	f.auditGate("python", false, false, time.Now(), 0, 10, 0, "", &fresh)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("审计文件不可读: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("审计行数 = %d, 期望 >= 2", len(lines))
	}
	var e1, e2 map[string]interface{}
	if err := json.Unmarshal([]byte(lines[len(lines)-2]), &e1); err != nil {
		t.Fatalf("第 1 条解析失败: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &e2); err != nil {
		t.Fatalf("第 2 条解析失败: %v", err)
	}
	if e1["cache_hit"] != true {
		t.Errorf("cache_hit 未落盘: %v", e1)
	}
	if e1["net_hint"] != "py-requests" {
		t.Errorf("net_hint 未落盘: %v", e1)
	}
	if e1["dropped_bytes"] != float64(4096) {
		t.Errorf("dropped_bytes 未落盘: %v", e1)
	}
	for _, k := range []string{"cache_hit", "net_hint", "dropped_bytes"} {
		if _, ok := e2[k]; ok {
			t.Errorf("真跑/无网络/无超限时不应出现 %s: %v", k, e2)
		}
	}
}
