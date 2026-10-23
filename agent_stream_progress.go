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

	// maxTurnMessages 单轮内 messages 的条数安全网, 超过则裁剪最早的完整 tool 轮。
	//
	// 阈值沿革: 60 → 300 (缓存修复 20260826) → 1200 (20261013 断裂实测)。
	// 300 仍会触发: 10-13 一个 turn 内 168 次请求, 每轮追加 assistant+tool 两条,
	// 初始 84 条 + 336 = 420 > 300 → 自第 108 轮起每轮都裁 → 每轮断链。
	// 实测该日 54 次全量重算 (单次 miss > 10 万, 与正常追加的 miss 形成双峰,
	// 中间 [1万,10万) 区间为空), 吃掉 986 万 miss tokens = 当日 miss 的 93%,
	// 折 9.86 元 (当日总成本 13.32 元)。全期同判据: 227 次 / 4403 万 tokens。
	// 结论: 命中价只有 miss 的 1/50, 长上下文远比断链便宜 —— 阈值宁高勿低。
	maxTurnMessages = 1200

	// maxTurnTokens 单轮内 messages 的 token 预算 (硬窗口 1M, 留余量后取 50 万)。
	//
	// 为什么条数之外还要 token: 条数与成本脱钩 —— 一条 tool 消息可能 50 token
	// (短回执), 也可能 5 万 (大文件内容)。只按条数判会放过"少数条巨消息"。
	maxTurnTokens = 500_000

	// trimTurnLowWater 裁剪目标: 一次裁到低水位, 而非刚好压回阈值。
	//
	// 本条是本文件最贵的一行经验: 裁剪必然使 messages 前缀在固定头之后断裂,
	// 下一次请求全量 miss (成本 ×50)。"每轮裁一点" = 每轮断一次;
	// "一次裁够" = 断一次, 之后 N 轮重新命中。同样的删除总量, 成本差 N 倍。
	trimTurnLowWater = 200
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

// trimTurnMessagesIfNeeded 超出条数安全网或 token 预算时裁剪最早的完整 tool 轮。
//
// 判据两路并行 (条数 + token): 条数防"极多条小消息", token 防"少数条巨消息"。
// 触发后一次裁到 trimTurnLowWater, 而非刚好压回阈值 —— 理由见该常量注释
// (断链成本是命中价的 50 倍, 裁一次比裁 N 次便宜 N 倍)。
// 未超限时原样返回同一底层数组, 不复制 (复制本身不影响缓存, 但无谓开销)。
//
// B3 批4: 阈值判定自 RunStream 抽出, 与常量就近放置。
func trimTurnMessagesIfNeeded(msgs []ChatMessage) []ChatMessage {
	if len(msgs) <= maxTurnMessages && estimateTokens(msgs) <= maxTurnTokens {
		return msgs
	}
	target := trimTurnLowWater
	if est := estimateTokens(msgs); est > maxTurnTokens {
		// token 超预算: 按线性比例把目标条数压到半预算以内 (至少留 1 条固定头)。
		target = len(msgs) * (maxTurnTokens / 2) / est
		if target < 1 {
			target = 1
		}
	}
	if target > trimTurnLowWater {
		target = trimTurnLowWater
	}
	out := trimTurnMessages(msgs, target)
	// 线性近似在"消息大小悬殊"时会低估, 兜底循环继续删到预算内。
	// 触发概率低 (token 超预算本身罕见), 故不优化为前缀和。
	for estimateTokens(out) > maxTurnTokens && len(out) > 1 {
		next := trimTurnMessages(out, len(out)-1)
		if len(next) >= len(out) {
			break // 已无可删的完整 tool 轮
		}
		out = next
	}
	return out
}
