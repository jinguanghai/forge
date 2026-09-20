package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ── 表达式化强制 + 拒绝权 测试 (20260920, 无剑二期) ────────────────────

func TestNSWExprRenderBasic(t *testing.T) {
	cases := []struct{ in, want string }{
		{"一共 {{35*30}} 元。", "一共 1050 元。"},
		{"{{3*25.5}} 元", "76.5 元"},
		{"实付 {{480*0.7-50}} 元。", "实付 286 元。"},
		{"{{128*365}}", "46720"},
		{"无标记的普通文本。", "无标记的普通文本。"},
		{"{{1+1}} 和 {{2+2}}", "2 和 4"},
		{"{{ 12 / 4 }}", "3"}, // 前后空格应被容忍
	}
	for _, c := range cases {
		got, bad := nswExprRender(c.in)
		if got != c.want {
			t.Errorf("nswExprRender(%q) = %q, want %q", c.in, got, c.want)
		}
		if len(bad) != 0 {
			t.Errorf("nswExprRender(%q) bad = %v, want none", c.in, bad)
		}
	}
}

func TestNSWExprRenderReject(t *testing.T) {
	// 拒绝权: 非纯算式的标记原样保留, 并记入 bad
	cases := []struct{ in, want string }{
		{"一共 {{35元*30}} 元", "一共 {{35元*30}} 元"},
		{"{{}}", "{{}}"},
		{"{{三七二十一}}", "{{三七二十一}}"},
		{"{{1/0}}", "{{1/0}}"},                           // Inf 拒绝
		{"{{9007199254740993}}", "{{9007199254740993}}"}, // 2^53 精度闸
	}
	for _, c := range cases {
		got, bad := nswExprRender(c.in)
		if got != c.want {
			t.Errorf("nswExprRender(%q) = %q, want %q (原样保留)", c.in, got, c.want)
		}
		if len(bad) != 1 {
			t.Errorf("nswExprRender(%q) bad = %v, want 1 项", c.in, bad)
		}
	}
}

func TestNSWExprRenderCodeFence(t *testing.T) {
	// 代码围栏内的 {{}} 是模板语法, 不是算式 → 不替换
	in := "说明如下：\n```\n{{35*30}}\n```\n外面 {{1+1}} 会算。"
	got, bad := nswExprRender(in)
	if !strings.Contains(got, "{{35*30}}") {
		t.Errorf("围栏内标记被误替换: %q", got)
	}
	if !strings.Contains(got, "外面 2 会算。") {
		t.Errorf("围栏外标记未替换: %q", got)
	}
	if len(bad) != 0 {
		t.Errorf("bad = %v, want none", bad)
	}
}

