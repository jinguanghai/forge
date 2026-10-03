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

// 自替换后的「重启」做成可注入变量 (20261003):
//
// Windows 的进程名在启动那一刻定格 —— 把 forge_new.exe 改名就位成 forge.exe 之后,
// 进程名仍然是 forge_new.exe。于是所有「按名字关旧炉子」的外部脚本(upgrade.cmd /
// restart.cmd / rollback.cmd / 各 .bat)全部失明: 升级脚本关不掉旧进程, 却继续往下
// 走 → 同时起两个实例抢写 memory.json / 缓存 / 审计流, 而存活检查看到新实例又报
// 「升级成功」= 假成功。修法就是就位后自己重启, 让进程名与文件名永远一致。
//
// 之所以做成变量: 真实 spawn 出去就收不回来(会多起一个进程), 测试无法观察, 只能靠
// 注入断言参数; 退出同理 —— 测试进程不能被真退出。
var (
	spawnSelfProcess = func(exePath, workDir string) error {
		cmd := exec.Command(exePath)
		cmd.Dir = workDir
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			return err
		}
		// 故意不 Wait: 子进程要独立于父进程活下去(父进程随后 selfReplaceExit)。
		// Windows 上父进程退出不会连带杀死子进程, 控制台仍被子进程附着。
		return nil
	}
	selfReplaceExit = func(code int) { os.Exit(code) }
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
	pruneExeBackups(exeDir, backupKeepCount)
	// 就位后必须重启 —— 理由见 spawnSelfProcess 的注释(进程名定格)。
	ev := map[string]string{"result": "ok", "backup": backup}
	if serr := spawnSelfProcess(oldExe, exeDir); serr != nil {
		ev["restart"] = "failed"
		ev["err"] = serr.Error()
		appendSelfReplaceEvent(exeDir, ev)
		fmt.Fprintf(os.Stderr, "%s 已就位为 forge.exe, 但自动重启失败: %v\n", color(ansi.yellow, "⚠"), serr)
		fmt.Fprintf(os.Stderr, "   请手动关闭本窗口, 再双击 %s\n", oldExe)
		return
	}
	ev["restart"] = "spawned"
	appendSelfReplaceEvent(exeDir, ev)
	selfReplaceExit(0)
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
			fmt.Printf("铸剑炉 %s — 流式智能体 · 编译器沙箱\n", versionString())
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
	// 非正常退出检测: 必须紧跟 initEventLog —— 早于本次进程写的任何事件,
	// 否则判据会读到自己的新记录而永远判"正常"(见 exit_watch.go)。
	reportLastExit(cfg.WorkDir)
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
	// 5S 出口: 一次性临时区启动即清空。必须在 initEventLog 之后(留痕需要 eventsPath),
	// 且只能落在这里 —— 放 NewForge 会让 17 个测试调用点互踩, 详见 cleanupWorkTempDir 注释。
	cleanupWorkTempDir(cfg.WorkDir)
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
