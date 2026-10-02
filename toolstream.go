// toolstream.go: tool code streaming display
//
// 工具代码流式显示: 模型边生成 arguments JSON, 这里边解码出 code 字段,
// 按"已收全的整行"实时打印(带高亮), 而不是等整段参数生成完再整块显示。
// 与 displayToolCode 的分工: 本文件负责"过程中显示"; displayToolCode 负责
// "兜底显示"(流式未产出或与最终 code 不一致时才整块打印, 由 streamedCode 去重)。
package main

import (
	"os"
	"strconv"
	"strings"
)

// FORGE_CODE_MAX_LINES 代码块显示行数上限 (0=不限). Default 0.
func codeMaxLines() int {
	if v := os.Getenv("FORGE_CODE_MAX_LINES"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// FORGE_CODE_MAX_COLS 工具输出单行显示列数上限 (0=不限). Default 0.
func codeMaxCols() int {
	if v := os.Getenv("FORGE_CODE_MAX_COLS"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// decodeJSONString 从 s[i] 起解码 JSON 字符串字面量(i 指向起始引号之后),
// 容忍流式分片: 值未闭合时返回已收到的部分。
// 注意: 未知转义/不完整 \u 一律就此收尾 —— 旧实现在这两个分支上 i 不前进,
// 外层 continue 会让循环空转(死循环)。分片 JSON 真会触发, 故一并修正。
func decodeJSONString(s string, i int) string {
	var sb strings.Builder
	for i < len(s) {
		c := s[i]
		if c == '\\' {
			if i+1 >= len(s) {
				break
			}
			n := s[i+1]
			switch n {
			case 'n':
				sb.WriteByte('\n')
				i += 2
			case 't':
				sb.WriteByte('\t')
				i += 2
			case 'r':
				sb.WriteByte('\r')
				i += 2
			case '\\':
				sb.WriteByte('\\')
				i += 2
			case '"':
				sb.WriteByte('"')
				i += 2
			case '/':
				sb.WriteByte('/')
				i += 2
			case 'u':
				if i+6 > len(s) {
					return sb.String()
				}
				v, err := strconv.ParseUint(s[i+2:i+6], 16, 32)
				if err != nil {
					return sb.String()
				}
				sb.WriteRune(rune(v))
				i += 6
			default:
				return sb.String()
			}
			continue
		}
		if c == '"' {
			break
		}
		sb.WriteByte(c)
		i++
	}
	return sb.String()
}

// extractStringField 取 arguments JSON 里顶层字符串字段 key 的值; 容忍分片未闭合。
func extractStringField(s, key string) string {
	pat := `"` + key + `"`
	i := strings.Index(s, pat)
	if i < 0 {
		return ""
	}
	i += len(pat)
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != ':' {
		return ""
	}
	i++
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) || s[i] != '"' {
		return ""
	}
	return decodeJSONString(s, i+1)
}

func extractCodeFromArgs(s string) string { return extractStringField(s, "code") }

// toolCodeLangWaitBytes 首块内容不足该字节数且 lang 未知时, 暂缓输出边框。
//
// 原因(20260922 实测): 工具 schema 的 properties 由 Go map 序列化, 键按字典序
// 输出 → lang 排在 code 之后。若首个分片只含 code, 立刻打印边框会把语言标签
// 钉死成 CODE(实测 e2e: 同一句提示两次运行, 一次显示 PYTHON 一次显示 CODE,
// 取决于 arguments 是否整块到达)。等到 lang 已知或内容够多再开画, 两者兼顾。
const toolCodeLangWaitBytes = 400

// toolCodeStreamer 边收 arguments 分片边逐行实时打印工具代码。
//
// 约束:
//   - 只打印已收全的整行(以 \n 结尾), 半行留缓冲 → 终端不抖动;
//   - 每行经 highlightLine 高亮, 与 displayToolCode 视觉一致;
//   - 首行前打印一次与 displayToolCode 同格式的边框(toolCodeHeader);
//   - code() 返回解码全文, 供 displayToolCode 去重(同段代码不打印两遍)。
type toolCodeStreamer struct {
	raw     strings.Builder
	decoded strings.Builder
	printed int // decoded 中已打印的字节数
	lines   int // 已打印行数
	lang    string
	head    bool // 边框是否已打印
	stop    bool // 触发行数上限后不再打印
}

// feed 追加一片 arguments, 返回本次可打印的文本(可能为空)。
func (t *toolCodeStreamer) feed(arg, lang string) string {
	if arg == "" {
		return ""
	}
	t.raw.WriteString(arg)
	// 语言标签取自 arguments 的 lang 字段(工具名不是语言)
	if l := extractStringField(t.raw.String(), "lang"); l != "" {
		t.lang = l
	}
	cur := extractCodeFromArgs(t.raw.String())
	if len(cur) < t.decoded.Len() || !strings.HasPrefix(cur, t.decoded.String()) {
		return "" // 解码回退(如转义未闭合): 本轮不打印, 交给 flush 与整块兜底
	}
	t.decoded.WriteString(cur[t.decoded.Len():])
	if !t.head && t.lang == "" && t.decoded.Len() < toolCodeLangWaitBytes {
		return "" // 等 lang 随分片到达(见 toolCodeLangWaitBytes 注释)
	}
	return t.drain(false)
}

// flush 输出残余内容(末行未以 \n 结尾也算完整内容)。
func (t *toolCodeStreamer) flush() string { return t.drain(true) }

// code 返回已解码的代码全文(供 displayToolCode 去重)。
func (t *toolCodeStreamer) code() string { return t.decoded.String() }

// drain 把 decoded 里尚未打印的部分渲染为可打印文本。
// final=false 时只输出到最后一个 \n 为止(整行); final=true 时连末行一起输出。
func (t *toolCodeStreamer) drain(final bool) string {
	if t.stop {
		return ""
	}
	full := t.decoded.String()
	if t.printed >= len(full) {
		return ""
	}
	cut := strings.LastIndex(full, "\n")
	if final {
		cut = len(full) - 1
	} else if cut < 0 || cut+1 <= t.printed {
		return ""
	}
	chunk := full[t.printed : cut+1]
	t.printed = cut + 1
	if chunk == "" {
		return ""
	}
	return t.render(chunk)
}

// render 渲染一段(含换行的)代码文本: 首次补边框, 逐行高亮。
func (t *toolCodeStreamer) render(chunk string) string {
	var sb strings.Builder
	if !t.head {
		t.head = true
		sb.WriteString(toolCodeHeader(t.lang))
	}
	maxLines := codeMaxLines()
	for _, l := range strings.Split(strings.TrimSuffix(chunk, "\n"), "\n") {
		if maxLines > 0 && t.lines >= maxLines {
			t.stop = true
			sb.WriteString("  " + dim("│") + " " + dim("... 已达显示上限 (FORGE_CODE_MAX_LINES)") + "\n")
			break
		}
		t.lines++
		sb.WriteString("  " + dim("│") + " " + highlightLine(l, t.lang) + "\n")
	}
	return sb.String()
}
