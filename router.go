package main

import (
	"strings"
	"unicode/utf8"
)

// ─── 动态模型路由 (Model Router) ─────────────────────────────
// 简单任务 → flash (便宜)，复杂任务 → pro (强)。
// 模式: auto(按复杂度自动) / flash(强制flash) / pro(强制pro) / fixed(固定Model,兼容旧行为)

const (
	RouterAuto  = "auto"
	RouterFlash = "flash"
	RouterPro   = "pro"
	RouterFixed = "fixed"
)

// classifyTask 返回任务复杂度 0.0~1.0。
// 强信号每个 +0.45，弱信号每个 +0.12，长度 +0.08~0.15，结构信号每个 +0.08。
func classifyTask(text string) float64 {
	lower := strings.ToLower(text)
	score := 0.0

	strong := []string{
		// 编译器/解析器
		"编译器", "解析器", "解释器", "词法", "语法分析", "ast",
		// 并发/线程
		"并发安全", "竞态", "死锁", "goroutine", "多线程", "线程安全", "线程池", "互斥锁", "mutex", "信号量", "原子操作", "waitgroup",
		// 分布式
		"分布式", "一致性", "共识", "微服务", "集群", "容错",
		// 安全/密码
		"加密", "解密", "密码学", "签名算法", "证书", "tls", "安全漏洞", "漏洞", "注入", "溢出", "鉴权",
		// 底层
		"内核", "驱动", "汇编", "系统调用", "内存管理", "页表", "虚拟机", "沙箱",
		// 协议
		"协议栈", "ssl", "quic", "grpc", "websocket",
		// 性能
		"性能优化", "性能", "内存泄漏", "高并发", "高可用", "基准测试",
		// 架构
		"架构设计", "设计模式", "多模块", "重构",
		// 数据引擎
		"数据库引擎", "事务隔离", "搜索引擎", "倒排索引",
		// 序列化
		"序列化", "反序列化", "字节码", "编解码",
	}
	for _, s := range strong {
		if strings.Contains(lower, s) {
			score += 0.45
		}
	}

	weak := []string{
		"网络", "tcp", "udp", "http", "socket", "并发", "并行", "异步",
		"缓存", "内存", "数据库", "sql", "索引", "事务", "正则", "匹配",
		"文件系统", "多进程", "连接池", "队列", "流式",
		"协议", "框架", "引擎", "测试", "二进制", "指针", "泛型", "反射",
		"计数器", "go 代码", "golang", "rust", "c++", "c 代码", "python 代码", "代码实现",
		"排序", "递归", "搜索", "哈希", "链表", "二叉树", "图算法", "动态规划", "算法", "数据结构",
	}
	for _, s := range weak {
		if strings.Contains(lower, s) {
			score += 0.12
		}
	}

	// 任务长度信号
	if n := utf8.RuneCountInString(text); n > 300 {
		score += 0.15
	} else if n > 120 {
		score += 0.08
	}

	// 结构信号: 项目级/多步骤
	multi := []string{"项目", "系统", "模块", "多个文件", "第一步", "然后", "实现一个", "从零", "完整"}
	for _, s := range multi {
		if strings.Contains(lower, s) {
			score += 0.08
		}
	}

	if score > 1.0 {
		score = 1.0
	}
	return score
}

// pickModel 根据路由模式为当前任务选择模型。
func pickModel(cfg *Config, task string) string {
	mode := cfg.RouterMode
	if mode == "" {
		mode = RouterAuto
	}
	switch mode {
	case RouterFlash:
		return cfg.ModelFlash
	case RouterPro:
		return cfg.ModelPro
	case RouterFixed:
		return cfg.Model
	default: // auto
		if classifyTask(task) >= 0.6 {
			return cfg.ModelPro
		}
		return cfg.ModelFlash
	}
}

// routerModeShort 返回路由模式的短标签（欢迎画面用，避免超宽破坏边框）。
func routerModeShort(mode string) string {
	switch mode {
	case RouterFlash:
		return "强制 flash"
	case RouterPro:
		return "强制 pro"
	case RouterFixed:
		return "固定模型"
	default:
		return "自动路由"
	}
}

// routerModeLabel 返回路由模式的中文说明。
func routerModeLabel(mode string) string {
	switch mode {
	case RouterFlash:
		return "强制 flash"
	case RouterPro:
		return "强制 pro"
	case RouterFixed:
		return "固定模型"
	default:
		return "自动路由 (简单→flash / 复杂→pro)"
	}
}

// effortLabel 返回当前思考强度档位 (V4-Pro: low/high/max, 空=官方默认)
func effortLabel(cfg *Config) string {
	if cfg.ReasoningEffort != "" {
		return cfg.ReasoningEffort
	}
	return "默认"
}
