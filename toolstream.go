// toolstream.go: tool code streaming display
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// FORGE_CODE_MAX_LINES (0=unlimited). Default 0.
func codeMaxLines() int {
	if v := os.Getenv("FORGE_CODE_MAX_LINES"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

func extractCodeFromArgs(s string) string {
	i := strings.Index(s, `"code"`)
	if i < 0 {
		return ""
	}
	i += len(`"code"`)
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
	i++
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
				if i+6 <= len(s) {
					if v, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
						sb.WriteRune(rune(v))
						i += 6
					} else {
						break
					}
				} else {
					break
				}
			default:
				break
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

type toolCodeStreamer struct {
	raw     strings.Builder
	decoded strings.Builder
}

func (t *toolCodeStreamer) feed(arg string) string {
	if arg == "" {
		return ""
	}
	t.raw.WriteString(arg)
	cur := extractCodeFromArgs(t.raw.String())
	if len(cur) > t.decoded.Len() && strings.HasPrefix(cur, t.decoded.String()) {
		delta := cur[t.decoded.Len():]
		t.decoded.WriteString(delta)
		return delta
	}
	return ""
}

func (t *toolCodeStreamer) flush() {
	if t.decoded.Len() > 0 {
		fmt.Fprintf(os.Stderr, "  %s %s\n", dim("|"), t.decoded.String())
	}
}

// outMaxLines returns FORGE_OUT_MAX_LINES (0=unlimited). Default 0 (show all output lines).
func outMaxLines() int {
	if v := os.Getenv("FORGE_OUT_MAX_LINES"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// outLineMax returns FORGE_OUT_LINE_MAX (0=no truncate per line). Default 0.
func outLineMax() int {
	if v := os.Getenv("FORGE_OUT_LINE_MAX"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}
