package main

// cov_gaps_more_test.go — 覆盖率缺口补测 (跨平台, 20261001)
//
// 针对 go tool cover 报出的零/低覆盖函数补齐判定。全部为纯函数或落在 t.TempDir,
// 不触网、不碰真实控制台、不写仓库文件。

import (
	"encoding/binary"
	"os/exec"
	"strings"
	"testing"
)

// ---- audit_schema.go: auditEventRequired ----

// 必填字段集必须与注册表逐字段一致 —— 两处漂移即审计契约失效。
func TestCovGap_AuditEventRequired_MatchesRegistry(t *testing.T) {
	if len(auditEvents) == 0 {
		t.Fatal("审计事件注册表为空")
	}
	for _, e := range auditEvents {
		got := auditEventRequired(e.Name)
		if got == nil {
			t.Fatalf("已登记事件 %q 查不到必填字段", e.Name)
		}
		if len(got) != len(e.Required) {
			t.Fatalf("事件 %q 字段数不符: got %d, want %d", e.Name, len(got), len(e.Required))
		}
		for _, f := range e.Required {
			if !got[f] {
				t.Fatalf("事件 %q 缺必填字段 %q", e.Name, f)
			}
		}
	}
	if got := auditEventRequired("__no_such_event__"); got != nil {
		t.Fatalf("未登记事件应返回 nil, 实际 %v", got)
	}
}

// ---- forge_env.go: isANSINoise ----

