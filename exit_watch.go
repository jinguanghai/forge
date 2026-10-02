package main

// exit_watch.go — 非正常退出检测 (20261002「炉体自杀」事故的兜底防线)。
//
// 事故: 15:17:02 炉体被自己执行的 taskkill /F /IM forge.exe 强杀, events.jsonl
// 在 approved 那一行硬截断 —— 会话静默消失, 主人只能从终端残留的提示里发现异常。
// 「检测到自杀」已在批准层修掉 (见 approval_delegate.go); 本文件补最后一道:
// **即使炉体还是死了, 下次启动必须说出「上次没正常收尾」**。
//
// 判据(可判定): 事件账本的最后一条记录是不是 shutdown。
//   正常退出 = main 的 defer / 信号处理 / 窗口关闭 → 三条路径都经 agent.Shutdown(),
//   在那里写一条 shutdown (20261002 接线)。被强杀/崩溃/断电 = 最后一条停在
//   tool_called / tool_result / approved 之类, 于是判为异常。
//
// 设计纪律: 宁可漏报不可误报 —— 正常收尾时本检测**必须完全静默**。
// 每次启动都喊"上次异常", 就是又一次告警贬值(本次事故的根因之一)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// exitTailBytes 只读事件账本尾部这么多字节: 账本可达数 MB, 全量读会拖慢每次启动。
const exitTailBytes = 8192

// lastEventOf 取事件账本的最后一条可解析记录 (只读尾部)。
// 尾部起点可能切坏一条记录, 故从后往前找第一条能解析的; 找不到返回 ok=false。
func lastEventOf(path string) (Event, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Event{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return Event{}, false
	}
	size := st.Size()
	off := int64(0)
	if size > exitTailBytes {
		off = size - exitTailBytes
	}
	if _, err := f.Seek(off, 0); err != nil {
		return Event{}, false
	}
	buf := make([]byte, size-off)
	if _, err := io.ReadFull(f, buf); err != nil {
		return Event{}, false
	}
	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev Event
		if json.Unmarshal(line, &ev) != nil || ev.Type == "" {
			continue
		}
		return ev, true
	}
	return Event{}, false
}

// abnormalExitNotice 由「上次账本的最后一条事件」生成启动横幅。
// 纯函数: 无记录、或最后一条是 shutdown → 返回空串 (正常收尾必须完全静默)。
func abnormalExitNotice(ev Event, ok bool) string {
	if !ok || ev.Type == EvShutdown {
		return ""
	}
	when := ev.Ts
	if when == "" {
		when = "时间未知"
	}
	return fmt.Sprintf("%s 上次运行未正常收尾 (最后事件: %s @ %s)\n"+
		"   → 可能是被强杀/崩溃/断电。会话未必全丢: 现场见 .forge/checkpoint.json 与 .forge/history。\n",
		color(ansi.yellow, "⚠"), ev.Type, when)
}

// reportLastExit 启动时报告上次是否正常收尾。
// 必须在**本次进程写任何事件之前**调用, 否则会读到自己的新记录而判据自毁。
func reportLastExit(workDir string) {
	ev, ok := lastEventOf(filepath.Join(workDir, ".forge", "events.jsonl"))
	if notice := abnormalExitNotice(ev, ok); notice != "" {
		fmt.Fprint(os.Stderr, notice)
	}
}

// markCleanExit 正常收尾留痕: 写一条 shutdown 事件。
// 由 agent.Shutdown 调用, 那三条正常退出路径的唯一汇聚点。
// 失败静默(logEvent 的纪律): 收尾留痕绝不能拖垮退出流程。
func markCleanExit() {
	logEvent(EvShutdown, "clean-exit", nil)
}
