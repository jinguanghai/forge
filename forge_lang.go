package main

import (
	"encoding/json"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// CompilerDef describes how to check and execute code in a language.
type CompilerDef struct {
	Check          []string
	Exec           []string
	Lint           []string
	Ext            string
	CompileTimeout time.Duration
	ExecTimeout    time.Duration
	InlineCode     bool
	SelfHosted     bool
}

// CompilerError represents a parsed compiler diagnostic.
type CompilerError struct {
	Lang string `json:"lang"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Msg  string `json:"msg"`
}

// retiredLangSentinels 是已退役、但仍由检测层返回的"哨兵值"。
//
// 为什么检测层还返回它: 返回不是为了执行, 而是让 Build 的退役拒绝分支接住,
// 从而在审计里区分"模型仍想用 sh"(度量学习曲线) 与"自动检测判成 sh"(占实际请求 82.1%)。
//
// 前置条件 (必须成立, 否则哨兵值会落到 forgeGate 的「不支持的语言」分支):
//
//	每个哨兵 key 必须在 forge.go Build() 里被拒绝分支拦截 —— 该前置条件由
//	TestForgeDetectLang_ReturnsOnlyCompilerTableKeys 自身断言(读 forge.go 源码核对),
//	以及 sh_retired_test.go::TestShRetired_RetireBranchBeforeExecution 双重钉住。
var retiredLangSentinels = map[string]bool{
	"sh": true, // 20261001 退役 (实测失败率 48.2%, 全炉唯一 fallback 源)
}

var 铸剑炉_COMPILERS = map[string]CompilerDef{
	"go": {
		Ext:            ".go",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     false,
		SelfHosted:     true,
	},
	"math": {
		Ext:            "",
		CompileTimeout: 15 * time.Second,
		ExecTimeout:    15 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"logic": {
		Ext:            "",
		CompileTimeout: 15 * time.Second,
		ExecTimeout:    15 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"knowledge": {
		Ext:            "",
		CompileTimeout: 30 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"regex": {
		Ext:            "",
		CompileTimeout: 10 * time.Second,
		ExecTimeout:    10 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"chain": {
		Ext:            "",
		CompileTimeout: 30 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"relation": {
		Ext:            "",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    15 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"python": {
		Check: []string{"python", "-m", "py_compile"},
		// -u 强制无缓冲: 管道下 python 默认块缓冲, 超时时 stdout 一个字都拿不到,
		// 反馈只剩 "context deadline exceeded" —— 模型看不到卡在哪 (P0 20260930)。
		Exec:           []string{"python", "-u", "{file}"},
		Ext:            ".py",
		CompileTimeout: 10 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     false,
	},
	"node": {
		Exec:           []string{"node", "-"},
		Ext:            ".js",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    30 * time.Second,
		InlineCode:     true,
	},
	"self": {
		Ext:            "",
		CompileTimeout: 60 * time.Second,
		ExecTimeout:    60 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"tcm": {
		Ext:            "",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    10 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"browser": {
		Ext:            ".py",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    120 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
	"media": {
		Ext:            ".py",
		CompileTimeout: 5 * time.Second,
		ExecTimeout:    120 * time.Second,
		InlineCode:     true,
		SelfHosted:     true,
	},
}

// pythonSignatureRE 是 Python 代码的结构性特征, 用于在「弱信号语言判定」前做排除。
// 判据单一源: Go 误判与 Shell 误判两处修复共用同一正则, 避免两套规则各自腐化。
// 为什么这些形态对 Go/Shell 安全 (逐条核对过):
//   - Go 的 import 是 `import (` 或 `import "path"`, 不匹配行首 `import <标识符>`
//   - Go 的输出是 fmt.Println(, 不含 print(
//   - Shell 里 `import x` / `from x import y` 都不是合法命令
var pythonSignatureRE = regexp.MustCompile(`(?m)^\s*(?:def|class)\s+\w+|(?m)^\s*(?:import|from)\s+\w+|print\s*\(|__name__`)

// goTopLevelRE 匹配行首的 Go 顶层声明 (含方法接收者 `func (r T) Name()`)。
// 用行首锚定取代旧版的全文本 `\s+func`: 闭包 `x := func()` 不在行首,
// 本就不构成「这是 Go 代码」的证据。
var goTopLevelRE = regexp.MustCompile(`(?m)^func\s`)

// atoi converts a string to int, returning 0 on failure.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func parseGoErr(text string) []CompilerError {
	var out []CompilerError
	re := regexp.MustCompile(`([^:\s]+\.go):(\d+):(\d+):\s*(.+)`)
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		out = append(out, CompilerError{
			Lang: "go", Line: atoi(m[2]), Col: atoi(m[3]),
			Msg: strings.TrimSpace(m[4]),
		})
	}
	return out
}

func parsePyErr(text string) []CompilerError {
	var out []CompilerError
	lineRe := regexp.MustCompile(`File "[^"]*", line (\d+)`)
	msgRe := regexp.MustCompile(`(?m)^\s*([A-Za-z][A-Za-z0-9_]*(?:Error|Exception): [^\r\n]+)`)
	caretRe := regexp.MustCompile(`(?m)^(\s*)\^`)
	msg := ""
	if mm := msgRe.FindStringSubmatch(text); len(mm) > 1 {
		msg = strings.TrimSpace(mm[1])
	}
	col := 0
	if cm := caretRe.FindStringSubmatch(text); len(cm) > 1 {
		col = len(cm[1]) + 1
	}
	for _, m := range lineRe.FindAllStringSubmatch(text, -1) {
		out = append(out, CompilerError{
			Lang: "python", Line: atoi(m[1]), Col: col,
			Msg: truncateMsg(msg),
		})
	}
	return out
}

func parseNodeErr(text string) []CompilerError {
	var out []CompilerError
	posRe := regexp.MustCompile(`(?m)^\[(?:stdin|eval)\]:(\d+)(?::(\d+))?`)
	msgRe := regexp.MustCompile(`(?m)^([A-Za-z]+Error: [^\r\n]+)`)
	caretRe := regexp.MustCompile(`(?m)^(\s*)\^`)
	msg := ""
	if mm := msgRe.FindStringSubmatch(text); len(mm) > 1 {
		msg = strings.TrimSpace(mm[1])
	}
	col := 0
	if cm := caretRe.FindStringSubmatch(text); len(cm) > 1 {
		col = len(cm[1]) + 1
	}
	for _, m := range posRe.FindAllStringSubmatch(text, -1) {
		c := atoi(m[2])
		if c == 0 {
			c = col
		}
		out = append(out, CompilerError{
			Lang: "node", Line: atoi(m[1]), Col: c,
			Msg: truncateMsg(msg),
		})
	}
	return out
}

func parseCompilerError(lang, text string) []CompilerError {
	// Strip ANSI color codes before parsing.
	ansiRe := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	text = ansiRe.ReplaceAllString(text, "")
	switch lang {
	case "go":
		return parseGoErr(text)
	case "python":
		return parsePyErr(text)
	case "node":
		return parseNodeErr(text)
	default:
		return nil
	}
}

func compilerErrorsToJSON(errs []CompilerError) string {
	if len(errs) == 0 {
		return ""
	}
	b, _ := json.Marshal(errs)
	return string(b)
}

// hasGoPackageDecl 判断首个有效行 (跳过空行与注释行) 是否为 package 声明。
// 强信号: package 子句必须位于文件最前, 是 Go 语法的硬约束。
func hasGoPackageDecl(code string) bool {
	for _, ln := range strings.Split(code, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") {
			continue
		}
		return strings.HasPrefix(t, "package ")
	}
	return false
}

func forgeDetectLang(code, hint string) string {
	code = strings.TrimSpace(code)

	// Go 判定分两级信号:
	//   强信号 = 首个有效行是 package 声明 (Go 语法硬约束, 别的语言无法满足)
	//   弱信号 = 行首 func 声明, 但必须先排除 Python 特征
	// 旧实现用裸 `(?:^|\s)func\s+\w+\s*\(` 全文本匹配 → Python 代码任何位置出现
	// "func xxx(" (注释/字符串/文档) 即被判 go, 且该分支优先于下方全部 Python 判定。
	// 实测 gate_audit 68 条 "main.go:1:1: expected 'package'" 全是此类误判, 且
	// 100% 来自 lang 省略(自动检测): 模型没写 lang, 检测器把 Python 判成 Go。
	if hasGoPackageDecl(code) {
		return "go"
	}
	if !pythonSignatureRE.MatchString(code) && goTopLevelRE.MatchString(code) {
		return "go"
	}

	// Shell/Bash detection — check shebang and shell keywords
	if strings.HasPrefix(code, "#!") {
		if strings.Contains(code, "bash") || strings.Contains(code, "sh") {
			return "sh"
		}
		if strings.Contains(code, "python") {
			return "python"
		}
		if strings.Contains(code, "node") {
			return "node"
		}
	}
	// Shell-like patterns: standalone commands at line starts. A follow-up
	// check rejects Python assignments that merely use a shell keyword as a
	// variable name (e.g. `echo = 5`, `cat = "x"`, `cd = "/tmp"`).
	// NOTE: Go regexp (RE2) does not support lookahead, so the `not followed
	// by =` check is done with FindStringIndex + TrimLeft instead of (?!\s*=).
	// 判定前先排除 Python 特征: 行首 `import os` / `from x import y` 在 sh 里不是
	// 合法命令, 实测 "'import' is not recognized" 23 条即 Python 代码被判 sh。
	shellCmdRE := regexp.MustCompile(`(?m)^\s*(?:echo|export|source|unset|alias|chmod|chown|mkdir|rm|cp|mv|ls|cat|grep|awk|sed|cd|pwd|exit)\b`)
	if !pythonSignatureRE.MatchString(code) {
		if loc := shellCmdRE.FindStringIndex(code); loc != nil {
			after := strings.TrimLeft(code[loc[1]:], " \t")
			// 排除两种「词形相同但语义不同」的形态:
			//   `cat = "x"` → 变量赋值 (= 开头)
			//   `exit(0)`   → 函数调用 (( 开头); shell 命令后不会紧跟括号
			// 实测: `import sys\nexit(0)` 被判 sh, sh gate 失败 3 次后由 fallback
			// 转 python 才成功 —— 用户看到「成功」, 代价是 3.1s 白烧 (audit 里
			// lang=sh/ok=true/dur=3111ms)。这类「成功但绕路」的缺陷只有审计能看见。
			if !strings.HasPrefix(after, "=") && !strings.HasPrefix(after, "(") {
				return "sh"
			}
		}
	}
	if strings.Contains(code, "import ") && (strings.Contains(code, "def ") || strings.Contains(code, "print(")) {
		return "python"
	}
	if strings.Contains(code, "console.log") || strings.Contains(code, "const ") {
		return "node"
	}

	// Python is the most common fallback
	if strings.Contains(code, "def ") || strings.Contains(code, "print(") ||
		strings.Contains(code, "import ") || strings.Contains(code, "class ") {
		return "python"
	}

	// 无编译器特征 → 语义 gate 路由 (math/logic)
	// 编译器优先已在前序完成; 此处只认"纯表达式", 防误伤 python 代码。
	if gate := detectSemanticGate(code); gate != "" {
		return gate
	}
	if hint != "" {
		return hint
	}
	return "python"
}

// detectSemanticGate: 纯数学/逻辑表达式 → 语义 gate。
// 设计: 只在前序编译器检测全部未命中时被调用; 含任何代码关键字立即放弃 (保持 python 默认)。
// 目标: 激活 gate_audit 中长期零使用的 math/logic —— 模型省略 lang 时自动路由。
func detectSemanticGate(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	// 明显代码/非表达式特征 → 不路由 (落 python 默认)
	codeKw := []string{"import ", "def ", "print(", "class ", "return ", "for ", "while ",
		"if ", "function", "var ", "let ", "const ", "http", "#", "```", ";", "{", "}", "\n"}
	for _, kw := range codeKw {
		if strings.Contains(code, kw) {
			return ""
		}
	}
	// 逻辑表达式: 逻辑连接词 (z3/命题逻辑常见记号)
	logicOps := []string{"=>", "<=>", "∀", "∃", "iff", "&", "|"}
	for _, op := range logicOps {
		if strings.Contains(code, op) {
			return "logic"
		}
	}
	// 数学表达式: 含数字 + 运算符; "=" 仅当等式(同时含运算)才路由, 纯赋值不路由
	mathRe := regexp.MustCompile(`^[\d\s\+\-\*\/\^\(\)\.\=\w]{1,200}$`)
	if mathRe.MatchString(code) {
		hasDigit := regexp.MustCompile(`\d`).MatchString(code)
		hasOp := strings.ContainsAny(code, "+-*/^")
		if hasDigit && hasOp {
			return "math"
		}
	}
	return ""
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// looksLikeValidGoTopLevel checks code is plausibly valid at Go package level.
func looksLikeValidGoTopLevel(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	validStarts := []string{"func ", "type ", "var ", "const ", "import ", "package "}
	for _, prefix := range validStarts {
		if strings.HasPrefix(code, prefix) {
			return true
		}
	}
	if strings.HasPrefix(code, "//") || strings.HasPrefix(code, "/*") {
		return true
	}
	return false
}
