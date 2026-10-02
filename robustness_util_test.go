package main

// 鲁棒性测试: 纯函数/小工具的边界场景
// 覆盖: 空/null/超长/Unicode/注入/极端值/畸形输入
// 公理五: 把可判定的地盘收给死程序 —— 这里测的就是「程序死了会怎样」。
//
// 设计原则:
//   1. 不重复已有 happy path
//   2. 每条用例测一个独立边界, 失败时能精确指认
//   3. 极端值用 math.MaxInt64 等显式常量
//   4. NULL 字节 / ANSI 残留 / 超长 1MB / Unicode 混合 都至少有一例

// 覆盖: 鲁棒性测试 — 工具函数(clamp / getEnvInt / getEnvFloat / cacheTTLForLang / looksLikeValidGoTopLevel / atoi / exeSuffix)
//
// F2 文件≤500行 上限触发: 原单文件超上限, 拆分到此文件。

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestRobustness_Clamp_Boundaries(t *testing.T) {
	cases := []struct {
		v, lo, hi, want int
	}{
		{0, 0, 0, 0},
		{5, 0, 10, 5},
		{-100, 0, 10, 0},
		{100, 0, 10, 10},
		{1 << 62, 0, 1 << 62, 1 << 62},
		{-1 << 62, 0, 100, 0},
		{math.MaxInt, 0, math.MaxInt, math.MaxInt},
		{math.MinInt, 0, math.MaxInt, 0},
		{math.MaxInt - 1, 0, math.MaxInt, math.MaxInt - 1},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("v=%d_lo=%d_hi=%d", c.v, c.lo, c.hi), func(t *testing.T) {
			got := clamp(c.v, c.lo, c.hi)
			if got != c.want {
				t.Errorf("clamp(%d,%d,%d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
			}
		})
	}
}

func TestRobustness_ClampFloat_Boundaries(t *testing.T) {
	cases := []struct {
		v, lo, hi, want float64
	}{
		{0.0, 0.0, 0.0, 0.0},
		{3.14, 0.0, 10.0, 3.14},
		{-1e300, 0.0, 1e300, 0.0},
		{1e300, 0.0, 1e300, 1e300},
		{1e-400, 0.0, 1.0, 0.0}, // 极小数 underflow → 0
		{1e300, -1e300, 1e300, 1e300},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("v=%g", c.v), func(t *testing.T) {
			got := clampFloat(c.v, c.lo, c.hi)
			if got != c.want {
				t.Errorf("clampFloat(%g,%g,%g) = %g, want %g", c.v, c.lo, c.hi, got, c.want)
			}
		})
	}
}

// ─── getEnvInt / getEnvFloat ─────────────────────────────────────────

func TestRobustness_GetEnvInt_EdgeCases(t *testing.T) {
	const key = "ROBUST_TEST_GETENVINT"
	defer os.Unsetenv(key)

	cases := []struct {
		name     string
		raw      string
		fallback int
		want     int
	}{
		{"空 → fallback", "", 42, 42},
		{"正常整数", "100", 42, 100},
		{"负数", "-50", 42, -50},
		{"零", "0", 42, 0},
		{"带前后空格", "  100  ", 42, 100},
		{"非数字 → fallback", "abc", 42, 42},
		{"十六进制 → fallback", "0x10", 42, 42},
		{"浮点 → fallback", "3.14", 42, 42},
		{"科学计数 → fallback", "1e10", 42, 42},
		{"溢出 MaxInt64", "99999999999999999999", 42, 42},
		{"负溢出", "-99999999999999999999", 42, 42},
		{"NULL 字节", "\x00", 42, 42},
		{"仅 Tab", "\t", 42, 42},
		{"仅空格", " ", 42, 42},
		{"仅换行", "\n", 42, 42},
		{"超长非数字", strings.Repeat("9", 100), 42, 42},
		{"嵌入数字", "1a2b3", 42, 42},
		{"大数字末尾带空格", "1000000 ", 42, 1000000},
		{"完整 MaxInt", "9223372036854775807", 42, math.MaxInt64},
		{"MaxInt+1", "9223372036854775808", 42, 42},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Setenv(key, c.raw)
			got := getEnvInt(key, c.fallback)
			if got != c.want {
				t.Errorf("getEnvInt(%q, %d) = %d, want %d", c.raw, c.fallback, got, c.want)
			}
		})
	}
}

