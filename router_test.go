package main

import "testing"

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
	cfg.ModelFlash = "deepseek-v4-flash"
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
