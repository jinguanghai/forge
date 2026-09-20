package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestShPrefersBashJudgement 判据单测 (纯函数, 无副作用)。
//
// 缺陷背景: gate_audit 实测 52 条 tool_missing, 其中 17 条是
// "shell execution failed: 'head'/'ls'/'tail'/'pwd'/'#!' is not recognized"
// —— LLM 写的是 POSIX shell (head/ls/管道), sh gate 在 Windows 上却用 cmd.exe
// 执行, 必然报 "is not recognized as an internal or external command"。
// 本机存在 Git Bash (C:\Program Files\Git\bin\bash.exe) 却从未被使用。
//
// 判据目标: 明确 POSIX 特征的代码走 bash, cmd 专有命令保持 cmd.exe, 中性代码不动
// (不扩大改动面)。所有判定必须可解释、可测试, 不含"猜"。
func TestShPrefersBashJudgement(t *testing.T) {
	cases := []struct {
		name string
		code string
		want bool
	}{
		// ── POSIX 工具首词 → 必须 bash (cmd.exe 下 100% 报 is not recognized) ──
		{"ls", "ls -la", true},
		{"head", "head -5 gate_audit.jsonl", true},
		{"tail", "tail -3 x.txt", true},
		{"pwd", "pwd", true},
		{"grep", "grep -n foo bar.txt", true},
		{"rm", "rm -f x.txt", true},
		{"多行第二行ls", "echo hi\nls -la", true},
		{"export", "export A=1\necho $A", true},
		{"shebang", "#!/bin/bash\necho hi", true},
		{"命令替换", "echo $(date +%s)", true},
		{"反引号", "echo `date`", true},

		// ── 管道/串联右侧的 POSIX 命令 → 必须 bash ──
		// 缺陷背景: 旧实现只取整行首词, "type a.txt | head -2" 判 false
		// → 落 cmd.exe → "head is not recognized" (20260916 探针复现)。
		{"管道右侧head", "type a.txt | head -2", true},
		{"管道右侧cat", "go version | cat", true},
		{"管道右侧wc", "dir /b | wc -l", true},
		{"管道右侧grep", "type x.txt | grep foo", true},
		{"分号右侧ls", "echo hi; ls -la", true},
		{"and右侧tail", "echo hi && tail -3 x.txt", true},
		{"or右侧sed", "echo hi || sed -n 1p x.txt", true},
		{"后台右侧rm", "echo hi & rm -f x.txt", true},

		// ── bash 语法关键字 / 独有片段 → 必须 bash ──
		// 缺陷背景: gate_audit 8 条真实失败 "The syntax of the command is incorrect"
		// / "f was unexpected" / "<< was unexpected" —— bash 语法落 cmd.exe 必死。
		{"if_fi", "if [ -f x ]; then echo y; fi", true},
		{"for_done", "for i in 1 2 3; do echo $i; done", true},
		{"while_done", "while read l; do echo $l; done < x.txt", true},
		{"case_esac", "case $1 in a) echo a;; esac", true},
		{"double_bracket", "[[ -f x ]] && echo y", true},
		{"heredoc", "cat <<EOF\ndata\nEOF", true},
		{"param_expand", "echo ${HOME}", true},
		{"function_kw", "function foo() { echo hi; }", true},
		{"local_kw", "local x=1\necho $x", true},
		{"elif_kw", "if [ -f a ]; then echo 1; elif [ -f b ]; then echo 2; fi", true},

		// ── cmd 同名关键字不得误升级 (for/if 是 cmd 合法命令) ──
		{"cmd_for_loop", "for %i in (*.go) do echo %i", false},
		{"cmd_if_exist", "if exist a.txt echo yes", false},
		{"echo_fi_is_arg", "echo fi", false},
		{"findstr_do_is_arg", "dir /b | findstr do", false},
		{"cmd_dollar_plain", "echo $HOME", false},

		// ── 管道两侧皆非 POSIX → 不得误升级 ──
		{"cmd管道cmd", "dir /b | findstr go", false},
		{"python管道", "python x.py | more", false},
		{"引号内竖线", `python -c "print(1|2)"`, false},

		// ── cmd.exe 专有 → 必须保持 cmd (改走 bash 会破坏语义) ──
		{"cmd_dir", "dir /b", false},
		{"cmd_type", "type x.txt", false},
		{"cmd_copy", "copy a.txt b.txt", false},
		{"cmd_del", "del x.txt", false},
		{"cmd_mkdir", "mkdir sub", false},

		// ── 中性 (两壳皆有/皆可) → 保持现状, 不扩大改动面 ──
		{"echo", "echo hi", false},
		{"python", "python x.py", false},
		{"空", "", false},
		{"纯空白", "   \n  ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shPrefersBash(c.code); got != c.want {
				t.Errorf("shPrefersBash(%q) = %v, want %v", c.code, got, c.want)
			}
		})
	}
}

