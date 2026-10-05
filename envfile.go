package main

// ─── .env 解析 (自实现, 取代 github.com/joho/godotenv) ──────────────
//
// 动机 (20261004 六西格玛·消除静默错误):
//  1. 编译期第三方依赖归零 —— 本体只依赖标准库, 离线可复现构建。
//  2. godotenv 的调用点是 `_ = godotenv.Load(...)`: 错误被整体丢弃。.env 存在
//     但读不动/语法坏时, 症状表现为「密钥没设置」, 排查先怀疑自己再怀疑依赖。
//  3. 依赖无版本钉: 库升级会静默改变解析语义, 而 .env 位于启动路径上。
//
// 语义与 godotenv v1.5.1 对齐 —— 43 条用例逐项对照实测, 差异 0 (对照程序
// _archive/envcmp_20261004, 已跑 5 轮收敛)。覆盖: 空行/# 注释行/export 前缀、
// KEY=VALUE 首个 = 分割两侧 trim、成对单双引号剥壳、双引号内 \n \r \t \" \\
// 转义、未引号值的行内注释(从右往左找第一个「空白+#」截断)、\$ 字面美元、
// $VAR/${VAR} 展开(只用本文件内已解析变量 —— 实测 Load 亦不查进程环境)、
// 已存在于进程环境的变量不覆盖(空值也算已存在, t.Setenv 优先)。
//
// 有意与 godotenv 不同的两处(方向均为 fail-fast, 不产生静默错误):
//   - 解析错误返回 error 而非丢弃: 调用方决定是否致命
//   - 键名含空白一律拒绝(godotenv 接受 "A B=1"): 含空格的键名是书写错误,
//     报错胜过静默写进一个没人会读的环境变量

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// parseEnvContent 解析 .env 文本为键值对 (纯函数, 不触碰进程环境)。
func parseEnvContent(content string) (map[string]string, error) {
	out := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("第 %d 行缺少 KEY=VALUE: %q", i+1, line)
		}
		key := strings.TrimSpace(line[:eq])
		if !validEnvKey(key) {
			return nil, fmt.Errorf("第 %d 行非法变量名: %q", i+1, key)
		}
		val := strings.TrimSpace(line[eq+1:])
		if isSingleQuoted(val) {
			out[key] = val[1 : len(val)-1] // 单引号 = 字面量, 不展开
			continue
		}
		if !isQuotedEnvValue(val) {
			val = stripInlineComment(val)
		}
		val = unquoteEnvValue(val)
		val = expandEnvRefs(val, out)
		out[key] = val
	}
	return out, nil
}

// loadEnvFile 解析 .env 并注入进程环境 (仅当变量尚未存在)。
// 返回 error = 文件读不到 / 行语法非法 —— 不静默。
func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var b strings.Builder
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return err
	}
	kv, err := parseEnvContent(b.String())
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range kv {
		if _, exists := os.LookupEnv(k); exists {
			continue // 已存在(含显式置空) -> 不覆盖, 与 godotenv.Load 同语义
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("%s: 设置 %s 失败: %w", path, k, err)
		}
	}
	return nil
}

// validEnvKey: 非空且不含空白。'=' 与 NUL 已由分割/读取排除 (os.Setenv 的实际约束)。
func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	return !strings.ContainsAny(k, " \t")
}

func isQuotedEnvValue(v string) bool {
	if len(v) < 2 {
		return false
	}
	return (v[0] == '"' && v[len(v)-1] == '"') || isSingleQuoted(v)
}

func isSingleQuoted(v string) bool {
	return len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\''
}

// stripInlineComment 未加引号值的行内注释: 从右往左找第一个「空白+#」并截断。
func stripInlineComment(v string) string {
	for i := len(v) - 1; i > 0; i-- {
		if v[i] == '#' && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimRight(v[:i], " \t")
		}
	}
	return v
}

func unquoteEnvValue(v string) string {
	if !isQuotedEnvValue(v) {
		return v
	}
	inner := v[1 : len(v)-1]
	if isSingleQuoted(v) {
		return inner
	}
	r := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`)
	return r.Replace(inner)
}

// expandEnvRefs 展开 $VAR / ${VAR}, 只用本文件内已解析的变量 (与 godotenv.Load 一致:
// 实测 A=$HOME/x 展开为空, 不查进程环境)。\$ 是字面美元。
func expandEnvRefs(v string, scope map[string]string) string {
	const litDollar = "\x00LITDOLLAR\x00"
	v = strings.ReplaceAll(v, `\$`, litDollar)
	v = os.Expand(v, func(k string) string { return scope[k] })
	return strings.ReplaceAll(v, litDollar, "$")
}
