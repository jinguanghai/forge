// ux_test.go: 流式渲染 / 语法高亮 / 语言归一的契约。
//
// 20260925 修复两处丢换行的显示缺陷, 并在此补正向断言(防回归):
//  1. streamRenderer.feed 的 openingFence 分支用 TrimSpace 后的内容输出,
//     丢行尾换行(首行与次行粘连)且丢缩进 —— 分支已删, 改走通用路径输出整行。
//  2. renderMarkdownLine 的列表分支用 TrimSpace(l) 当输出源,
//     丢行尾换行且丢缩进 —— 已改为 indent + 内容 + nl。
//
// 两处都只影响显示, 不影响请求前缀/缓存。
package main

import (
	"strings"
	"testing"
)

// stripANSI 去掉 ANSI 转义序列, 用于断言"上色不改变文本"。
func stripANSI(s string) string { return ansiEscapeRe.ReplaceAllString(s, "") }

func TestUX_NormalizeLang(t *testing.T) {
	cases := []struct{ in, want string }{
		{"go", "go"}, {"golang", "go"}, {" GO ", "go"},
		{"python", "python"}, {"py", "python"},
		{"javascript", "js"}, {"js", "js"}, {"node", "js"},
		{"typescript", "ts"}, {"ts", "ts"},
		{"rust", "rust"}, {"rs", "rust"},
		{"c", "c"}, {"cpp", "c"}, {"c++", "c"}, {"h", "c"},
		{"bash", "sh"}, {"shell", "sh"}, {"sh", "sh"}, {"zsh", "sh"},
		{"sql", "sql"},
		{"json", "json"},
		{"yaml", "yaml"}, {"yml", "yaml"},
		{"markdown", "markdown"}, {"md", "markdown"},
		{"  Unknown  ", "unknown"}, // 未知语言 → 小写去空白
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeLang(c.in); got != c.want {
			t.Errorf("normalizeLang(%q) = %q 期望 %q", c.in, got, c.want)
		}
	}
}

func TestUX_OverlapsAny(t *testing.T) {
	spans := []span{{start: 2, end: 4}, {start: 10, end: 12}}
	cases := []struct {
		idx  []int
		want bool
	}{
		{[]int{0, 5}, true},   // 与 [2,4) 相交
		{[]int{4, 6}, false},  // 紧邻不算相交
		{[]int{1, 3}, true},   // 部分重叠
		{[]int{11, 13}, true}, // 与第二个相交
		{[]int{5, 9}, false},  // 落在间隙
	}
	for _, c := range cases {
		if got := overlapsAny(c.idx, spans); got != c.want {
			t.Errorf("overlapsAny(%v) = %v 期望 %v", c.idx, got, c.want)
		}
	}
	if overlapsAny([]int{0, 100}, nil) {
		t.Error("空 spans 应为 false")
	}
}

func TestUX_HighlightLinePreservesText(t *testing.T) {
	cases := []struct{ line, lang string }{
		{"func main() { // comment", "go"},
		{"x := 1", "go"},
		{"s := \"hello\"", "go"},
		{"def f(): pass", "python"},
		{"SELECT * FROM t", "sql"},
		{"中文注释 // 中文", "go"},
		{"", "go"},
		{"    indented  ", "python"},
		{"a + b * (c - d)", "c"},
	}
	for _, c := range cases {
		got := highlightLine(c.line, c.lang)
		if plain := stripANSI(got); plain != c.line {
			t.Errorf("高亮改变了文本(语言 %s):\n 原文 %q\n 剥离 %q", c.lang, c.line, plain)
		}
	}
}

func TestUX_HighlightLineKeywords(t *testing.T) {
	got := highlightLine("func main", "go")
	if !strings.Contains(got, ansi.cyan) {
		t.Errorf("go 关键字应着青色: %q", got)
	}
	// 无关键字/无字面量的普通行不应着色
	if plain := highlightLine("plain text", "python"); plain != "plain text" {
		t.Errorf("普通行不应着色: %q", plain)
	}
	// 数字着品红
	if got := highlightLine("x := 42", "go"); !strings.Contains(got, ansi.magenta) {
		t.Errorf("数字应着品红: %q", got)
	}
}

