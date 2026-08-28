package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ==== 建议2补强: main.go 交互主循环的纯逻辑辅助函数 ====

// captureStdout 临时重定向 os.Stdout 并返回输出
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

// ---- displayWidth ----
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"中文", 4},
		{"a中b", 4},
		{"\x1b[31mred\x1b[0m", 3}, // ANSI 不计宽度
		{"\x1b[1m加粗\x1b[0m", 4},
		{"あいう", 6}, // 假名全角
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ---- padRight ----
func TestPadRight(t *testing.T) {
	if got := padRight("abc", 5); got != "abc  " {
		t.Errorf("padRight(abc,5) = %q", got)
	}
	if got := padRight("中文", 5); got != "中文 " {
		t.Errorf("padRight(中文,5) = %q", got)
	}
	if got := padRight("abcdef", 5); got != "abcdef" {
		t.Errorf("padRight(abcdef,5) = %q, want unchanged", got)
	}
	if got := padRight("", 3); got != "   " {
		t.Errorf("padRight(empty,3) = %q", got)
	}
}

// ---- fitWidth ----
func TestFitWidth(t *testing.T) {
	cases := []struct {
		in   string
		maxW int
		want string
	}{
		{"hello", 10, "hello"},                              // 不超宽原样
		{"hello", 5, "hello"},                               // 恰好
		{"hello world", 5, "hell…"},                         // 截断到 maxW-1 + 省略号
		{"中文测试", 5, "中文…"},                                  // 中文按 2 列
		{"\x1b[32mhello\x1b[0m", 5, "\x1b[32mhello\x1b[0m"}, // ANSI 不占宽
		{"\x1b[32mhello\x1b[0m", 3, "\x1b[32mhe…"},          // ANSI 保留+截断
	}
	for _, c := range cases {
		got := fitWidth(c.in, c.maxW)
		if got != c.want {
			t.Errorf("fitWidth(%q,%d) = %q, want %q", c.in, c.maxW, got, c.want)
		}
		if displayWidth(got) > c.maxW {
			t.Errorf("fitWidth(%q,%d) 结果宽度 %d 超限", c.in, c.maxW, displayWidth(got))
		}
	}
}

// ---- isPathLike ----
func TestIsPathLike(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`C:\foo\bar`, true},
		{`c:\x`, true},
		{`D:`, true},
		{`\\server\share`, true},
		{`relative/path`, false},
		{`abc`, false},
		{`1:2`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := isPathLike(c.in); got != c.want {
			t.Errorf("isPathLike(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---- unbalancedDelimiters ----
func TestUnbalancedDelimiters(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"(", true},
		{"()", false},
		{"(abc", true},
		{"(abc)", false},
		{`"unclosed`, true},
		{`"closed"`, false},
		{`("a(b)")`, false}, // 字符串内括号不计数
		{`{a: "x"}`, false},
		{`foo("bar\")")`, false}, // 转义引号
		{`]`, false},             // 错配视为完整
		{"`tick", true},
		{"a[b]c", false},
		{"def foo():\n    return (1+2", true},
	}
	for _, c := range cases {
		if got := unbalancedDelimiters(c.in); got != c.want {
			t.Errorf("unbalancedDelimiters(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---- escapeHistory / unescapeHistory ----
func TestHistoryEscape(t *testing.T) {
	if got := escapeHistory("a\nb"); got != "a\\nb" {
		t.Errorf("escape = %q", got)
	}
	if got := unescapeHistory("a\\nb"); got != "a\nb" {
		t.Errorf("unescape = %q", got)
	}
	orig := "多行\n输入\t带制表符"
	if unescapeHistory(escapeHistory(orig)) != orig {
		t.Errorf("roundtrip failed")
	}
}

// ---- appendHistory / readHistory ----
func TestHistoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.txt")
	appendHistory(path, "第一行")
	appendHistory(path, "第二行\n带换行")
	got := readHistory(path, 20)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != "第一行" {
		t.Errorf("got[0] = %q", got[0])
	}
	if got[1] != "第二行\n带换行" {
		t.Errorf("got[1] = %q", got[1])
	}
}

func TestHistoryMaxLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hist.txt")
	for i := 0; i < 5; i++ {
		appendHistory(path, string(rune('a'+i)))
	}
	got := readHistory(path, 3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0] != "c" || got[2] != "e" {
		t.Errorf("got = %v, want [c d e]", got)
	}
}

func TestHistoryRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// 写入超过 100KB 触发轮转
	big := strings.Repeat("x", 110*1024)
	appendHistory(path, big)
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf("rotation to .old failed: %v (path=%q exists=%v)", err, path, fileExists(path))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func TestHistoryMissingFile(t *testing.T) {
	if got := readHistory(filepath.Join(t.TempDir(), "nope.txt"), 10); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// ==== handleCommand 交互命令 ====

func newHandleCmdAgent(t *testing.T) (*AgentRunner, *Config, string) {
	t.Helper()
	wd := t.TempDir()
	cfg := &Config{
		APIKey:         "test-key",
		BaseURL:        "http://localhost:9999/v1",
		Model:          "test-model",
		ModelFlash:     "test-flash",
		ModelPro:       "test-pro",
		RouterMode:     "auto",
		MaxTokens:      2048,
		Temperature:    0.7,
		TopP:           1.0,
		RequestTimeout: 10 * time.Second,
		MaxConcurrent:  2,
		CacheMaxSize:   100,
		RetryMax:       1,
		WorkDir:        wd,
	}
	agent, err := NewAgentRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(agent.Shutdown)
	histFile := filepath.Join(wd, "history.txt")
	return agent, cfg, histFile
}

func TestHandleCommand_Empty(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("", agent, cfg, hist, &sr)
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("empty input should produce no output, got %q", out)
	}
}

func TestHandleCommand_Help(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/help", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "铸剑炉 命令:") {
		t.Fatalf("help output missing header: %q", out[:min(200, len(out))])
	}
	if !strings.Contains(out, "/upgrade") {
		t.Fatalf("help output missing commands: %q", out[:min(200, len(out))])
	}
}

func TestHandleCommand_Unknown(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/nosuchcmd", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "未知命令") {
		t.Fatalf("unknown command output = %q", out)
	}
}

func TestHandleCommand_RouterSwitch(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/router pro", agent, cfg, hist, &sr)
	})
	if cfg.RouterMode != "pro" {
		t.Fatalf("RouterMode = %q, want pro", cfg.RouterMode)
	}
	if !strings.Contains(out, "路由模式已切换") {
		t.Fatalf("output = %q", out)
	}
}

