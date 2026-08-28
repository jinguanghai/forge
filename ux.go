package main

import (
	"regexp"
	"strings"
)

// ─── Terminal syntax highlighter ────────────────────────────
//
// Renders code blocks and inline code with terminal ANSI colors.
// Supports: go, python, javascript/node, typescript, rust, c, bash/sh, sql, json, yaml, markdown, diff

// Keyword sets per language
var langKeywords = map[string][]string{
	"go":     {"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for", "func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select", "struct", "switch", "type", "var"},
	"python": {"False", "None", "True", "and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield"},
	"js":     {"async", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do", "else", "export", "extends", "finally", "for", "function", "if", "import", "in", "instanceof", "let", "new", "of", "return", "super", "switch", "this", "throw", "try", "typeof", "var", "void", "while", "with", "yield"},
	"ts":     {"abstract", "as", "async", "await", "break", "case", "catch", "class", "const", "continue", "debugger", "declare", "default", "delete", "do", "else", "enum", "export", "extends", "finally", "for", "function", "if", "implements", "import", "in", "instanceof", "interface", "let", "namespace", "new", "of", "private", "protected", "public", "return", "super", "switch", "this", "throw", "try", "type", "typeof", "var", "void", "while", "with", "yield"},
	"rust":   {"as", "async", "await", "break", "const", "continue", "crate", "dyn", "else", "enum", "extern", "false", "fn", "for", "if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "self", "Self", "static", "struct", "super", "trait", "true", "type", "unsafe", "use", "where", "while"},
	"c":      {"auto", "break", "case", "char", "const", "continue", "default", "do", "double", "else", "enum", "extern", "float", "for", "goto", "if", "int", "long", "register", "return", "short", "signed", "sizeof", "static", "struct", "switch", "typedef", "union", "unsigned", "void", "volatile", "while"},
	"sh":     {"if", "then", "else", "elif", "fi", "case", "esac", "for", "while", "until", "do", "done", "in", "function", "return", "exit", "export", "local", "source", "eval", "exec", "trap", "set", "unset"},
	"sql":    {"SELECT", "FROM", "WHERE", "AND", "OR", "NOT", "IN", "LIKE", "BETWEEN", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "ON", "AS", "GROUP", "BY", "ORDER", "HAVING", "LIMIT", "OFFSET", "INSERT", "INTO", "VALUES", "UPDATE", "SET", "DELETE", "CREATE", "TABLE", "DROP", "ALTER", "INDEX", "UNION", "ALL", "DISTINCT", "COUNT", "SUM", "AVG", "MAX", "MIN", "NULL", "IS", "EXISTS", "CASE", "WHEN", "THEN", "ELSE", "END"},
}

// Pre-compiled keyword regexes per language — compiled once at init,
// not on every highlightLine call (hot path).
var langKeywordRE = func() map[string][]*regexp.Regexp {
	m := make(map[string][]*regexp.Regexp, len(langKeywords))
	for lang, kws := range langKeywords {
		res := make([]*regexp.Regexp, 0, len(kws))
		for _, k := range kws {
			res = append(res, regexp.MustCompile(`\b`+regexp.QuoteMeta(k)+`\b`))
		}
		m[lang] = res
	}
	return m
}()

// Patterns independent of language
var (
	// Strings: "..." '...' `...`
	reStringDQ = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
	reStringSQ = regexp.MustCompile(`'(?:[^'\\]|\\.)*'`)
	reBacktick = regexp.MustCompile("`[^`]*`")
	// Comments
	reLineComment  = regexp.MustCompile(`//.*$|#.*$`)
	reBlockComment = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	// Numbers
	reNumber = regexp.MustCompile(`\b\d+\.?\d*(?:[eE][+-]?\d+)?\b`)
	// Builtin/common types
	reType = regexp.MustCompile(`\b(string|int|bool|float|byte|rune|error|nil|true|false|null|undefined|None|True|False)\b`)
	// Markdown regexes — pre-compiled (renderMarkdownLine is a hot path)
	reBold       = regexp.MustCompile(`\*\*(.+?)\*\*|__(.+?)__`)
	reItalic     = regexp.MustCompile(`(?:\s|^)\*(.+?)\*(?:\s|$)|(?:\s|^)_(.+?)_(?:\s|$)`)
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reNumbered   = regexp.MustCompile(`^(\d+\.\s)`)
)

// span represents a highlighted region in a line
type span struct {
	start, end int
	color      string
}

// highlightLine applies syntax highlighting to a single line of code
func highlightLine(line, lang string) string {
	normalized := normalizeLang(lang)
	var spans []span
	addSpan := func(s span) {
		if s.start >= 0 && s.end > s.start && s.start < len(line) {
			if s.end > len(line) {
				s.end = len(line)
			}
			spans = append(spans, s)
		}
	}
	collectRegex := func(re *regexp.Regexp, color string) {
		for _, m := range re.FindAllStringIndex(line, -1) {
			if !overlapsAny(m, spans) {
				addSpan(span{m[0], m[1], color})
			}
		}
	}

	// block comments first (dim)
	for _, m := range reBlockComment.FindAllStringIndex(line, -1) {
		addSpan(span{m[0], m[1], ansi.dim})
	}
	collectRegex(reStringDQ, ansi.green)
	collectRegex(reStringSQ, ansi.green)
	collectRegex(reBacktick, ansi.yellow)
	collectRegex(reLineComment, ansi.dim)
	collectRegex(reNumber, ansi.magenta)
	if kws, ok := langKeywordRE[normalized]; ok {
		for _, reKW := range kws {
			collectRegex(reKW, ansi.cyan)
		}
	}
	collectRegex(reType, ansi.yellow)

	// Sort spans by start (insertion)
	for i := 0; i < len(spans); i++ {
		for j := i + 1; j < len(spans); j++ {
			if spans[j].start < spans[i].start {
				spans[i], spans[j] = spans[j], spans[i]
			}
		}
	}

	if len(spans) == 0 {
		return line
	}
	var result strings.Builder
	pos := 0
	for _, s := range spans {
		if s.start > pos {
			result.WriteString(line[pos:s.start])
		}
		if s.start >= pos {
			result.WriteString(s.color)
			result.WriteString(line[s.start:s.end])
			result.WriteString(ansi.reset)
			pos = s.end
		}
	}
	if pos < len(line) {
		result.WriteString(line[pos:])
	}
	return result.String()
}

func overlapsAny(idx []int, spans []span) bool {
	for _, s := range spans {
		if idx[0] < s.end && idx[1] > s.start {
			return true
		}
	}
	return false
}

func normalizeLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return "go"
	case "python", "py":
		return "python"
	case "javascript", "js", "node":
		return "js"
	case "typescript", "ts":
		return "ts"
	case "rust", "rs":
		return "rust"
	case "c", "cpp", "c++", "h":
		return "c"
	case "bash", "shell", "sh", "zsh":
		return "sh"
	case "sql":
		return "sql"
	case "json":
		return "json"
	case "yaml", "yml":
		return "yaml"
	case "markdown", "md":
		return "markdown"
	default:
		return strings.ToLower(strings.TrimSpace(lang))
	}
}