func TestUX_StreamRendererSplitEquivalence(t *testing.T) {
	input := "hello world\nsecond line\n"
	whole := newStreamRenderer()
	want := whole.feed(input)

	// 逐字符喂 == 一次喂 (流式分块不得改变输出)
	chunked := newStreamRenderer()
	var sb strings.Builder
	for _, r := range input {
		sb.WriteString(chunked.feed(string(r)))
	}
	if got := sb.String(); got != want {
		t.Errorf("分块不等价:\n 整体 %q\n 分块 %q", want, got)
	}

	// 按不同粒度切分也应等价
	parts := []string{"hel", "lo wor", "ld\nsecond", " line\n"}
	split := newStreamRenderer()
	var sb2 strings.Builder
	for _, p := range parts {
		sb2.WriteString(split.feed(p))
	}
	if got := sb2.String(); got != want {
		t.Errorf("粗粒度分块不等价:\n 整体 %q\n 分块 %q", want, got)
	}
}

func TestUX_StreamRendererTextLine(t *testing.T) {
	r := newStreamRenderer()
	out := r.feed("hello **bold** `code`\n")
	plain := stripANSI(out)
	if plain != "hello bold code\n" {
		t.Errorf("正文渲染结果 = %q", plain)
	}
	if !strings.Contains(out, ansi.bold) || !strings.Contains(out, ansi.yellow) {
		t.Errorf("粗体/行内码应着色: %q", out)
	}
}

func TestUX_StreamRendererCodeBlockContent(t *testing.T) {
	r := newStreamRenderer()
	out := stripANSI(r.feed("```go\nx := 1\n```\n"))
	for _, want := range []string{"```go", "x := 1", "```"} {
		if !strings.Contains(out, want) {
			t.Errorf("代码块缺 %q: %q", want, out)
		}
	}
	// 逐行等价: 早期 openingFence 分支丢行尾换行, 首行代码与次行粘连成 "x := 1y := 2"。
	// 逐字符喂入以排除分块差异, 断言整体输出与输入逐行一致。
	r2 := newStreamRenderer()
	var sb strings.Builder
	for _, ch := range "```go\nx := 1\ny := 2\n```\n" {
		sb.WriteString(r2.feed(string(ch)))
	}
	if got, want := stripANSI(sb.String()), "```go\nx := 1\ny := 2\n```\n"; got != want {
		t.Errorf("代码块逐行输出 = %q, 期望 %q", got, want)
	}
	if !strings.Contains(r.codeLang, "") && r.inCodeBlock {
		t.Error("闭合 fence 后应退出代码块状态")
	}
	if r.inCodeBlock {
		t.Error("闭合 fence 后 inCodeBlock 应为 false")
	}
}

func TestUX_StreamRendererFlush(t *testing.T) {
	r := newStreamRenderer()
	// 无换行 → feed 不输出, 内容留在行缓冲
	if got := r.feed("partial"); got != "" {
		t.Errorf("未换行不应输出, 实际 %q", got)
	}
	// flush 把残留行吐出
	got := stripANSI(r.flush())
	if got != "partial" {
		t.Errorf("flush 结果 = %q", got)
	}
	// flush 幂等: 再 flush 无残留
	if again := r.flush(); again != "" {
		t.Errorf("二次 flush 应为空, 实际 %q", again)
	}
}

func TestUX_StreamRendererUnclosedCodeBlock(t *testing.T) {
	r := newStreamRenderer()
	r.feed("```python\nprint(1)\n")
	if !r.inCodeBlock {
		t.Fatal("未闭合 fence 应仍处于代码块状态")
	}
	// 流结束时 flush 必须收尾, 不得让状态泄漏到下一轮渲染
	_ = r.flush()
	if r.inCodeBlock {
		t.Error("flush 后应退出代码块状态")
	}
}

func TestUX_ReasoningRendererLifecycle(t *testing.T) {
	r := newReasoningRenderer()
	// 从未 feed → close 无输出
	if got := r.close(); got != "" {
		t.Errorf("未开启的推理块 close 应为空, 实际 %q", got)
	}

	r2 := newReasoningRenderer()
	first := r2.feed("line1\nline2")
	if !strings.Contains(first, "推理") {
		t.Errorf("首块应含标题: %q", first)
	}
	if !strings.Contains(stripANSI(first), "line1") {
		t.Errorf("首块应含第一行: %q", first)
	}
	tail := r2.close()
	plainTail := stripANSI(tail)
	if !strings.Contains(plainTail, "line2") {
		t.Errorf("close 应吐出残留行: %q", plainTail)
	}
	if !strings.Contains(plainTail, "└") {
		t.Errorf("close 应闭合边框: %q", plainTail)
	}
	// 二次 close 幂等
	if again := r2.close(); again != "" {
		t.Errorf("二次 close 应为空, 实际 %q", again)
	}
}

