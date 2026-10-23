package main

// ============================================================================
// console_quiet_test.go —— 控制台静默补丁覆盖哨兵 (20261010)
//
// 用户报障: 「按时间段弹终端窗口, 里面没有文字, 影响干活」。
//
// 根因 (实测复现): 计划任务用 pythonw.exe (GUI 子系统, 无控制台) 启动巡检脚本;
// 脚本内部再调 cmd/go/git/ssh 等【控制台子系统】程序时, Windows 发现父进程没有
// 控制台, 就替子进程新建一个 -> 屏幕弹出黑窗。标题=程序完整路径, 内容为空
// (stdout 已被 capture_output 重定向到管道) —— 正是「终端, 没有文字」。
// hourly 每小时串行跑 26 项, 其中 11 个脚本没加静默补丁, 每个都弹一次;
// go test 类窗口会停留数秒~数十秒 (即「影响干活」的部分)。
//
// 判据: defense_system 下凡含 subprocess 调用的 .py 必须 import _winquiet。
//
// A/B 对照证据 (同脚本同命令, 仅差一行 import; 无控制台环境实测):
//   未加补丁 -> 新增窗口 1 个 (go.exe, 标题 "C:\Program Files\Go\bin\go.EXE")
//   已加补丁 -> 新增窗口 0 个
//   两者 rc 均为 0 (静默不影响功能)
//
// 为什么扫全包而不是写死清单: 教训「判据/哨兵必须扫全包, 禁写死单文件」——
// 本次缺陷正是「部分脚本有补丁、部分没有」而无人发现: 逐个登记必然漏。
// ============================================================================

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// subprocessCallRe 匹配真实调用点 (带左括号), 避免把注释/文档里的字样算进来。
var subprocessCallRe = regexp.MustCompile(`subprocess\.(run|Popen|call|check_output|check_call)\s*\(`)

// consoleQuietExempt: 补丁实现自身 (它就是 patch Popen 的那个文件, 无需被 patch)。
var consoleQuietExempt = map[string]bool{"_winquiet.py": true}

// consoleQuietMinScanned 探针自检下限: 扫描数低于此值说明正则/路径腐化 -> 判据恒绿。
const consoleQuietMinScanned = 10

func TestConsoleQuietPatchCoverage(t *testing.T) {
	const dir = "defense_system"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", dir, err)
	}
	var missing []string
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".py") || consoleQuietExempt[name] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		src := string(b)
		if !subprocessCallRe.MatchString(src) {
			continue
		}
		scanned++
		if !strings.Contains(src, "_winquiet") {
			missing = append(missing, name)
		}
	}
	// 补丁实现自身的有效性 (判据的判据): 缺了它, 所有 import 都只是空转。
	quiet, err := os.ReadFile(filepath.Join(dir, "_winquiet.py"))
	if err != nil {
		t.Fatalf("_winquiet.py 缺失: %v", err)
	}
	if !strings.Contains(string(quiet), "CREATE_NO_WINDOW") || !strings.Contains(string(quiet), "PATCHED") {
		t.Errorf("_winquiet.py 未实现 CREATE_NO_WINDOW 补丁 -> 判据空转")
	}
	if scanned < consoleQuietMinScanned {
		t.Fatalf("仅扫描到 %d 个含子进程调用的脚本 (下限 %d) -> 探针可能腐化, 判据恒绿",
			scanned, consoleQuietMinScanned)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("以下脚本含子进程调用却未加静默补丁 (计划任务 pythonw 下会弹控制台黑窗): %v\n"+
			"修法: 在首个 import 前加 try: import _winquiet / except ImportError: pass", missing)
	}
	t.Logf("已扫描 %d 个含子进程调用的脚本, 全部带静默补丁", scanned)
}
