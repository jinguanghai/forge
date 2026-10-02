package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ─── gate 输出捕获上限 ────────────────────────────────────────────────
//
// 动机 (DeepSeek Elastic Compute, DSec, arXiv 2609.22978, 2026-09-19):
// 论文记录 agent 执行 `yes` 命令, 其输出被平台采集, 累积几十 GB 存储。
// 铸剑炉侧存在同构缺陷: gate 捕获点全为无上限 Builder/Buffer, 而给 LLM 的
// 截断 (truncateOutput, 8000 rune) 发生在 gate 返回之后, 管不住内存峰值。
//
// 本机探针实测 (子进程输出 64 MiB, 由 python 生成):
//
//	无限制 strings.Builder   → 堆 +64.1 MiB,  exit=0
//	无限制 bytes.Buffer      → 堆 +128.0 MiB (2x 扩容策略), exit=0
//	limitedWriter (4 MiB)    → 堆 +4.4 MiB,   exit=0   ← 采用
//	errWriter (超限返 error) → 堆 +4.4 MiB,   exit=1   ← 反例
//
// 最后一行是硬约束: Write 必须永远返回 (len(p), nil)。返回 error 会让
// os/exec 的拷贝协程中止, 子进程写满管道缓冲后收 EPIPE 而死 —— 表现为
// 「限流把任务搞挂了」, 比不限流更糟。
const maxGateCaptureBytes = 4 << 20

// gateCaptureLimit 返回单流捕获上限(字节), 可由 FORGE_MAX_CAPTURE 覆盖。
func gateCaptureLimit() int {
	if v := strings.TrimSpace(os.Getenv("FORGE_MAX_CAPTURE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return maxGateCaptureBytes
}

// limitedWriter 是带上限的输出捕获器: 超限部分丢弃并计数, 永不报错。
type limitedWriter struct {
	buf     strings.Builder
	limit   int
	dropped int
}

func newLimitedWriter(limit int) *limitedWriter {
	if limit <= 0 {
		limit = maxGateCaptureBytes
	}
	return &limitedWriter{limit: limit}
}

// Write 永远返回 (len(p), nil) —— 见文件头注释的反例实测。
func (w *limitedWriter) Write(p []byte) (int, error) {
	room := w.limit - w.buf.Len()
	if room > 0 {
		if room > len(p) {
			room = len(p)
		}
		w.buf.Write(p[:room])
	} else {
		room = 0
	}
	w.dropped += len(p) - room
	return len(p), nil
}

func (w *limitedWriter) String() string { return w.buf.String() }

func (w *limitedWriter) Len() int { return w.buf.Len() }

// Dropped 返回被丢弃的字节数。
func (w *limitedWriter) Dropped() int { return w.dropped }

// markCaptureLimit 把捕获丢弃量写回 gate 结果(供展示层与审计使用)。
func markCaptureLimit(r *ForgeGateResult, ws ...*limitedWriter) {
	total := 0
	for _, w := range ws {
		if w != nil {
			total += w.Dropped()
		}
	}
	if total > 0 {
		r.DroppedBytes = total
		r.OutputTruncated = true
	}
}

// captureNote 生成人类可读的超限提示(无超限返回空串)。
func captureNote(r ForgeGateResult) string {
	if !r.OutputTruncated {
		return ""
	}
	return fmt.Sprintf("⚠ 输出超限: 已丢弃 %d 字节(上限 %d 字节/流, 可用 FORGE_MAX_CAPTURE 调整)",
		r.DroppedBytes, gateCaptureLimit())
}