func TestNSWExprPendingTail(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 0},
		{"abc{{x}}", 0},
		{"abc{", 1},
		{"abc{{x", 3},
		{"abc{{x}}d{{", 2},
	}
	for _, c := range cases {
		if got := nswExprPendingTail(c.in); got != c.want {
			t.Errorf("nswExprPendingTail(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNSWExprFilterCrossChunk(t *testing.T) {
	// 标记跨分块: 未闭合时不得提前渲染半个算式
	f := &nswExprFilter{}
	got := f.feed("一共 {{480*0")
	if strings.Contains(got, "{{") || strings.Contains(got, "480") {
		t.Errorf("未闭合标记被提前渲染: %q", got)
	}
	got += f.feed(".7-50}} 元")
	if !strings.Contains(got, "286") {
		t.Errorf("跨块标记未替换: %q", got)
	}
	if got != "一共 286 元" {
		t.Errorf("got %q, want %q", got, "一共 286 元")
	}
	if f.marks != 1 {
		t.Errorf("marks = %d, want 1", f.marks)
	}
	if tail := f.flush(); tail != "" {
		t.Errorf("flush 应为空, got %q", tail)
	}
}

func TestNSWExprFilterFlushUnclosed(t *testing.T) {
	// 流结束仍有未闭合标记 → 原文输出, 不吞掉
	f := &nswExprFilter{}
	_ = f.feed("结果 {{35*")
	tail := f.flush()
	if !strings.Contains(tail, "{{35*") {
		t.Errorf("flush 未吐出残余: %q", tail)
	}
	if tail2 := f.flush(); tail2 != "" {
		t.Errorf("二次 flush 应为空: %q", tail2)
	}
}

func TestNSWExprFilterSplitBrace(t *testing.T) {
	// 孤立 { 分块到达: 不得提前渲染
	f := &nswExprFilter{}
	got := f.feed("值 {")
	if strings.Contains(got, "{") {
		t.Errorf("孤立 { 被提前渲染: %q", got)
	}
	got += f.feed("{1+1}}")
	if got != "值 2" {
		t.Errorf("got %q, want %q", got, "值 2")
	}
}

func TestNSWExprRenderDeterministic(t *testing.T) {
	// 确定性: 同输入逐字节相同 (缓存友好)
	in := "一共 {{35*30}} 元, 另有 {{1/0}} 和 {{3+4}}。"
	first, bad1 := nswExprRender(in)
	for i := 0; i < 200; i++ {
		got, bad := nswExprRender(in)
		if got != first {
			t.Fatalf("第%d次输出不一致: %q vs %q", i, got, first)
		}
		if len(bad) != len(bad1) {
			t.Fatalf("第%d次 bad 不一致: %v vs %v", i, bad, bad1)
		}
	}
}

func TestNSWExprRejectText(t *testing.T) {
	if s := nswExprRejectText("一切正常 {{1+1}}"); s != "" {
		t.Errorf("无非法标记时应返回空, got %q", s)
	}
	s := nswExprRejectText("错的两个 {{35元}} 和 {{1/0}}")
	if s == "" {
		t.Fatal("有非法标记时应返回反馈文本")
	}
	if !strings.Contains(s, "35元") || !strings.Contains(s, "1/0") {
		t.Errorf("反馈文本未列出非法标记: %q", s)
	}
	// 去重: 同一非法标记重复出现只列一次
	dup := nswExprRejectText("{{35元}} 和 {{35元}}")
	if strings.Count(dup, "35元") != 1 {
		t.Errorf("非法标记未去重: %q", dup)
	}
	// 确定性
	for i := 0; i < 50; i++ {
		if nswExprRejectText("错 {{35元}}") != nswExprRejectText("错 {{35元}}") {
			t.Fatal("拒绝权文本不确定")
		}
	}
}

func TestNSWExprConstraintStable(t *testing.T) {
	// 约束文本必须逐字节恒定 (缓存前缀保护)
	if nswExprConstraint != nswExprConstraint {
		t.Fatal("约束文本不稳定")
	}
	if !strings.Contains(nswExprConstraint, "{{") {
		t.Error("约束文本未示范标记写法")
	}
	if strings.Count(nswExprConstraint, "{{") != strings.Count(nswExprConstraint, "}}") {
		t.Error("约束文本里的示例标记不闭合, 会污染解析")
	}
}

// ── 接线哨兵 (教训 20260910: 实现完整但零调用 = 设开关也不生效) ──────────

// TestNSWExprWire_CallSitesExist agent.go 主干接线点存在性
func TestNSWExprWire_CallSitesExist(t *testing.T) {
	agent, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatalf("读 agent.go 失败: %v", err)
	}
	src := string(agent)
	cases := []struct{ call, why string }{
		{"nswExprEnabled()", "表达式化开关未接线 -> FORGE_NSW_EXPR=1 不生效"},
		{"nswExprConstraint", "system 约束段未接线 -> 模型不知道要写标记"},
		{"exprF.feed(", "流式过滤器未接线 -> 用户看到标记原文而非求值结果"},
		{"exprF.flush()", "出口 flush 未接线 -> 未闭合标记残留在终端"},
		{"nswExprRejectText(", "拒绝权未接线 -> 非法标记静默漏过"},
		{"nswExprAudit(", "埋点未接线 -> 表达式化触发率不可测"},
	}
	for _, c := range cases {
		if !strings.Contains(src, c.call) {
			t.Errorf("接线缺失: agent.go 未出现 %q (%s)", c.call, c.why)
		}
	}
}

// TestNSWExprWire_NoOrphanFunctions nosword_expr.go 顶层函数零调用检测
func TestNSWExprWire_NoOrphanFunctions(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	var corpus strings.Builder
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		corpus.Write(b)
		corpus.WriteString("\n")
	}
	all := corpus.String()

	src, err := os.ReadFile("nosword_expr.go")
	if err != nil {
		t.Fatalf("读 nosword_expr.go 失败: %v", err)
	}
	re := regexp.MustCompile(`(?m)^func (\w+)\(`)
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		name := m[1]
		defs := strings.Count(all, "func "+name+"(")
		refs := len(regexp.MustCompile(`\b`+name+`\s*\(`).FindAllString(all, -1)) - defs
		refs += len(regexp.MustCompile(`\b`+name+`\b`).FindAllString(all, -1)) - defs
		if refs <= 0 {
			t.Errorf("孤儿函数(零调用点): %s — 实现完整但未接线, 设开关也不生效", name)
		}
	}
}

