package main

import (
	"testing"
	"time"
)

// 测试固定为非高峰时段, 使 pickModel 行为与旧逻辑一致 (阈值 0.6)。
// 若实际运行恰逢高峰, 不注入则 auto/复杂任务可能被降档到 flash 而破坏用例。
func init() {
	peakHourNow = func() bool { return false }
}

func TestClassifyTask(t *testing.T) {
	cases := []struct {
		name    string
		task    string
		wantPro bool // 期望复杂度 >= 0.6 (路由到 pro)
	}{
		{"简单查询", "列出当前目录的文件", false},
		{"简单算术", "计算 1+1 等于多少", false},
		{"简单文本", "把这句话翻译成英文", false},
		{"简单统计", "统计这个文件夹里有多少个txt文件", false},
		{"并发服务器", "写一个并发安全的 TCP 服务器，支持连接池和心跳", true},
		{"编译器", "实现一个 JSON 解析器，要求词法分析和语法分析", true},
		{"安全", "分析这段代码的 SQL 注入漏洞并修复", true},
		{"分布式", "设计一个分布式一致性方案，处理网络分区和容错", true},
		{"性能", "对这段代码做性能优化，消除内存泄漏", true},
		{"加密", "实现 TLS 证书验证和加密通信", true},
		{"架构", "重构这个多模块项目，应用设计模式", true},
		{"数据库", "设计数据库引擎的事务隔离级别和索引", true},
		{"内核驱动", "写一个内核驱动，处理系统调用和内存管理", true},
		{"底层协议", "实现 WebSocket 协议栈和序列化", true},
	}
	for _, c := range cases {
		score := classifyTask(c.task)
		got := score >= 0.6
		if got != c.wantPro {
			t.Errorf("[%s] task=%q score=%.2f wantPro=%v got=%v", c.name, c.task, score, c.wantPro, got)
		} else {
			t.Logf("[%s] score=%.2f -> %v", c.name, score, got)
		}
	}
}

func TestPickModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ModelFlash = "deepseek-flash"
	cfg.ModelPro = "deepseek-v4-pro"
	cfg.Model = "deepseek-v4-pro"

	// auto 模式
	cfg.RouterMode = RouterAuto
	if m := pickModel(cfg, "列出当前目录的文件"); m != cfg.ModelFlash {
		t.Errorf("auto/简单任务: 应选 flash, got %s", m)
	}
	if m := pickModel(cfg, "写一个并发安全的 TCP 服务器"); m != cfg.ModelPro {
		t.Errorf("auto/复杂任务: 应选 pro, got %s", m)
	}

	// 强制模式
	cfg.RouterMode = RouterFlash
	if m := pickModel(cfg, "写一个并发安全的 TCP 服务器"); m != cfg.ModelFlash {
		t.Errorf("flash 强制: got %s", m)
	}
	cfg.RouterMode = RouterPro
	if m := pickModel(cfg, "列出当前目录的文件"); m != cfg.ModelPro {
		t.Errorf("pro 强制: got %s", m)
	}

	// fixed 模式 (兼容旧行为)
	cfg.RouterMode = RouterFixed
	if m := pickModel(cfg, "任意任务"); m != cfg.Model {
		t.Errorf("fixed: 应返回 cfg.Model, got %s", m)
	}

	// 空模式 → auto
	cfg.RouterMode = ""
	if m := pickModel(cfg, "列出当前目录的文件"); m != cfg.ModelFlash {
		t.Errorf("空模式应视为 auto, got %s", m)
	}
}

func TestMiniMaxWindow(t *testing.T) {
	cases := []struct {
		name string
		t    time.Time
		want bool
	}{
		{"周一10点高峰", time.Date(2026, 8, 31, 10, 0, 0, 0, time.Local), true},
		{"周六10点周末", time.Date(2026, 8, 29, 10, 0, 0, 0, time.Local), false},
		{"周日15点周末", time.Date(2026, 8, 30, 15, 0, 0, 0, time.Local), false},
		{"周三13点非高峰", time.Date(2026, 8, 26, 13, 0, 0, 0, time.Local), false},
		{"周三15点高峰", time.Date(2026, 8, 26, 15, 0, 0, 0, time.Local), true},
		{"周五18点整(开区间不含)", time.Date(2026, 8, 28, 18, 0, 0, 0, time.Local), false},
		{"周五17点高峰", time.Date(2026, 8, 28, 17, 0, 0, 0, time.Local), true},
	}
	for _, c := range cases {
		if got := isMiniMaxWindow(c.t); got != c.want {
			t.Errorf("[%s] %v -> want %v got %v", c.name, c.t, c.want, got)
		}
	}
}

func TestRouteEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIKey = "ds-key"
	cfg.BaseURL = "https://api.deepseek.com/v1"
	cfg.Model = "deepseek-flash"

	old := minimaxWindowNow
	defer func() { minimaxWindowNow = old }()

	// MiniMax 未配 → 始终 DeepSeek
	minimaxWindowNow = func() bool { return true }
	if ep := routeEndpoint(cfg); ep.Provider != EndpointDeepSeek {
		t.Errorf("MiniMax 未配, 应 DeepSeek, got %s", ep.Provider)
	}

	// MiniMax 配好 + 窗口 → MiniMax
	cfg.MiniMaxAPIKey = "mm-key"
	cfg.MiniMaxBaseURL = "https://api.minimaxi.com/v1"
	cfg.MiniMaxModel = "MiniMax-M3"
	ep := routeEndpoint(cfg)
	if ep.Provider != EndpointMiniMax {
		t.Errorf("窗口且已配, 应 MiniMax, got %s", ep.Provider)
	}
	if ep.Model != "MiniMax-M3" || ep.APIKey != "mm-key" {
		t.Errorf("MiniMax 端点字段错: %+v", ep)
	}

	// 非窗口 → 回 DeepSeek
	minimaxWindowNow = func() bool { return false }
	ep = routeEndpoint(cfg)
	if ep.Provider != EndpointDeepSeek {
		t.Errorf("非窗口, 应 DeepSeek, got %s", ep.Provider)
	}
	if ep.Model != "deepseek-flash" {
		t.Errorf("DeepSeek model 错: %s", ep.Model)
	}
}
