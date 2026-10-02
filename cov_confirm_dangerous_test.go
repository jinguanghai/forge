package main

// cov_confirm_dangerous_test.go — 危险操作审批门补测 (20260920)
//
// confirmDangerous 是 L3 副作用闸门 (危险代码执行前的人工批准)。此前零覆盖,
// 而它的语义是"默认拒绝"—— 任何非明确批准 (空行/EOF/乱输入/读取失败) 都必须
// 判 deny 并留痕 guard_blocked。这是安全默认值, 必须由死程序判定守住。
//
// 隔离: stdin 用临时文件注入 (绝不从真实终端读取), stderr 落临时文件,
// eventsPath 指向 t.TempDir() (不污染生产 .forge/events.jsonl)。

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdinText 用临时文件替换 os.Stdin, 返回恢复函数 (由 t.Cleanup 兜底)。
func withStdinText(t *testing.T, text string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin_*.txt")
	if err != nil {
		t.Fatalf("创建 stdin 临时文件失败: %v", err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("写入 stdin 失败: %v", err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("回绕 stdin 失败: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = old
		f.Close()
	})
}

// sinkStderr 把 os.Stderr 重定向到临时文件 (避免审批提示刷屏测试输出)。
func sinkStderr(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr_*.log")
	if err != nil {
		t.Fatalf("创建 stderr 临时文件失败: %v", err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() {
		os.Stderr = old
		f.Close()
	})
	return f
}

// isolateApprovalDelegate 把委托档位显式钉成「关闭」。
//
// 为什么必须有: FORGE_DELEGATE 可能被设在 User 级环境 (生产配置 = 直通档 all,
// 见 approval_delegate.go 动机段)。不隔离时, 下面这些「人工审批路径」用例会被
// 委托直通绕过 —— 输入 n 也拿不到 deny, 4 个用例集体假红。被测的是人工路径,
// 就必须先把自动路径关掉 (测试未隔离环境变量 ≠ 代码缺陷)。
func isolateApprovalDelegate(t *testing.T) {
	t.Helper()
	t.Setenv("FORGE_DELEGATE", "")
}

// isolateEventsPath 把事件日志指向临时文件, 返回日志路径。
func isolateEventsPath(t *testing.T) string {
	t.Helper()
	old := eventsPath
	dir := t.TempDir()
	eventsPath = filepath.Join(dir, "events.jsonl")
	t.Cleanup(func() { eventsPath = old })
	return eventsPath
}

func readEventTypes(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取事件日志失败: %v", err)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("事件行非法 JSON: %v (%s)", err, line)
		}
		types = append(types, ev.Type)
	}
	return types
}

// 默认拒绝矩阵: 只有明确批准才放行, 其余一律 deny。
func TestConfirmDangerous_DefaultDenyMatrix(t *testing.T) {
	isolateApprovalDelegate(t)
	sinkStderr(t)
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"小写 y 批准", "y\n", true},
		{"大写 Y 批准", "Y\n", true},
		{"明确 n 拒绝", "n\n", false},
		{"空行默认拒绝", "\n", false},
		{"EOF 默认拒绝", "", false},
		{"乱输入默认拒绝", "也许吧\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			logPath := isolateEventsPath(t)
			withStdinText(t, c.input)
			got := (&Forge{}).confirmDangerous("rm -rf /tmp/x", "destroy", "rm -rf")
			if got != c.want {
				t.Fatalf("输入 %q 应得 %v, 实际 %v", c.input, c.want, got)
			}
			types := readEventTypes(t, logPath)
			want := EvGuardBlocked
			if c.want {
				want = EvApproved
			}
			if len(types) != 1 || types[0] != want {
				t.Fatalf("留痕应为 [%s], 实际 %v", want, types)
			}
		})
	}
}

// ─── 审批提示的信息完整性 (2026-10 安全测试) ─────────────────────
//
// 缺陷: 危险载荷 base64/hex 编码后, 审批提示只显示一串乱码 -> 主人按 y 等于盲批。
// approvalExtraText 负责把编码块解码展示; 下面既测纯函数, 也测 confirmDangerous
// 的接线 —— 「解码了但没接上」在功能上等于没做。