// ─── Streaming Markdown/Code renderer ────────────────────────

type streamRenderer struct {
	inCodeBlock  bool
	openingFence bool
	codeLang     string
	codeBuf      strings.Builder
	lineBuf      strings.Builder // for partial lines
}

func newStreamRenderer() *streamRenderer {
	return &streamRenderer{}
}

// feed processes a chunk of text and returns ANSI-rendered output.
// Designed for streaming: it buffers incomplete lines and code blocks.
func (r *streamRenderer) feed(chunk string) string {
	var out strings.Builder
	for _, ch := range chunk {
		r.lineBuf.WriteRune(ch)

		// Check for code block boundaries on newlines only
		if ch == '\n' {
			line := r.lineBuf.String()
			r.lineBuf.Reset()

			trimmed := strings.TrimSpace(line)

			if r.inCodeBlock {
				// Check for closing ```
				if strings.HasPrefix(trimmed, "```") {
					r.inCodeBlock = false
					r.codeBuf.Reset()
					// Write the closing ``` line dimmed
					out.WriteString(ansi.dim)
					out.WriteString(line)
					out.WriteString(ansi.reset)
					r.codeLang = ""
					continue
				}
				// Trailing content on the opening line (```lang code) becomes
				// the first code line instead of being swallowed by the fence.
				if r.openingFence {
					r.openingFence = false
					if rest := strings.TrimSpace(trimmed); rest != "" {
						if r.codeLang != "" {
							out.WriteString(highlightLine(rest, r.codeLang))
						} else {
							out.WriteString(ansi.dim + rest + ansi.reset)
						}
						continue
					}
				}
				// Stream: highlight and output each line immediately.
				// Don't accumulate (avoid double-render on close).
				if r.codeLang != "" {
					out.WriteString(highlightLine(line, r.codeLang))
				} else {
					out.WriteString(ansi.dim)
					out.WriteString(line)
					out.WriteString(ansi.reset)
				}
			} else {
				// Check for opening ```
				if strings.HasPrefix(trimmed, "```") {
					r.inCodeBlock = true
					r.codeLang = strings.TrimPrefix(trimmed, "```")
					r.codeLang = strings.TrimSpace(r.codeLang)
					r.codeBuf.Reset()
					r.openingFence = true

					// Render the opening ``` line with a subtle style
					out.WriteString(ansi.dim)
					out.WriteString(line)
					out.WriteString(ansi.reset)
					continue
				}
				// Regular text with markdown rendering
				out.WriteString(renderMarkdownLine(line))
			}
		}
	}

	// If we have accumulated partial line (no newline yet), check if it's
	// closing a code block inline (rare) or just hold it.
	// For code blocks, the closing ``` always comes after a newline in practice.
	return out.String()
}