// TestNSWExprSystemPromptSwitch 缓存前缀守卫: 开关关时 system 必须与改动前逐字节相同。
// 约束段一旦无条件追加, 会让所有历史会话的前缀缓存失效 (未命中价是命中价的 30 倍)。
func TestNSWExprSystemPromptSwitch(t *testing.T) {
	os.Unsetenv("FORGE_NSW_EXPR")
	off := buildSystemPromptStable("D:/forge", []string{"python"})
	os.Setenv("FORGE_NSW_EXPR", "1")
	defer os.Unsetenv("FORGE_NSW_EXPR")
	on := buildSystemPromptStable("D:/forge", []string{"python"})

	if strings.Contains(off, "numeric_output") || strings.Contains(off, nswExprConstraint) {
		t.Error("开关关时 system 仍含表达式化约束 -> 缓存前缀被污染")
	}
	if !strings.Contains(on, nswExprConstraint) {
		t.Error("开关开时约束段未追加 -> 模型不知道要写标记")
	}
	if off == on {
		t.Error("开关对 system 无影响 -> 接线失效")
	}
	if !strings.HasSuffix(on, nswExprConstraint) {
		t.Error("约束段必须追加在稳定段尾部 (保持前缀结构稳定)")
	}
	if !strings.HasPrefix(on, off) {
		t.Error("开关开时的 system 必须以关时的 system 为前缀 (否则前缀缓存全量失效)")
	}
}

// ─── 横幅状态位可感知性 ───────────────────────

// bannerText 渲染一次启动横幅 (cfg.WorkDir 指向空目录以免读真实告警文件)。
func bannerText(t *testing.T, expr string) string {
	t.Helper()
	t.Setenv("FORGE_NSW_EXPR", expr)
	cfg := &Config{Model: "m", BaseURL: "http://x", WorkDir: t.TempDir()}
	return strings.Join(buildBanner(cfg), "\n")
}

// TestNSWExprBannerVisible 表达式化开关必须可从横幅感知 —— 否则"开着也看不出来",
// 与 FORGE_NOSWORD 当年那条教训同构 (三态输出逐字节相同, 用户无法判断是否生效)。
func TestNSWExprBannerVisible(t *testing.T) {
	off := bannerText(t, "0")
	if strings.Contains(off, "标记求值") {
		t.Fatalf("开关关时横幅不应出现表达式化状态位:\n%s", off)
	}
	on := bannerText(t, "1")
	if !strings.Contains(on, "标记求值") {
		t.Fatalf("开关开时横幅必须出现表达式化状态位:\n%s", on)
	}
	if !strings.Contains(on, "已启用") {
		t.Fatalf("状态位缺少『已启用』字样:\n%s", on)
	}
}

// TestBannerStatusLineFits 状态位不得超宽被 fitWidth 截断成省略号 (截断=信息丢失)。
func TestBannerStatusLineFits(t *testing.T) {
	t.Setenv("FORGE_NOSWORD", "1")
	on := bannerText(t, "1")
	for _, ln := range strings.Split(on, "\n") {
		if strings.Contains(ln, "标记求值") && strings.Contains(ln, "...") {
			t.Fatalf("状态位行被截断:\n%s", ln)
		}
	}
}
