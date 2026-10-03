package main

// 鲁棒性测试: 纯函数/小工具的边界场景
// 覆盖: 空/null/超长/Unicode/注入/极端值/畸形输入
// 公理二: 把可判定的地盘收给死程序 —— 这里测的就是「程序死了会怎样」。
//
// 设计原则:
//   1. 不重复已有 happy path
//   2. 每条用例测一个独立边界, 失败时能精确指认
//   3. 极端值用 math.MaxInt64 等显式常量
//   4. NULL 字节 / ANSI 残留 / 超长 1MB / Unicode 混合 都至少有一例

// 覆盖: 鲁棒性测试 — 解析类(parseApproval / summarizeCode / 各语言报错)
//
// F2 文件≤500行 上限触发: 原单文件超上限, 拆分到此文件。

import (
	"strings"
	"testing"
)

func TestRobustness_ParseApproval_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// 已有 happy path 不重复
		{"大写混合 YeS", "YeS", true},
		{"全大写 OK", "OK", true},
		{"中文全角 是", "是", true},
		// 鲁棒性: 边界
		{"全部空格", "   ", false},
		{"仅 Tab", "\t\t", false},
		{"仅换行", "\n", false},
		{"空字符串", "", false},
		{"NULL 字节", "y\x00", false},
		{"Bell 字符", "\x07", false},
		{"单字符 y", "y", true},
		{"单字符 Y", "Y", true},
		{"单字符 n", "n", false},
		{"含 leading CR", "\ry", true},
		{"含 trailing CR", "y\r", true},
		{"y 后接 NUL y", "y\x00n", false},
		{"含其他字符 yX", "yX", false},
		{"emoji 前 y", "👍y", false},
		{"emoji 后 y", "y👍", false},
		{"超长 n", strings.Repeat("n", 10000), false},
		{"超长空格", strings.Repeat(" ", 10000), false},
		{"注入 y; rm", "y; rm -rf /", false},
		{"中文前后空格 是", "  是  ", true},
		{"Tab 中间", "y\tyes", false},
		{"CRLF 包裹 y", "\r\ny\r\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseApproval(c.in)
			if got != c.want {
				t.Errorf("parseApproval(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ─── summarizeCode ────────────────────────────────────────────────────

func TestRobustness_SummarizeCode_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // 空表示不需要具体断言, 只测不 panic + 长度限制
	}{
		{"空串", "", ""},
		{"仅空白", " \t\n\r ", ""},
		{"单字符", "a", "a"},
		{"中文", "你好世界", "你好世界"},
		{"emoji", "🔥👨‍💻", ""}, // 不具体断言长度
		{"混合中英", "func 计算() { 中文 }", "func 计算() { 中文 }"},
		{"超长 ASCII", strings.Repeat("a", 200), ""},
		{"超长中文", strings.Repeat("中", 200), ""},
		{"超长 emoji", strings.Repeat("😀", 200), ""},
		{"含 NULL", "abc\x00def", ""},
		{"含 ANSI", "\x1b[31mred\x1b[0m", ""},
		{"极长 1MB", strings.Repeat("a", 1024*1024), ""},
		{"CRLF 多行", "line1\r\nline2\r\nline3", ""},
		{"多个连续空白", "a    b\t\tc", "a b c"},
		{"仅换行", "\n\n\n", ""},
		{"Unicode 零宽", "a\u200bb\u200cc", "a\u200bb\u200cc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("summarizeCode(%q) panic: %v", c.in, r)
				}
			}()
			got := summarizeCode(c.in)
			if c.want != "" {
				if got != c.want {
					t.Errorf("summarizeCode(%q) = %q, want %q", c.in, got, c.want)
				}
			}
			// 摘要长度应 ≤ 121 字符(120 + …)
			if n := len([]rune(got)); n > 121 {
				t.Errorf("summarizeCode(%q) length = %d, want ≤ 121", c.in, n)
			}
		})
	}
}

// ─── clamp / clampFloat ──────────────────────────────────────────────

func TestRobustness_ParseGoErr_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		wantN int
	}{
		{"空", "", 0},
		{"仅空白", "   \n\t", 0},
		{"单条错误", "main.go:1:1: oops", 1},
		{"多条错误", "a.go:1:1: x\nb.go:2:3: y\nc.go:4:5: z", 3},
		{"无文件名", "1:1: oops", 0}, // 没有 .go
		{"非 .go 后缀", "main.txt:1:1: bad", 0},
		{"路径含空格", "C:\\Users\\me\\file .go:10:5: err", 0}, // 含空格路径不应匹配
		{"行/列非数字", "main.go:abc:def: bad", 0},
		{"行/列负数", "main.go:-1:-1: bad", 0}, // 行号正则匹配的是 \d+
		{"极长文件名", strings.Repeat("a", 500) + ".go:1:1: bad", 1},
		{"含 ANSI", "\x1b[31mmain.go:1:1: bad\x1b[0m", 1}, // parseGoErr 不去 ANSI
		{"含 NULL", "main.go:1:\x001: bad", 0},            // \d+ 在 \x00 处接断, 不匹配
		{"无冒号", "  \n main.go\n  ", 0},
		{"仅一行空白", "\n", 0},
		{"CRLF 错误", "main.go:1:1: bad\r\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parseGoErr panic: %v", r)
				}
			}()
			got := parseGoErr(c.in)
			if len(got) != c.wantN {
				t.Errorf("parseGoErr(%q) got %d errors, want %d", c.in, len(got), c.wantN)
			}
		})
	}
}