func TestCovGap_IsANSINoise(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},               // 空串无可见内容
		{"\x1b[0m\x1b[0m", true}, // 纯重置序列
		{"\x1b[38;5;240m", true}, // 纯配色序列
		{"abc", false},           // 有可见内容
		{"\x1b[0", false},        // 残缺序列: 不视作"无信息"
		{"\x1b[0m ", false},      // 空格也是可见内容
		{"\x1b[0m文", false},      // 宽字符可见
	}
	for _, c := range cases {
		if got := isANSINoise(c.in); got != c.want {
			t.Errorf("isANSINoise(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ---- vision.go: checkWebPContainer ----

func TestCovGap_CheckWebPContainer(t *testing.T) {
	build := func(fourcc string, declared int) []byte {
		b := make([]byte, 24)
		copy(b[0:4], "RIFF")
		binary.LittleEndian.PutUint32(b[4:8], uint32(declared))
		copy(b[8:12], "WEBP")
		copy(b[12:16], fourcc)
		return b
	}
	for _, cc := range []string{"VP8 ", "VP8L", "VP8X"} {
		if err := checkWebPContainer(build(cc, 16)); err != nil {
			t.Fatalf("合法 WebP (%q) 应通过, 实际 %v", cc, err)
		}
	}
	if err := checkWebPContainer(make([]byte, 19)); err == nil {
		t.Fatal("过短文件应报错")
	} else if !strings.Contains(err.Error(), "过短") {
		t.Fatalf("过短错误信息不符: %v", err)
	}
	if err := checkWebPContainer(build("VP8X", 9999)); err == nil {
		t.Fatal("RIFF 长度不自洽应报错")
	} else if !strings.Contains(err.Error(), "不自洽") {
		t.Fatalf("长度错误信息不符: %v", err)
	}
	// 改名的非图 RIFF 容器 (WAV: fmt chunk) 必须被拒
	if err := checkWebPContainer(build("fmt ", 16)); err == nil {
		t.Fatal("非 VP8 chunk 应报错")
	} else if !strings.Contains(err.Error(), "VP8") {
		t.Fatalf("fourcc 错误信息不符: %v", err)
	}
}

// ---- forge_env.go: findGoCommand ----

// PATH 上已有 go 时必须直接返回该路径 —— 绝不进入自动下载分支。
func TestCovGap_FindGoCommand_UsesPath(t *testing.T) {
	want, err := exec.LookPath("go")
	if err != nil {
		t.Skip("PATH 上无 go 工具链, 跳过 (避免触发自动下载)")
	}
	f := NewForge(t.TempDir(), &Config{})
	got, err := f.findGoCommand()
	if err != nil {
		t.Fatalf("findGoCommand 失败: %v", err)
	}
	if got != want {
		t.Fatalf("应返回 PATH 上的 go: got %q, want %q", got, want)
	}
}

// ---- session.go: touchSession ----

func TestCovGap_TouchSession_UpdatesMeta(t *testing.T) {
	wd := t.TempDir()

	// legacy 模式 (无当前会话): 直接返回, 不得创建任何文件
	setCurrentSession("")
	t.Cleanup(func() { setCurrentSession("") })
	touchSession(wd, "不该出现")

	id := "s20261001_120000"
	setCurrentSession(id)
	t.Cleanup(func() { setCurrentSession("") })
	if ensureSessionDir(wd, id) == "" {
		t.Fatal("创建会话目录失败")
	}
	orig := Session{ID: id, Title: "旧标题", CreatedAt: "t0", UpdatedAt: "t0"}
	if err := saveSessionMeta(wd, &orig); err != nil {
		t.Fatalf("写入会话元数据失败: %v", err)
	}

	touchSession(wd, "新标题")

	list, err := listSessions(wd)
	if err != nil {
		t.Fatalf("读会话列表失败: %v", err)
	}
	var found *Session
	for i := range list {
		if list[i].ID == id {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatalf("会话 %s 丢失, 实际 %+v", id, list)
	}
	if found.Title != "新标题" {
		t.Fatalf("标题未更新: %q", found.Title)
	}
	if found.UpdatedAt == "t0" || found.UpdatedAt == "" {
		t.Fatalf("updated_at 未刷新: %q", found.UpdatedAt)
	}
}

// ---- commands_session.go: cmdStats / cmdLast ----

func TestCovGap_CmdStats_NoGateRecords(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false
	out := captureStdout(t, func() { cmdStats(agent, cfg, nil, hist, &sr, "/stats") })
	if !strings.Contains(out, "会话统计") {
		t.Fatalf("缺少统计标题: %q", out)
	}
	if !strings.Contains(out, "forge: builds=") {
		t.Fatalf("缺少 forge 统计行: %q", out)
	}
	if !strings.Contains(out, "gates:") {
		t.Fatalf("缺少 gates 行: %q", out)
	}
}

func TestCovGap_CmdLast_EmptyAndTruncate(t *testing.T) {
	agent, cfg, hist := newHandleCmdAgent(t)
	sr := false

	agent.lastOut = ""
	out := captureStdout(t, func() { cmdLast(agent, cfg, nil, hist, &sr, "/last") })
	if !strings.Contains(out, "暂无工具输出") {
		t.Fatalf("空输出应给出提示: %q", out)
	}

	long := strings.Repeat("x", 200)
	agent.lastOut = long
	out = captureStdout(t, func() { cmdLast(agent, cfg, nil, hist, &sr, "/last") })
	if !strings.Contains(out, "最近一次工具输出") {
		t.Fatalf("缺标题: %q", out)
	}
	if !strings.Contains(out, strings.Repeat("x", 160)+"...") {
		t.Fatalf("超长行应被截断为 160 字符 + 省略号: %q", out)
	}
	if strings.Contains(out, strings.Repeat("x", 161)) {
		t.Fatalf("截断失效, 出现 161 个连续字符: %q", out)
	}
}

// ---- main_interactive.go: resolveVoiceInput ----

func TestCovGap_ResolveVoiceInput_PassThrough(t *testing.T) {
	const in = "普通文本输入"
	got, isVoice, ok := resolveVoiceInput(in)
	if got != in || isVoice || !ok {
		t.Fatalf("非 /听 输入应原样透传: got=%q isVoice=%v ok=%v", got, isVoice, ok)
	}
}

// ---- config.go: DefaultConfig ----

func TestCovGap_DefaultConfig_Sane(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("DefaultConfig 返回 nil")
	}
	if cfg.WorkDir == "" {
		t.Fatal("WorkDir 不得为空 (cwd 取不到时应回退 '.')")
	}
	if cfg.BaseURL == "" || cfg.Model == "" || cfg.ModelFlash == "" || cfg.ModelVision == "" {
		t.Fatalf("关键默认值不得为空: %+v", cfg)
	}
	if cfg.RouterMode == "" {
		t.Fatal("RouterMode 不得为空")
	}
}
