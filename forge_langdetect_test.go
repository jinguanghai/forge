package main

import "testing"

// TestDetectLangNoMisroute 回归 20260913 实测缺陷 (六期 DMAIC 度量驱动)。
//
// 缺陷① Python 代码被判 go —— 旧实现用全文本 `(?:^|\s)func\s+\w+\s*\(` 匹配,
// 注释/字符串里出现 "func xxx(" 即命中, 且该分支优先于下方全部 Python 判定
// → go build 必然失败 "main.go:1:1: expected 'package'"。
// gate_audit 实测 68 条, 100% 来自 lang 省略 (自动检测)。
//
// 缺陷② Python 代码被判 sh —— 旧实现的行首命令表命中 `exit(0)` 等函数调用形态
// → sh gate 执行失败并重试 3 次, 最后由 fallback 转 python 才成功。
// 用户看到的是「成功」, 代价是白烧 3.1s (audit: lang=sh ok=true dur=3111ms)。
func TestDetectLangNoMisroute(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		// ① 缺陷活体复现: 注释/字符串里的 func 声明
		{"注释含func声明", "import os\n# helper func format_path( used below\ndef main():\n    print(os.getcwd())\nmain()", "python"},
		{"字符串含func", "import os\nmsg = \"call func foo( now\"\nprint(msg)", "python"},

		// ② 缺陷活体复现: 行首 shell 词形 (赋值 / 函数调用)
		{"行首exit调用", "import sys\nexit(0)", "python"},
		{"裸exit调用", "exit(0)", "python"},
		{"行首ls赋值", "ls = [1, 2, 3]\nprint(ls)", "python"},
		{"行首cat赋值", "cat = \"x\"\nprint(cat)", "python"},

		// 真 Go 不得误伤
		{"完整Go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(1)\n}", "go"},
		{"Go片段无package", "func add(a, b int) int {\n\treturn a + b\n}", "go"},
		{"Go方法接收者", "func (f *Foo) Bar() error {\n\treturn nil\n}", "go"},
		{"注释后package", "// Package demo\npackage main\n\nfunc main() {}", "go"},

		// 真 sh 不得误伤
		{"echo", "echo hi", "sh"},
		{"多命令", "echo hi\nls -la", "sh"},
		{"export", "export A=1\necho $A", "sh"},
		{"shebang bash", "#!/bin/bash\necho hi", "sh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := forgeDetectLang(c.code, ""); got != c.want {
				t.Errorf("detect(%q) = %q, want %q", c.code, got, c.want)
			}
		})
	}
}

// TestIsDeterministicFailure 确定性失败判定: 编译阶段的非环境失败不重试。
func TestIsDeterministicFailure(t *testing.T) {
	cases := []struct {
		name string
		r    ForgeGateResult
		want bool
	}{
		{"编译语法错误", ForgeGateResult{OK: false, Stage: "compile", Error: "go build failed: expected 'package'"}, true},
		{"超时归 Timeout 管", ForgeGateResult{OK: false, Stage: "compile", Error: "x", Timeout: true}, false},
		{"工具缺失属环境", ForgeGateResult{OK: false, Stage: "compile", Error: "python: not found"}, false},
		{"执行阶段保留重试", ForgeGateResult{OK: false, Stage: "execute", Error: "boom"}, false},
		{"成功", ForgeGateResult{OK: true, Stage: "done"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDeterministicFailure(c.r); got != c.want {
				t.Errorf("isDeterministicFailure(%+v) = %v, want %v", c.r, got, c.want)
			}
		})
	}
}