// ─── parsePyErr ───────────────────────────────────────────────────────

func TestRobustness_ParsePyErr_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		wantN int
	}{
		{"空", "", 0},
		{"完整 traceback", `Traceback (most recent call last):
  File "x.py", line 10, in <module>
    1/0
ZeroDivisionError: division by zero`, 1},
		{"多条 file 行", `File "a.py", line 1
File "b.py", line 2
ValueError: bad`, 2},
		{"仅 message", "NameError: undefined", 0}, // 无 line 行
		{"无 caret", `File "x.py", line 5
not match`, 1}, // lineRe 只需 File, 不需 caret    // 无 ^ 行 = col=0
		{"含 ANSI", "\x1b[31mFile \"x.py\", line 1\n\x1b[0m", 1}, // parsePyErr 不去 ANSI, lineRe 仍匹配
		{"file 含特殊路径", `File "C:\\path with space\\x.py", line 3`, 1},
		{"含 NULL", "File \"x.py\", line 0\x00", 1},
		{"仅换行", "\n\n\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parsePyErr panic: %v", r)
				}
			}()
			got := parsePyErr(c.in)
			if len(got) != c.wantN {
				t.Errorf("parsePyErr got %d errors, want %d", len(got), c.wantN)
			}
		})
	}
}

// ─── parseNodeErr ─────────────────────────────────────────────────────

func TestRobustness_ParseNodeErr_EdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		wantN int
	}{
		{"空", "", 0},
		{"stdin 含 col", "[stdin]:3:5", 1},
		{"stdin 仅 line", "[stdin]:3", 1},
		{"eval 含 col", "[eval]:1:1", 1},
		{"多条", "[stdin]:1\n[stdin]:2\n[stdin]:3", 3},
		{"含 msg", "[stdin]:3\nTypeError: bad", 1},         // posRe 只匹配 [stdin]:3 = 1 个
		{"仅 msg", "ReferenceError: bad", 0},               // 无位置行
		{"含 ANSI", "\x1b[31m[stdin]:3\x1b[0m", 0},         // posRe 要\d+,[ 中间,不匹配
		{"文件路径 node", "/usr/x.js:5", 0},                   // 不是 stdin/eval, 不算
		{"含 NULL 真实字节", strings.Repeat("\u0001", 100), 0}, // 全 NUL 不匹配
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parseNodeErr panic: %v", r)
				}
			}()
			got := parseNodeErr(c.in)
			if len(got) != c.wantN {
				t.Errorf("parseNodeErr got %d errors, want %d", len(got), c.wantN)
			}
		})
	}
}

// ─── parseCompilerError ───────────────────────────────────────────────

func TestRobustness_ParseCompilerError_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		lang string
		in   string
		want int
	}{
		{"空", "go", "", 0},
		{"go ANSI strip", "go", "\x1b[31mmain.go:1:1: bad\x1b[0m", 1},
		{"python ANSI strip", "python", "\x1b[31mFile \"x.py\", line 1\nZeroDivisionError\x1b[0m", 1},
		{"node ANSI strip", "node", "\x1b[31m[stdin]:1\x1b[0m", 1},
		{"未知语言", "fortran", "anything", 0},
		{"空 lang", "", "main.go:1:1: bad", 0},
		{"go 多条", "go", "a.go:1:1: x\nb.go:2:2: y", 2},
		{"bash 不支持", "bash", "anything", 0},
		{"javascript 不支持", "javascript", "anything", 0},
		{"NULL lang", "\x00", "main.go:1:1: bad", 0},
		{"超长 1MB ANSI", "go", strings.Repeat("\x1b[31m \x1b[0m", 1024*1024/7) + "main.go:1:1: bad", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("parseCompilerError panic: %v", r)
				}
			}()
			got := parseCompilerError(c.lang, c.in)
			if len(got) != c.want {
				t.Errorf("parseCompilerError(%q, ...) got %d, want %d", c.lang, len(got), c.want)
			}
		})
	}
}

// ─── validGateJSON / parseGateReject ─────────────────────────────────