func TestHandleCommand_RouterQuery(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/router", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "当前路由") {
		t.Fatalf("query output = %q", out)
	}
}

func TestHandleCommand_RouterInvalid(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/router bogus", agent, cfg, hist, &sr)
	})
	if cfg.RouterMode != "auto" {
		t.Fatalf("RouterMode changed to %q on invalid input", cfg.RouterMode)
	}
	if !strings.Contains(out, "无效模式") {
		t.Fatalf("output = %q", out)
	}
}

func TestHandleCommand_Reasoning(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	defer os.Unsetenv("FORGE_SHOW_REASONING")
	captureStdout(t, func() {
		handleCommand("/reasoning", agent, cfg, hist, &sr)
	})
	if !sr {
		t.Fatal("showReasoning not toggled")
	}
	if os.Getenv("FORGE_SHOW_REASONING") != "true" {
		t.Fatal("env FORGE_SHOW_REASONING not set")
	}
	// 再切一次关闭
	captureStdout(t, func() {
		handleCommand("/reasoning", agent, cfg, hist, &sr)
	})
	if sr {
		t.Fatal("showReasoning not toggled back")
	}
}

func TestHandleCommand_Model(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/model", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "test-model") || !strings.Contains(out, "test-flash") {
		t.Fatalf("model output = %q", out)
	}
}

func TestHandleCommand_History(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	appendHistory(hist, "昨日提问")
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/history", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "昨日提问") {
		t.Fatalf("history output = %q", out)
	}
}

func TestHandleCommand_Tools(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/tools", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "12 gates") || !strings.Contains(out, "python") {
		t.Fatalf("tools output = %q", out[:min(300, len(out))])
	}
	if !strings.Contains(out, "tcm") || !strings.Contains(out, "browser") {
		t.Fatalf("tools output 缺保留 gate: %q", out[:min(300, len(out))])
	}
	if !strings.Contains(out, "builds=") {
		t.Fatalf("tools output missing stats: %q", out)
	}
}

func TestHandleCommand_LastEmpty(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/last", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "暂无工具输出") {
		t.Fatalf("last output = %q", out)
	}
}

func TestHandleCommand_Health(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/health", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "Forge:") || !strings.Contains(out, "LLM:") {
		t.Fatalf("health output = %q", out)
	}
}

func TestHandleCommand_Cache(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/cache", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "DeepSeek 前缀缓存命中") {
		t.Fatalf("cache output = %q", out[:min(200, len(out))])
	}
}

func TestHandleCommand_UnfoldUsage(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() {
		handleCommand("/unfold", agent, cfg, hist, &sr)
	})
	if !strings.Contains(out, "用法: /unfold") {
		t.Fatalf("unfold output = %q", out)
	}
}

// 防止 min 与内置冲突 (Go 1.21+ 有内置 min)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = time.Now // 保持 time 导入 (测试时间相关逻辑预留)