// TestForgeFindBashIsReal 探测结果必须指向真实存在的可执行文件 (反"编造路径")。
func TestForgeFindBashIsReal(t *testing.T) {
	p := forgeFindBash()
	if p == "" {
		t.Skip("本机未探测到 Git Bash")
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatalf("forgeFindBash 返回了不存在的路径: %s (%v)", p, err)
	}
	if st.IsDir() {
		t.Fatalf("forgeFindBash 返回目录而非可执行文件: %s", p)
	}
}

// TestShBashLiveExecution 缺陷活体复现与修复验证:
// head / ls / 管道 在旧实现下必然 "is not recognized", 修复后必须真跑通。
func TestShBashLiveExecution(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 专属缺陷")
	}
	if forgeFindBash() == "" {
		t.Skip("本机无 Git Bash")
	}
	f := newTestForge(t)
	if err := os.WriteFile(filepath.Join(f.workDir, "a.txt"), []byte("l1\nl2\nl3\nl4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"head -2 a.txt", "ls -1 a.txt", "cat a.txt | head -2", "pwd", "wc -l a.txt", "echo abc | wc -l",
		// bash 语法活体: 修复前落 cmd.exe 必报 "The syntax of the command is incorrect"
		"if [ -f a.txt ]; then echo yes; fi", "for i in 1 2 3; do echo $i; done"} {
		res := f.selfHostedSh(code, "", time.Now())
		if !res.OK {
			t.Errorf("selfHostedSh(%q) 失败: stage=%s err=%s", code, res.Stage, res.Error)
			continue
		}
		if res.Stdout == "" {
			t.Errorf("selfHostedSh(%q) 成功但无输出", code)
		}
	}
	// 管道盲区活体回归: 修复前必落 cmd.exe → 'head' is not recognized。
	// 修复后走 bash (type 的 cmd/bash 语义差异不计入本测试, 只看是否还报 not recognized)。
	res := f.selfHostedSh("type a.txt | head -2", "", time.Now())
	if !res.OK && strings.Contains(res.Error, "is not recognized") {
		t.Errorf("管道盲区未修复, 仍落 cmd.exe: %s", res.Error)
	}
}

// TestSplitShellSegments 分段器单测 (纯词法, 无副作用)。
func TestSplitShellSegments(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a | b", []string{"a ", " b"}},
		{"a|b", []string{"a", "b"}},
		{"a && b", []string{"a ", " b"}},
		{"a || b", []string{"a ", " b"}},
		{"a ; b", []string{"a ", " b"}},
		{"a & b", []string{"a ", " b"}},
		{"a > x", []string{"a > x"}},
		{"a", []string{"a"}},
		{"", []string{""}},
		{"| a", []string{"", " a"}},
		{"a |", []string{"a ", ""}},
	}
	for _, c := range cases {
		got := splitShellSegments(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitShellSegments(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitShellSegments(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestShCmdBuiltinRegression cmd 专有命令未被 bash 改造误伤 (向后兼容哨兵)。
func TestShCmdBuiltinRegression(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows 专属")
	}
	f := newTestForge(t)
	if err := os.WriteFile(filepath.Join(f.workDir, "a.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	res := f.selfHostedSh("type a.txt", "", time.Now())
	if !res.OK {
		t.Errorf("cmd builtin 回归失败: stage=%s err=%s", res.Stage, res.Error)
	}
}