func TestApprovalExtraText_DecodesEmbeddedPayload(t *testing.T) {
	payload := "import os; os.system('rm -rf /tmp/x')"
	b64 := base64.StdEncoding.EncodeToString([]byte(payload))
	hexs := hex.EncodeToString([]byte(payload))
	cases := []struct {
		name         string
		code         string
		hit          string
		wantContains []string
		wantNot      []string
		wantEmpty    bool
	}{
		{
			name:         "base64 载荷: 解码后明文可见",
			code:         `os.system(__import__("base64").b64decode("` + b64 + `"))`,
			hit:          "os.system",
			wantContains: []string{"base64 解码", payload},
		},
		{
			name:         "hex 载荷: 解码后明文可见",
			code:         `os.system(bytes.fromhex("` + hexs + `"))`,
			hit:          "os.system",
			wantContains: []string{"hex 解码", payload},
		},
		{
			name:      "普通危险代码: 无编码块则不追加噪音",
			code:      `os.remove("x.txt")`,
			hit:       "os.remove",
			wantEmpty: true,
		},
		{
			name:         "摘要被截断: 标注代码总长度",
			code:         `x = "` + strings.Repeat("a", 200) + `"`,
			hit:          "x",
			wantContains: []string{"只显示前 120 字"},
			wantNot:      []string{"base64 解码"},
		},
		{
			name:      "边界: 短 base64 块(12 字符)低于阈值, 不解码",
			code:      `f = "cm0gLXJmIC8="`,
			hit:       "f",
			wantEmpty: true,
		},
		// ─── 缺口 1 (同日): 命中落在首 120 字之外, 旧提示里完全看不见 ───
		{
			name:         "命中在截断之外: 展示命中处窗口",
			code:         `pad = "` + strings.Repeat("a", 200) + `"` + "\n" + `os.remove("memory.json")`,
			hit:          "memory.json",
			wantContains: []string{"命中处上下文", "memory.json", "截断处之外"},
		},
		{
			name:         "命中跨截断边界: 标注跨边界",
			code:         `pad = "` + strings.Repeat("a", 90) + `"` + "\n" + `os.remove("memory.json")`,
			hit:          "memory.json",
			wantContains: []string{"命中处上下文", "跨截断边界"},
		},
		{
			name:         "命中在首 120 字内: 不重复展示窗口",
			code:         `os.remove("memory.json")` + "\n" + `pad = "` + strings.Repeat("a", 200) + `"`,
			hit:          "memory.json",
			wantContains: []string{"只显示前 120 字"},
			wantNot:      []string{"命中处上下文"},
		},
		{
			name:         "空 hit: 静默跳过, 不显示错位窗口",
			code:         `pad = "` + strings.Repeat("a", 200) + `"`,
			hit:          "",
			wantContains: []string{"只显示前 120 字"},
			wantNot:      []string{"命中处上下文"},
		},
		{
			name:         "hit 与代码大小写不同: 兜底匹配并展示窗口",
			code:         `pad = "` + strings.Repeat("a", 200) + `"` + "\n" + `os.Remove("Memory.JSON")`,
			hit:          "memory.json",
			wantContains: []string{"命中处上下文", "Memory.JSON"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := approvalExtraText(c.code, c.hit)
			if c.wantEmpty {
				if got != "" {
					t.Fatalf("应无附加警示, 实际: %q", got)
				}
				return
			}
			for _, w := range c.wantContains {
				if !strings.Contains(got, w) {
					t.Fatalf("附加警示缺少 %q, 实际: %q", w, got)
				}
			}
			for _, w := range c.wantNot {
				if strings.Contains(got, w) {
					t.Fatalf("附加警示不应含 %q, 实际: %q", w, got)
				}
			}
		})
	}
}

// 接线判定: approvalExtraText 写了但没接进 confirmDangerous = 等于没写。
func TestConfirmDangerous_ShowsDecodedPayload(t *testing.T) {
	payload := "import os; os.system('rm -rf /tmp/x')"
	b64 := base64.StdEncoding.EncodeToString([]byte(payload))
	code := `os.system(__import__("base64").b64decode("` + b64 + `"))`

	isolateApprovalDelegate(t)
	isolateEventsPath(t)
	withStdinText(t, "n\n")
	errFile := sinkStderr(t)
	if got := (&Forge{}).confirmDangerous(code, "删除", "os.system"); got {
		t.Fatalf("输入 n 应判 deny")
	}
	if err := errFile.Sync(); err != nil {
		t.Fatalf("sync stderr 失败: %v", err)
	}
	if _, err := errFile.Seek(0, 0); err != nil {
		t.Fatalf("回绕 stderr 失败: %v", err)
	}
	b, err := io.ReadAll(errFile)
	if err != nil {
		t.Fatalf("读 stderr 失败: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "base64 解码") || !strings.Contains(out, payload) {
		t.Fatalf("审批提示未展示解码后的真实意图, 实际输出:\n%s", out)
	}
}

// ─── 缺口 1 (同日): 命中处窗口 ────────────────────────────────────────
//
// 缺陷: 摘要只显示前 120 字。实测 205 字代码把 os.remove('memory.json')
// 放在末尾, 提示里完全看不到 —— 只标注"共 N 字"仍是在盲批。
// 修法: 命中处落在首 120 字之外(或跨边界)时, 追加命中位置前后 60 字窗口。

func TestHitContext(t *testing.T) {
	cases := []struct {
		name   string
		norm   string
		hit    string
		wantOK bool
		wantAt int
	}{
		{"精确命中: 返回下标", "aaabbbccc", "bbb", true, 3},
		{"大小写不同: 兜底匹配", "AAAbbbCCC", "bbb", true, 3},
		{"空 hit: 不匹配", "abc", "", false, 0},
		{"hit 不存在: 不匹配", "abc", "zzz", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, at, _, ok := hitContext(c.norm, c.hit, 60)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, 期望 %v", ok, c.wantOK)
			}
			if ok && at != c.wantAt {
				t.Fatalf("at = %d, 期望 %d", at, c.wantAt)
			}
		})
	}
}

// 接线判定: hit 必须真的从 confirmDangerous 传进 approvalExtraText。
// 「窗口函数写了但没接上」在功能上等于没做 —— 与编码块解码同构。
func TestConfirmDangerous_ShowsHitContextBeyondTruncation(t *testing.T) {
	code := `pad = "` + strings.Repeat("a", 200) + `"` + "\n" + `os.remove("memory.json")`
	isolateApprovalDelegate(t)
	isolateEventsPath(t)
	withStdinText(t, "n\n")
	errFile := sinkStderr(t)
	if got := (&Forge{}).confirmDangerous(code, "保护目标", "memory.json"); got {
		t.Fatal("输入 n 应判拒绝")
	}
	if _, err := errFile.Seek(0, 0); err != nil {
		t.Fatalf("回绕 stderr 失败: %v", err)
	}
	b, err := io.ReadAll(errFile)
	if err != nil {
		t.Fatalf("读取 stderr 失败: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, "命中处上下文") {
		t.Fatalf("审批提示未展示命中处窗口 — hit 没接上? 实际输出:\n%s", out)
	}
	if !strings.Contains(out, "memory.json") {
		t.Fatalf("命中处窗口未包含命中串, 实际输出:\n%s", out)
	}
}
