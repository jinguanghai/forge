package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseEnvContent_Table 钉住 .env 解析语义。
// 期望值来源: 与 godotenv v1.5.1 逐用例对照实测(.forge-temp/envcmp, 43 用例差异 0),
// 非推测。
func TestParseEnvContent_Table(t *testing.T) {
	cases := []struct {
		name string
		body string
		want map[string]string
	}{
		{"basic", "A=1\nB=hello\n", map[string]string{"A": "1", "B": "hello"}},
		{"comment_blank", "# c\n\nA=1\n  # indented\n", map[string]string{"A": "1"}},
		{"export_prefix", "export A=1\nexport B = 2\n", map[string]string{"A": "1", "B": "2"}},
		{"double_quote", "A=\"hi there\"\n", map[string]string{"A": "hi there"}},
		{"single_quote", "A='hi there'\n", map[string]string{"A": "hi there"}},
		{"empty_value", "A=\nB=2\n", map[string]string{"A": "", "B": "2"}},
		{"space_in_value", "A=hello world\n", map[string]string{"A": "hello world"}},
		{"equals_in_value", "A=a=b=c\n", map[string]string{"A": "a=b=c"}},
		{"hash_in_value", "A=abc#def\n", map[string]string{"A": "abc#def"}},
		{"inline_comment", "A=abc # comment\n", map[string]string{"A": "abc"}},
		{"tab_before_hash", "A=abc\t# c\n", map[string]string{"A": "abc"}},
		{"double_hash", "A=a # b # c\n", map[string]string{"A": "a # b"}},
		{"backslash_path", "A=D:\\\\forge\n", map[string]string{"A": "D:\\\\forge"}},
		{"quote_escape", "A=\"line1\\nline2\"\n", map[string]string{"A": "line1\nline2"}},
		{"crlf", "A=1\r\nB=2\r\n", map[string]string{"A": "1", "B": "2"}},
		{"empty_quoted", "A=\"\"\n", map[string]string{"A": ""}},
		{"expand_from_file", "A=1\nB=$A/x\n", map[string]string{"A": "1", "B": "1/x"}},
		{"expand_brace", "A=1\nB=${A}/x\n", map[string]string{"A": "1", "B": "1/x"}},
		{"dollar_literal", "A=\\$HOME\n", map[string]string{"A": "$HOME"}},
		{"single_quote_no_expand", "A='$HOME/x'\n", map[string]string{"A": "$HOME/x"}},
		{"unset_expands_empty", "A=$NOPE/x\n", map[string]string{"A": "/x"}},
		{"quoted_then_comment", "A=\"abc\" # c\n", map[string]string{"A": "abc"}},
		{"utf8_value", "A=\u4e2d\u6587\n", map[string]string{"A": "\u4e2d\u6587"}},
		{"only_spaces_value", "A=   \n", map[string]string{"A": ""}},
	}
	for _, c := range cases {
		got, err := parseEnvContent(c.body)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got[k], v)
			}
		}
	}
}

// TestParseEnvContent_Errors 非法输入必须报错(fail-fast), 不得静默产出半截结果。
func TestParseEnvContent_Errors(t *testing.T) {
	for _, body := range []string{"JUSTAWORD\n", "=1\n", "A B=1\n"} {
		if _, err := parseEnvContent(body); err == nil {
			t.Errorf("%q: 期望报错, 实际通过", body)
		}
	}
}

// TestLoadEnvFile_DoesNotOverrideExisting: 已存在变量(含显式置空)不覆盖 —— 与
// godotenv.Load 同语义, t.Setenv 优先。
func TestLoadEnvFile_DoesNotOverrideExisting(t *testing.T) {
	t.Setenv("FORGE_ENVFILE_KEEP", "keep")
	t.Setenv("FORGE_ENVFILE_EMPTY", "")
	dir := t.TempDir()
	p := filepath.Join(dir, "t.env")
	if err := os.WriteFile(p, []byte("FORGE_ENVFILE_KEEP=other\nFORGE_ENVFILE_EMPTY=x\nFORGE_ENVFILE_NEW=n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := loadEnvFile(p); err != nil {
		t.Fatalf("loadEnvFile: %v", err)
	}
	if v := os.Getenv("FORGE_ENVFILE_KEEP"); v != "keep" {
		t.Errorf("已存在变量被覆盖: %q", v)
	}
	if v, ok := os.LookupEnv("FORGE_ENVFILE_EMPTY"); !ok || v != "" {
		t.Errorf("显式置空变量被覆盖: %q ok=%v", v, ok)
	}
	if v := os.Getenv("FORGE_ENVFILE_NEW"); v != "n" {
		t.Errorf("新变量未注入: %q", v)
	}
	os.Unsetenv("FORGE_ENVFILE_NEW")
}

func TestLoadEnvFile_MissingFileIsError(t *testing.T) {
	if err := loadEnvFile(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Error("文件不存在应返回 error(由调用方决定是否致命)")
	}
}

func TestLoadEnvFile_SyntaxErrorMentionsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.env")
	if err := os.WriteFile(p, []byte("A=1\nBROKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := loadEnvFile(p)
	if err == nil {
		t.Fatal("语法错误应返回 error")
	}
	if !strings.Contains(err.Error(), "bad.env") || !strings.Contains(err.Error(), "第 2 行") {
		t.Errorf("错误信息应含文件名与行号, 实际: %v", err)
	}
}