func TestRobustness_GetEnvFloat_EdgeCases(t *testing.T) {
	const key = "ROBUST_TEST_GETENVFLOAT"
	defer os.Unsetenv(key)

	cases := []struct {
		name     string
		raw      string
		fallback float64
		want     float64
	}{
		{"空 → fallback", "", 3.14, 3.14},
		{"正常小数", "3.14", 0, 3.14},
		{"整数", "42", 0, 42.0},
		{"负数", "-3.14", 0, -3.14},
		{"科学计数", "1e10", 0, 1e10},
		{"负科学", "-1e-5", 0, -1e-5},
		{"零", "0", 1, 0},
		{"非数字 → fallback", "abc", 1.0, 1.0},
		{"带空格", "  3.14  ", 0, 3.14},
		{"Inf → +Inf", "Inf", 0, math.Inf(1)},
		{"NaN → NaN", "NaN", 0, 0}, // NaN != NaN; 此处用 math.IsNaN 单独检查
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Setenv(key, c.raw)
			got := getEnvFloat(key, c.fallback)
			if c.name == "NaN → NaN" {
				if !math.IsNaN(got) {
					t.Errorf("getEnvFloat(%q, %g) = %g, want NaN", c.raw, c.fallback, got)
				}
			} else if got != c.want {
				t.Errorf("getEnvFloat(%q, %g) = %g, want %g", c.raw, c.fallback, got, c.want)
			}
		})
	}
}

// ─── parseGoErr ───────────────────────────────────────────────────────

func TestRobustness_CacheTTLForLang(t *testing.T) {
	cases := []struct {
		lang string
		want int
	}{
		{"knowledge", 1800},
		{"browser", 1800},
		{"tcm", 1800},
		{"go", 600},
		{"python", 600},
		{"node", 600},
		{"sh", 600},
		{"", 600},
		{"未知 lang", 600},
		{"GO 大写", 600},                   // case-sensitive
		{"中文 lang", 600},                 // 完全不识别
		{strings.Repeat("a", 1000), 600}, // 超长
	}
	for _, c := range cases {
		t.Run(c.lang, func(t *testing.T) {
			got := cacheTTLForLang(c.lang)
			if got != c.want {
				t.Errorf("cacheTTLForLang(%q) = %d, want %d", c.lang, got, c.want)
			}
		})
	}
}

// ─── looksLikeValidGoTopLevel ────────────────────────────────────────

func TestRobustness_LooksLikeValidGoTopLevel_EdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"   ", false},
		{"   func main() {}", true},
		{"\n\npackage main", true},
		{"type Foo struct {}", true},
		{"var x = 1", true},
		{"const PI = 3.14", true},
		{"import (", true},
		{"// comment", true},
		{"/* block */", true},
		{"x := 1", false}, // 不是顶级声明
		{"fmt.Println()", false},
		{"if x > 0 {", false},
		{"for i := 0; ;", false},
		{"return 1", false},
		{"中文 func", false}, // func 不是开头
		{"Func 大写", false}, // 大写 F 不在白名单
		{"FUNC 大写", false},
		{"superfunc", false},
		{"func() {} lambda 形式", false}, // func 后无空格, 不匹配 "func "
		// NULL injection inputs (no name field)
		struct {
			in   string
			want bool
		}{in: string([]byte{0x00, 'f', 'u', 'n', 'c', ' ', 'm', 'a', 'i', 'n'}), want: false}, // NULL + func main, TrimSpace 不去 NUL, 首字符不以 f 开头
		struct {
			in   string
			want bool
		}{in: "\nfunc main() {}", want: true},
		struct {
			in   string
			want bool
		}{in: string([]byte{0x07}), want: false}, // Bell only
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("in=%q", c.in), func(t *testing.T) {
			got := looksLikeValidGoTopLevel(c.in)
			if got != c.want {
				t.Errorf("looksLikeValidGoTopLevel(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// ─── isTimeoutErr / isTransientError / shouldFallback / pickFallback ──

func TestRobustness_Atoi_EdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"123", 123},
		{"-456", -456},
		{"", 0}, // 解析失败 → 0
		{"abc", 0},
		{"3.14", 0}, // 含点
		{" 10 ", 0}, // 含空格
		{"99999999999999999999", 9223372036854775807}, // strconv.Atoi 溢出返回 MaxInt64 + ErrRange
		{"-1", -1},
		// NULL injection - see TestRobustness_Atoi_NULLInjection
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := atoi(c.in)
			if got != c.want {
				t.Errorf("atoi(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// NULL byte injection (Go source code disallows NUL literal)

func TestRobustness_Atoi_NULLInjection(t *testing.T) {
	cases := []string{
		string([]byte{0x00}),
		string([]byte{'1', 0x00}),
		string([]byte{0x00, '1'}),
		strings.Repeat(string([]byte{0x00}), 10),
	}
	for _, in := range cases {
		t.Run(fmt.Sprintf("len=%d", len(in)), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("atoi panic: %v", r)
				}
			}()
			atoi(in) // not panic
		})
	}
}

func TestRobustness_ExeSuffix(t *testing.T) {
	got := exeSuffix()
	if runtime.GOOS == "windows" {
		if got != ".exe" {
			t.Errorf("windows 下应为 .exe, got %q", got)
		}
	} else {
		if got != "" {
			t.Errorf("非 windows 应为空, got %q", got)
		}
	}
}
