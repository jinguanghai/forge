package main

// main_startup.go — 启动序拆分: 自替换/flag解析/配置加载/信号安装/历史目录。

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// runSelfReplace 自替换: forge_new.exe 启动时替换旧 forge.exe。
func runSelfReplace() {
	myName := filepath.Base(os.Args[0])
	if strings.TrimSuffix(myName, ".exe") == "forge_new" {
		exeDir := filepath.Dir(os.Args[0])
		oldExe := filepath.Join(exeDir, "forge.exe")
		newExe := filepath.Join(exeDir, "forge_new.exe")
		if _, err := os.Stat(newExe); err == nil {
			os.Remove(oldExe)
			os.Rename(newExe, oldExe)
		}
	}
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
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "\n%s Received signal: %v — shutting down...\n",
				color(ansi.yellow, "⚡"), sig)
			agent.Shutdown()
			os.Exit(0)
		}
	}()
}