// flush drains any remaining buffered content
func (r *streamRenderer) flush() string {
	var out strings.Builder
	if r.lineBuf.Len() > 0 {
		line := r.lineBuf.String()
		r.lineBuf.Reset()
		if r.inCodeBlock {
			if r.codeLang != "" {
				out.WriteString(highlightLine(line, r.codeLang))
			} else {
				out.WriteString(ansi.dim)
				out.WriteString(line)
				out.WriteString(ansi.reset)
			}
		} else {
			out.WriteString(renderMarkdownLine(line))
		}
	}
	// If still in code block at end of stream, close it cleanly
	if r.inCodeBlock {
		r.inCodeBlock = false
		r.codeBuf.Reset()
	}
	return out.String()
}

// renderMarkdownLine renders a single line of markdown text with basic formatting.
func renderMarkdownLine(line string) string {
	l := line

	// Bold: **text** or __text__
	l = reBold.ReplaceAllStringFunc(l, func(m string) string {
		inner := m
		if strings.HasPrefix(m, "**") {
			inner = m[2 : len(m)-2]
		} else {
			inner = m[2 : len(m)-2]
		}
		return ansi.bold + inner + ansi.reset
	})

	// Italic: *text* or _text_ (but not inside words like a_b)
	l = reItalic.ReplaceAllStringFunc(l, func(m string) string {
		inner := strings.Trim(m, " *_")
		// Preserve the match's own leading/trailing whitespace instead of forcing
		// spaces — that would break indented text like "    *note*".
		prefix, suffix := "", ""
		if strings.HasPrefix(m, " ") || strings.HasPrefix(m, "\t") {
			prefix = " "
		}
		if strings.HasSuffix(m, " ") || strings.HasSuffix(m, "\t") {
			suffix = " "
		}
		return prefix + ansi.dim + inner + ansi.reset + suffix
	})

	// Inline code: `code`
	l = reInlineCode.ReplaceAllStringFunc(l, func(m string) string {
		inner := m[1 : len(m)-1]
		return ansi.yellow + inner + ansi.reset
	})

	// ### Headers
	if strings.HasPrefix(l, "### ") {
		return ansi.bold + ansi.cyan + l + ansi.reset
	}
	if strings.HasPrefix(l, "## ") {
		return ansi.bold + ansi.cyan + l + ansi.reset
	}
	if strings.HasPrefix(l, "# ") {
		return ansi.bold + ansi.cyan + l + ansi.reset
	}

	// > Blockquote
	if strings.HasPrefix(strings.TrimSpace(l), "> ") {
		return ansi.dim + "▎ " + l + ansi.reset
	}

	// - List items
	trimmed := strings.TrimSpace(l)
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return ansi.cyan + "• " + ansi.reset + strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
	}

	// 1. Numbered list
	if matched, _ := regexp.MatchString(`^\d+\.\s`, trimmed); matched {
		return reNumbered.ReplaceAllString(trimmed, ansi.cyan+"$1"+ansi.reset)
	}

	return l
}

