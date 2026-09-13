package main

// main_startup.go — 启动序拆分: 自替换/flag解析/配置加载/信号安装/历史目录。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// runSelfReplace 自替换: 以 forge_new.exe 名字启动时, 把它就位为 forge.exe。
//
// 说明: self gate 现在自己完成「冒烟→就位」(见 forge.go deploySelfExe),
// 本函数只兜底「用户手动运行 forge_new.exe」这条路径。加固三点:
//  1. 定位用 os.Executable 而非 os.Args[0] —— 后者可能是相对路径(如 .\forge_new.exe),
//     filepath.Dir 得到 "." → 在别的目录启动时替换到错误位置且静默失败。
//  2. 备份失败 = 拒绝替换(旧 exe 必须可回滚, 不能裸奔)。
//  3. 每个结局都留痕(启动序早于 initEventLog, 故自带最小写入)。
func runSelfReplace() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	if strings.TrimSuffix(filepath.Base(self), ".exe") != "forge_new" {
		return
	}
	exeDir := filepath.Dir(self)
	oldExe := filepath.Join(exeDir, "forge.exe")
	newExe := filepath.Join(exeDir, "forge_new.exe")
	if _, serr := os.Stat(newExe); serr != nil {
		return
	}
	ts := time.Now().Format("20060102_150405")
	backup := ""
	if _, serr := os.Stat(oldExe); serr == nil {
		backup = oldExe + ".bak_" + ts
		if rerr := os.Rename(oldExe, backup); rerr != nil {
			fmt.Fprintf(os.Stderr, "%s 自替换中止: 备份 forge.exe 失败: %v\n", color(ansi.yellow, "⚠"), rerr)
			appendSelfReplaceEvent(exeDir, map[string]string{"result": "abort-backup-failed", "err": rerr.Error()})
			return
		}
	}
	// 直接 rename 就位: Go 在 Windows 走 MoveFileEx(MOVEFILE_REPLACE_EXISTING), 无需先删。
	// 先删后改名有致命窗口: 删成功 + 改名失败 = forge.exe 消失(只剩 forge_new.exe)。
	if rerr := os.Rename(newExe, oldExe); rerr != nil {
		note := "无备份可回滚"
		if backup != "" {
			if rbErr := os.Rename(backup, oldExe); rbErr == nil {
				note = "已回滚旧 exe"
				backup = ""
			} else {
				note = fmt.Sprintf("回滚失败(%v), 备份: %s", rbErr, backup)
			}
		}
		fmt.Fprintf(os.Stderr, "%s 自替换失败(%s): %v\n", color(ansi.yellow, "⚠"), note, rerr)
		appendSelfReplaceEvent(exeDir, map[string]string{"result": "failed", "err": rerr.Error(), "note": note})
		return
	}
	if _, serr := os.Stat(oldExe); serr != nil {
		fmt.Fprintf(os.Stderr, "%s 自替换后校验失败: %v\n", color(ansi.yellow, "⚠"), serr)
		appendSelfReplaceEvent(exeDir, map[string]string{"result": "verify-failed", "err": serr.Error()})
		return
	}
	pruneExeBackups(exeDir, 10)
	appendSelfReplaceEvent(exeDir, map[string]string{"result": "ok", "backup": backup})
}

// appendSelfReplaceEvent 启动序早期的最小留痕: 直接 append 到 <workDir>/.forge/events.jsonl。
// 此刻全局事件日志尚未 initEventLog(eventsPath 为空, logEvent 会静默丢弃),
// 所以这里自包含写一次。失败静默, 永不 panic。
func appendSelfReplaceEvent(workDir string, data map[string]string) {
	dir := filepath.Join(workDir, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}
	ev := Event{
		Ts:     time.Now().Format(time.RFC3339),
		Type:   EvSelfRestart,
		Detail: "startup-self-replace",
		Data:   data,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(b, '\n'))
}

// parseFlags 解析命令行 flag; --help/--version 直接退出。
func parseFlags() (showReasoning bool, nonFlagArgs []string) {
	showReasoning = true
	nonFlagArgs = []string{}
	for _, arg := range os.Args[1:] {
		if len(nonFlagArgs) > 0 {
			nonFlagArgs = append(nonFlagArgs, arg)
			continue
		}
		switch arg {
		case "--reasoning", "-r":
			showReasoning = true
		case "--no-reasoning", "-nr":
			showReasoning = false
		case "--help", "-h":
			printHelp()
			os.Exit(0)
		case "--version", "-v":
			fmt.Printf("铸剑炉 v%s — 流式智能体 · 编译器沙箱\n", AppVersion)
			os.Exit(0)
		default:
			nonFlagArgs = append(nonFlagArgs, arg)
		}
	}
	if showReasoning {
		os.Setenv("FORGE_SHOW_REASONING", "true")
	} else {
		os.Setenv("FORGE_SHOW_REASONING", "false")
	}
	return showReasoning, nonFlagArgs
}

// runStartupConfig 加载配置并执行启动序。
func runStartupConfig(showReasoning bool) (*Config, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	cfg.ShowReasoning = showReasoning
	initEventLog(cfg.WorkDir)
	reportLastUpgrade(cfg.WorkDir)
	if recovered, herr := HealMemory(cfg.WorkDir); herr != nil {
		fmt.Fprintf(os.Stderr, "%s 记忆自愈失败: %v\n", color(ansi.yellow, "⚠"), herr)
	} else if recovered {
		fmt.Fprintf(os.Stderr, "%s 记忆已从 .bak 自动恢复\n", color(ansi.yellow, "♻"))
	}
	// 记忆写入留痕基线 (幂等): 让写入路径哨兵部署即生效
	ensureMemoryWriteBaseline(cfg.WorkDir)
	senseScript := filepath.Join(cfg.WorkDir, "sense.py")
	if _, err := os.Stat(senseScript); err == nil {
		senseCmd := exec.Command("python", senseScript)
		senseCmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
		senseCmd.Run()
	}
	setCacheStatPath(cfg.WorkDir)
	return cfg, nil
}

// setupHistoryFile 创建 .forge 目录并返回 history 文件路径。
func setupHistoryFile(cfg *Config) string {
	forgeDir := filepath.Join(cfg.WorkDir, ".forge")
	if err := os.MkdirAll(forgeDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: failed to create forge directory %s: %v\n", forgeDir, err)
	}
	return filepath.Join(forgeDir, "history")
}

// installSignals 安装信号处理: 忙时首个 Ctrl+C 取消任务, 闲时退出, 二次/SIGTERM 关闭。
func installSignals(agent *AgentRunner) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		for sig := range sigCh {
			if agentBusy.Load() && sig == os.Interrupt && sigCount.Load() == 0 {
				sigCount.Add(1)
				fmt.Fprintf(os.Stderr, "\n%s 已取消当前任务（再次 Ctrl+C 退出）\n", color(ansi.yellow, "⚡"))
				agent.CancelCurrent()
				continue
			}
			if !agentBusy.Load() && sig == os.Interrupt {
				fmt.Fprintln(os.Stderr)
				fmt.Fprintf(os.Stderr, "%s 再见。\n", color(ansi.yellow, "⚡"))
				// os.Exit 会跳过 main 的 defer agent.Shutdown(), 缓存不落盘 →
				// 下次冷启动全量 miss。闲时退出也必须显式保存。
				if agent != nil {
					agent.Shutdown()
				}
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "\n%s Received signal: %v — shutting down...\n",
				color(ansi.yellow, "⚡"), sig)
			agent.Shutdown()
			os.Exit(0)
		}
	}()
}
