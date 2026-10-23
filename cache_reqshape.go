package main

// ─── 请求形状埋点: 前缀断裂定位 ─────────────────────────────
// 动机 (20261013): 10-13 的 cache_stats.jsonl 出现「高命中(23万) 与 全断
// (仅固定头) 交替」的形态, 但每行只有 hit/miss/out, 无法回答三个问题:
// ①是哪个进程 ②断裂落在第几条消息 ③是裁剪导致还是内容变化导致。
// 旧诊断只能靠「hit 恰等于固定头长度」反推, 属推测, 不是证据。
//
// 本埋点把「本次请求与上一次请求的公共前缀条数 (LCP)」直接落盘:
//
//	LCP == Prev → 纯追加, 前缀未动 (正常, 缓存应命中)
//	LCP <  Prev → 前缀在第 LCP 条处断裂 (断链点被精确定位)
//	LCP == -1   → 本进程首次请求 (无基线)
//
// 加上 PID, 并发实例的交错写入可被分组还原。
//
// 判定权在死程序: 分析脚本读 cache_stats.jsonl 出结论, 不需人工推理。
// 纯本地计算, 不改请求体, 不影响缓存。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
)

// reqShape 一次请求的形状快照: preflightStream 写入, recordCacheStat 读出落盘。
type reqShape struct {
	PID   int    // 进程 ID (区分并发实例)
	Msgs  int    // messages 条数
	Chars int    // 全部 content 的字符数 (rune)
	Est   int    // estimateTokens 估算值
	LCP   int    // 与上一次请求的公共前缀条数; -1 = 首次
	Prev  int    // 上一次请求的条数; -1 = 首次
	Pfx   string // 前 4 条指纹, 用于判断固定头本身是否被改
}

var (
	reqShapeMu  sync.Mutex
	curReqShape = reqShape{LCP: -1, Prev: -1}
	lastMsgFps  []string
)

// msgFingerprint 单条消息的缓存指纹。
// 用 JSON 序列化 (与请求体同源) 而非只取 Content: tool_calls 的 arguments
// 同样是前缀的一部分, 只比 Content 会漏判「同一句话、不同工具参数」的断裂。
func msgFingerprint(m ChatMessage) string {
	b, err := json.Marshal(m)
	if err != nil {
		return "err"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}

// setReqShape 在每次请求发出前调用: 算形状 + 算 LCP + 更新基线。
func setReqShape(msgs []ChatMessage) {
	fps := make([]string, len(msgs))
	chars := 0
	for i, m := range msgs {
		fps[i] = m.Role + ":" + msgFingerprint(m)
		chars += len([]rune(m.Content))
	}
	reqShapeMu.Lock()
	defer reqShapeMu.Unlock()
	lcp, prev := -1, -1
	if lastMsgFps != nil {
		prev = len(lastMsgFps)
		lcp = 0
		for lcp < len(fps) && lcp < prev && fps[lcp] == lastMsgFps[lcp] {
			lcp++
		}
	}
	lastMsgFps = fps
	head := len(fps)
	if head > 4 {
		head = 4
	}
	var pfx string
	for i := 0; i < head; i++ {
		pfx += fps[i] + "|"
	}
	curReqShape = reqShape{
		PID: os.Getpid(), Msgs: len(msgs), Chars: chars,
		Est: estimateTokens(msgs), LCP: lcp, Prev: prev, Pfx: pfx,
	}
}

// getReqShape 返回最近一次请求的形状快照。
func getReqShape() reqShape {
	reqShapeMu.Lock()
	defer reqShapeMu.Unlock()
	return curReqShape
}

// resetReqShape 清空进程内前缀基线 (测试隔离用)。
func resetReqShape() {
	reqShapeMu.Lock()
	defer reqShapeMu.Unlock()
	lastMsgFps = nil
	curReqShape = reqShape{LCP: -1, Prev: -1}
}