func TestUX_ReasoningRendererBlankLine(t *testing.T) {
	r := newReasoningRenderer()
	out := stripANSI(r.feed("\n"))
	if !strings.Contains(out, "│") {
		t.Errorf("空行应渲染为分隔符: %q", out)
	}
}

func TestUX_WrapReasoningLine(t *testing.T) {
	avail := terminalAvail()
	if avail <= 0 {
		t.Fatalf("terminalAvail = %d, 应为正数", avail)
	}
	// 短行原样单段
	short := "短行"
	if segs := wrapReasoningLine(short); len(segs) != 1 || segs[0] != short {
		t.Errorf("短行应原样返回: %v", segs)
	}
	// 长行切分: 段内宽度不超限, 拼接等于原文(不得丢字符)
	long := strings.Repeat("中文字符", 60)
	segs := wrapReasoningLine(long)
	if len(segs) < 2 {
		t.Fatalf("超宽行应切分, 实际 %d 段", len(segs))
	}
	if joined := strings.Join(segs, ""); joined != long {
		t.Errorf("切分丢字符: 原 %d 字, 拼回 %d 字", len([]rune(long)), len([]rune(joined)))
	}
	for i, s := range segs {
		if w := displayWidth(s); w > avail {
			t.Errorf("第 %d 段宽度 %d 超限 %d", i, w, avail)
		}
	}
}

func TestUX_RenderMarkdownLine(t *testing.T) {
	// 标题: 加粗 + 青色, 剥离 "## " 前缀 (主人不编程, 原始 Markdown 符号是噪音)
	head := renderMarkdownLine("## title\n")
	if !strings.Contains(head, ansi.bold) || !strings.Contains(head, ansi.cyan) {
		t.Errorf("标题应加粗青色: %q", head)
	}
	if stripANSI(head) != "title\n" {
		t.Errorf("标题文本应已剥离 ## 前缀: %q", stripANSI(head))
	}
	// # / ### 同口径
	if stripANSI(renderMarkdownLine("# h1\n")) != "h1\n" {
		t.Errorf("# 标题前缀未剥离: %q", stripANSI(renderMarkdownLine("# h1\n")))
	}
	if stripANSI(renderMarkdownLine("### h3\n")) != "h3\n" {
		t.Errorf("### 标题前缀未剥离: %q", stripANSI(renderMarkdownLine("### h3\n")))
	}
	// 引用: 加 ▎ 前缀
	quote := stripANSI(renderMarkdownLine("> quoted\n"))
	if quote != "▎ > quoted\n" {
		t.Errorf("引用渲染 = %q", quote)
	}
	// 列表: 换成圆点符号, 且必须保留行尾换行与缩进
	// (早期实现用 TrimSpace(l) 当输出源 → 列表行与下一行粘连、嵌套缩进丢失)
	if got := stripANSI(renderMarkdownLine("- item\n")); got != "• item\n" {
		t.Errorf("列表渲染 = %q, 期望 %q", got, "• item\n")
	}
	if got := stripANSI(renderMarkdownLine("  - sub\n")); got != "  • sub\n" {
		t.Errorf("嵌套列表渲染 = %q, 期望 %q", got, "  • sub\n")
	}
	if got := stripANSI(renderMarkdownLine("1. one\n")); got != "1. one\n" {
		t.Errorf("有序列表渲染 = %q, 期望 %q", got, "1. one\n")
	}
	// 普通行原样
	if got := renderMarkdownLine("plain\n"); got != "plain\n" {
		t.Errorf("普通行应原样: %q", got)
	}
}

func TestUX_TerminalAvail(t *testing.T) {
	if got := terminalAvail(); got <= 0 {
		t.Errorf("terminalAvail = %d, 应为正数", got)
	}
}
