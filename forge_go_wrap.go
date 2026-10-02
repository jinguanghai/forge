package main

import "strings"

// looksLikeFullGoProgram 判定代码是「完整程序」(含顶层声明) 还是「函数体片段」。
// 判据: 存在一行「无前导空白」且以顶层声明关键字开头 —— 片段是语句序列,
// 语句必然缩进在 func main 内; 完整程序的顶层声明必然顶格。
func looksLikeFullGoProgram(code string) bool {
	kw := []string{"func ", "type ", "var ", "const ", "import ", "import("}
	for _, ln := range strings.Split(code, "\n") {
		t := strings.TrimLeft(ln, " \t")
		if t == "" || len(t) != len(ln) {
			continue
		}
		for _, k := range kw {
			if strings.HasPrefix(t, k) {
				return true
			}
		}
	}
	return false
}

// hasPackageClause 判定代码是否自带 package 声明(允许前导空白与注释)。
//
// 旧判据 strings.Contains(code, "package main") 是子串匹配, 实测失效:
// 代码里任何位置出现 "package main"(字符串字面量/注释/变量名)即被判定
// "自带声明" → 跳过包装 → 裸代码直写 main.go → 编译期报
// "main.go:1:1: expected 'package', found 'import'"。
// 实测 596 次 go 调用 125 次失败, 其中 69 次(55.2%)是这一条。
func hasPackageClause(code string) bool {
	rest := code
	for {
		rest = strings.TrimLeft(rest, " \t\r\n")
		switch {
		case strings.HasPrefix(rest, "//"):
			i := strings.IndexByte(rest, '\n')
			if i < 0 {
				return false
			}
			rest = rest[i+1:]
		case strings.HasPrefix(rest, "/*"):
			i := strings.Index(rest, "*/")
			if i < 0 {
				return false
			}
			rest = rest[i+2:]
		default:
			return strings.HasPrefix(rest, "package ")
		}
	}
}
