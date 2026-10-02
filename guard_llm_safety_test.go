package main

import "testing"

// TestGuard_TaskKillByPID 钉死「LLM 误杀 PID」防御：任何 taskkill /PID 命中都走审批。
// 教训(20261001)：实战中 LLM 用 taskkill /F /PID 13356 把主进程干掉，原因是它
// 无法区分子进程 vs 主进程。/im 模式已防但 /PID 漏判，故加此模式。
func TestGuard_TaskKillByPID(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"PID 杀子进程", `subprocess.run(["taskkill", "/F", "/PID", "13356"])`, true},
		{"PID 空格变体", `taskkill  /F  /PID 9999`, true},
		{"小写 pid", `taskkill /f /pid 1234`, true},
		{"仅 taskkill 无 PID", `taskkill /im notepad.exe`, false},
		{"无 taskkill", `print("hello")`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, danger := checkDangerousCode(c.code)
			if danger != c.want {
				t.Errorf("code=%q: want danger=%v got %v", c.code, c.want, danger)
			}
		})
	}
}

// TestGuard_SubprocessForgeExe 钉死「python 包一层调 forge.exe」防御。
// 教训(20261001)：LLM 调 self gate 时用 python 的 subprocess.run(forge.exe /self ...)
// 包了一层，触发 python gate 的 30s 超时，而真正的 self gate 需要 60s+。应直接调
// self gate (lang="self") 而非包 subprocess。
func TestGuard_SubprocessForgeExe(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		{"subprocess.run forge.exe", `subprocess.run(["D:\forge\forge.exe", "/self", "deploy"])`, true},
		{"subprocess.Popen forge.exe", `subprocess.Popen([r"D:\forge\forge.exe", "--help"])`, true},
		{"cmd 调 forge", `subprocess.call(["forge.exe"])`, true},
		{"普通 subprocess", `subprocess.run(["ls", "-la"])`, false},
		{"普通代码", `print("ok")`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, danger := checkDangerousCode(c.code)
			if danger != c.want {
				t.Errorf("code=%q: want danger=%v got %v", c.code, c.want, danger)
			}
		})
	}
}