// ========================== Streaming Reasoning renderer ==========================
// 把模型的推理内容渲染为可读的缩进块 (流式, 按行缓冲)。
// 独立于正文渲染器, 输出到 stderr, 与正文 (stdout) 分离。

type reasoningRenderer struct {
	opened  bool
	closed  bool
	lineBuf strings.Builder
}

func newReasoningRenderer() *reasoningRenderer {
	return &reasoningRenderer{}
}

// feed 增量渲染推理 chunk, 返回 ANSI 文本。
// 首块时输出起始标题; 按换行拆行, 每行加竖线前缀缩进。
func (r *reasoningRenderer) feed(chunk string) string {
	var out strings.Builder
	if !r.opened {
		r.opened = true
		out.WriteString("\n" + ansi.magenta + ansi.bold + "🧠 推理" + ansi.reset + "\n")
	}
	for _, ch := range chunk {
		if ch == '\n' {
			line := r.lineBuf.String()
			r.lineBuf.Reset()
			r.renderLine(line, &out)
		} else {
			r.lineBuf.WriteRune(ch)
		}
	}
	return out.String()
}

// renderLine 渲染单行推理内容。空行保持竖线连续, 非空行超宽时按终端宽度软换行。
func (r *reasoningRenderer) renderLine(line string, out *strings.Builder) {
	if strings.TrimSpace(line) == "" {
		out.WriteString(ansi.cyan + "│" + ansi.reset + "\n")
		return
	}
	for _, seg := range wrapReasoningLine(line) {
		out.WriteString(ansi.cyan + "│ " + ansi.reset + ansi.dim + seg + ansi.reset + "\n")
	}
}

// wrapReasoningLine 按终端显示宽度把超长推理行软换行 (中英文按显示宽度计)。
// 竖线前缀+间隔占 2 列, 续行缩进对齐。
func wrapReasoningLine(line string) []string {
	avail := terminalAvail()
	if avail <= 0 || displayWidth(line) <= avail {
		return []string{line}
	}
	var segs []string
	var sb strings.Builder
	w := 0
	for _, r := range line {
		rw := runeWidth(r)
		if w+rw > avail && w > 0 {
			segs = append(segs, sb.String())
			sb.Reset()
			w = 0
		}
		sb.WriteRune(r)
		w += rw
	}
	if sb.Len() > 0 {
		segs = append(segs, sb.String())
	}
	return segs
}

// terminalAvail 返回推理行可用显示宽度 (自适应终端宽度, 非 tty 时用保守默认)。
func terminalAvail() int {
	w := getTermWidth()
	if w <= 20 {
		return 72
	}
	return w - 3
}

// close 输出收尾边框 (若有内容输出过), 并清空缓冲。仅第一次调用生效。
func (r *reasoningRenderer) close() string {
	if r.closed {
		return ""
	}
	r.closed = true
	if !r.opened {
		return ""
	}
	var out strings.Builder
	if r.lineBuf.Len() > 0 {
		r.renderLine(r.lineBuf.String(), &out)
		r.lineBuf.Reset()
	}
	out.WriteString(ansi.magenta + "└" + strings.Repeat("─", 12) + "┘" + ansi.reset + "\n\n")
	return out.String()
}
