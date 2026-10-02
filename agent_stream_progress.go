package main

// ─── 无进展检测 (Detector B) ─────────────────────────────────────
// B3 批4: 自 RunStream 抽出 (原 agent.go:768-803, 含 trim 阈值兜底)。
//
// 检测语义: 不同代码但结果不变 = 无进展。单看调用参数(Detector A)抓不到这类
// 循环 —— 模型每轮换一种写法重算同一个死胡同, 参数哈希次次不同, 但输出哈希
// 恒定。因此这里按"本回合最后一条原始输出"的归一化哈希判连续。
//
// 拦截 ≠ 失败中止: 命中阈值只做两件事 —— 提示换策略 + 升级模型打破僵局, 任务
// 继续; 连续 MaxLoopStrikes 次拦截仍无进展, 才带已取得进展自动收尾(不无限转)。
//
// 指针契约 (同 toolLoop / streamTurn / turnFinalizer): loopStrikes 在块外仍被
// 引用(finishStuck 收尾日志 + toolLoop 重复调用拦截), 故走指针, 使"回写遗漏"
// 在类型层面不可能发生; prevOutHash / sameOutRun 块外零引用, 归本结构体所有。

import (
	"fmt"
	"os"
)

const (
	// sameOutputThreshold 连续 N 次输出完全相同 → 判定无进展。
	// B3 批4: 原为 RunStream 内局部 const, 块外无引用, 随检测逻辑一并迁出。
	sameOutputThreshold = 4

	// maxTurnMessages 单轮内 messages 的长度上限, 超过则裁剪最早的完整 tool 轮。
	//
	// 阈值从 60 提到 300 (缓存修复 20260826)。旧阈值 60 在工具密集任务(单轮多次
	// forge 调用)中极易触发, 而 trimTurnMessages 从头部删最早 tool 轮 → 固定头
	// 之后的前缀断裂 → 后续所有 API 全量 miss (实证: cache hit 恒等于固定头长度)。
	// 单轮内 tool 轮几乎不可能超 300 次, 提高阈值从根本上避免触发。
	// 真正超长的历史由 trimHistory(尾部删, 缓存友好)与 maybeCompact 兜底。
	maxTurnMessages = 300
)

// progressTracker 承载"连续相同输出"无进展检测的全部状态。
type progressTracker struct {
	// ── 只读输入 ──
	maxLoopStrikes int
	escalateOnLoop func() // 拦截时升级模型(flash→pro)打破僵局, 由 RunStream 注入

	// ── 与 RunStream 共享的可变状态(指针, 防回写遗漏) ──
	loopStrikes   *int    // 跨轮累计的拦截次数(成功不清零, 与 consecutiveFails 不同)
	lastRawOutput *string // 本回合最后一次执行的原始输出, 由 toolLoop 每轮写入

	// ── 本检测器私有状态(块外无引用, 无需回写) ──
	prevOutHash string // 上一回合输出的归一化哈希
	sameOutRun  int    // 当前连续相同输出的轮数
}

// check 检测"连续相同输出"型无进展。
//
// 返回 (intervene, stuck):
//   - stuck=true      → 已达拦截上限, 调用方应 return finishStuck() 收尾;
//   - intervene != "" → 应把该文本作为 user 消息注入 messages, 然后继续循环;
//   - 两者皆空/假     → 有进展, 无需干预。
//
// stuck 与 intervene 互斥: 达上限即收尾, 不再注入干预指令。
func (p *progressTracker) check() (string, bool) {
	if *p.lastRawOutput == "" {
		return "", false
	}
	outHash := hashOutput(*p.lastRawOutput)
	if outHash == p.prevOutHash {
		p.sameOutRun++
	} else {
		p.sameOutRun = 1
		p.prevOutHash = outHash
	}
	if p.sameOutRun < sameOutputThreshold {
		return "", false
	}

	p.sameOutRun = 0 // 拦截后重新累计
	(*p.loopStrikes)++
	p.escalateOnLoop()
	fmt.Fprintf(os.Stderr, "  %s %s\n", color(ansi.yellow, "⚠"),
		dim(fmt.Sprintf("无进展拦截 %d/%d (输出连续%d次无变化), 已要求换策略",
			*p.loopStrikes, p.maxLoopStrikes, sameOutputThreshold)))
	if *p.loopStrikes >= p.maxLoopStrikes {
		return "", true
	}
	// 干预指令以 user 消息注入 messages, 下一轮模型必须回应(换策略或总结)
	return stuckInterventionMsg(*p.loopStrikes), false
}

// trimTurnMessagesIfNeeded 超过 maxTurnMessages 时裁剪最早的完整 tool 轮。
// B3 批4: 阈值判定自 RunStream 抽出, 与常量 maxTurnMessages 就近放置。
func trimTurnMessagesIfNeeded(msgs []ChatMessage) []ChatMessage {
	if len(msgs) > maxTurnMessages {
		return trimTurnMessages(msgs, maxTurnMessages)
	}
	return msgs
}
