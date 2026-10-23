package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ══════════════════════════════════════════════════════════════════
// pythonCommand —— Python 解释器探测 (T25, 2026-10-08)
//
// 为什么需要【反境】: `python` 是铸剑炉的单点硬依赖 —— 4 个核心 gate
// (math/logic/regex/knowledge) 内部各 spawn 一次 + 脚本型 gate 宿主
// (browser/media/relation/task/memory_write/netroute) + 启动自检 sense.py
// + 记忆护栏 memory_write.py + 度量 quality_report.py + 自升级 forge_guard.py
// 全挂在它上面; 而 Debian/Ubuntu 系默认只提供 `python3`。
// 环境侧软链 (`ln -s "$(which python3)" /usr/local/bin/python`) 是换机时的
// 一行命令, 代码侧探测是不依赖换机时间的那一半。
//
// 优先级: 1) FORGE_PYTHON 显式覆盖 (逃生通道: 多解释器共存时可指定)
//         2) PATH 探测 —— Windows 先 python 后 python3; 其他平台先 python3 后 python
//         3) 回落字面 "python" —— 保持旧行为, 让「命令缺失」错误自然浮现,
//            不把探测失败伪装成别的东西 (fail-visible, 非 fail-silent)。
//
// 边界(不做): pythonw 23 处 (defense_system 巡检体系, 随计划任务→cron 一起处理);
//             asr.go / tts.go 的硬编码在 //go:build windows 内, Linux 走 stub。
// ══════════════════════════════════════════════════════════════════

// pythonCandidates 返回按平台排序的解释器候选序列。
// 平台优先项在前: Windows 上 `python` 才是常态 (官方安装器默认建它),
// 非 Windows 上 `python3` 才是常态 (Debian/Ubuntu 无 python 软链)。
func pythonCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{"python", "python3"}
	}
	return []string{"python3", "python"}
}

// pythonCommand 返回本机可用的 Python 解释器 (PATH 解析后的路径优先, 找不到时回落 "python")。
func pythonCommand() string {
	if v := strings.TrimSpace(os.Getenv("FORGE_PYTHON")); v != "" {
		if p, err := exec.LookPath(v); err == nil {
			return p
		}
	}
	for _, c := range pythonCandidates() {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return "python"
}

// resolveExecutable 把编译器表 (铸剑炉_COMPILERS) 里的可执行名归一化到本机实际命令。
// 目前只有 python 需要平台探测; 其他名字原样返回 (零行为变更)。
func resolveExecutable(name string) string {
	if name == "python" {
		return pythonCommand()
	}
	return name
}
