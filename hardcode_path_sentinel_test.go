package main

// hardcode_path_sentinel_test.go — 生产代码禁写死盘符路径 (T27, 20261008)
//
// 动机: 铸剑炉的根路径解析必须跨平台。实测 13 处生产硬编码让 Linux 端到端跑不通
// —— memory_write.py 在 Linux 上直接 FileNotFoundError (唯一阻塞功能的缺陷)。
// 本哨兵把「以后谁再写 D:\forge 立刻报红」从约定变成死程序判据
// (公理三: 架构约束只有两种存在形式 —— 可执行的判据, 或不存在)。
//
// 范围: 跟踪的 .go (非 _test.go) + .py。测试文件跳过 —— 其中的盘符多是
// 测试输入字符串/历史注释 (T27 B 类: 测试自身的 Windows 假设), 另行处理。
// 逃逸口: FORGE_ROOT 环境变量 + _forge_root()/forgeRoot() 解析器 —— 正确做法永远可达。
//
// 判据自身也有判据: 变异自检 (真违规必抓 + 格式化串/URL 不误报) + 白名单结构断言。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// drivePathRE 匹配盘符路径起始: 字母 + 冒号 + 1~2 个分隔符 + 路径字符。
// 支持 "D:\\forge" (转义) / `D:\forge` (raw) / "D:/x" (正斜杠) 三种源码形态。
// 不用 lookbehind (Go RE2 不支持), 前缀与转义检查在 hasDrivePath 里手工做。
var drivePathRE = regexp.MustCompile(`[A-Za-z]:[\\/]{1,2}[A-Za-z0-9_.]`)

// escapeAfterSep 转义序列首字符: 反斜杠后跟这些字母是控制字符转义, 不是路径。
// 只排 \n \t \r \b —— 它们覆盖了实测的全部误报源 (%q:\n / class A:\n);
// \f 故意保留 (它正是 "D:\forge" 被误写时的形态, 该报)。
var escapeAfterSep = map[byte]bool{'n': true, 't': true, 'r': true, 'b': true}

// hasDrivePath 判断一行是否含盘符路径字面量。
func hasDrivePath(line string) bool {
	for idx := 0; ; {
		loc := drivePathRE.FindStringIndex(line[idx:])
		if loc == nil {
			return false
		}
		s := idx + loc[0]
		match := line[s : idx+loc[1]]
		prev := byte(0)
		if s > 0 {
			prev = line[s-1]
		}
		sep := match[2]
		after := match[len(match)-1]
		okPrefix := !isAlnumOrPct(prev)
		okEscape := sep != '\\' || !escapeAfterSep[after]
		if okPrefix && okEscape {
			return true
		}
		idx = s + 1
	}
}

func isAlnumOrPct(c byte) bool {
	return c == '%' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// hardcodePathExempt 白名单: 文件 → 理由 (结构哨兵钉住每条必须有理由且文件存在)。
var hardcodePathExempt = map[string]string{
	"bench/fetch_datasets.py": "bench 工具: 本地 Windows 跑数据集基准, 非铸剑炉运行时路径",
	"bench/run_dataset.py":    "bench 工具: 同上 (FORGE_EXE 可覆盖)",
	"vps_sync.py":             "本地→VPS 同步工具: 源端固定为 Windows 桌面",
}

// scanHardcodedDrivePaths 返回违规清单 (file:line: text)。抽成函数以便变异自检直接调用。
func scanHardcodedDrivePaths(root string, files []string) []string {
	var viol []string
	for _, f := range files {
		if _, ok := hardcodePathExempt[f]; ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(data), "\n") {
			if hasDrivePath(line) {
				viol = append(viol, fmt.Sprintf("%s:%d: %s", f, i+1, strings.TrimSpace(line)))
			}
		}
	}
	sort.Strings(viol)
	return viol
}

// trackedProdSources 返回跟踪的生产源码 (排除 _test.go)。
func trackedProdSources(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "*.go", "*.py")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files 失败: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f == "" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		files = append(files, f)
	}
	return files
}

// TestNoHardcodedDrivePathInProduction 主判据。
func TestNoHardcodedDrivePathInProduction(t *testing.T) {
	root := testRoot(t)
	files := trackedProdSources(t, root)
	if len(files) < 50 {
		t.Fatalf("扫描面异常小 (%d 个文件) —— git ls-files 可能失效, 判据会静默变空", len(files))
	}
	if viol := scanHardcodedDrivePaths(root, files); len(viol) > 0 {
		t.Errorf("生产代码出现盘符硬编码 %d 处 —— 应走 FORGE_ROOT / _forge_root() / forgeRoot() 解析:\n  %s",
			len(viol), strings.Join(viol, "\n  "))
	}
}

// TestHardcodePathExemptJustified 结构哨兵: 白名单每项必须有理由, 且文件仍存在
// (文件没了就该删条目 —— 白名单只能随实际收缩, 不许无声膨胀)。
func TestHardcodePathExemptJustified(t *testing.T) {
	root := testRoot(t)
	for f, reason := range hardcodePathExempt {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("白名单 %s 缺理由 —— 白名单必须可辩护", f)
		}
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("白名单 %s 已不存在 —— 该条目应删除", f)
		}
	}
}

// TestHardcodeSentinelDetectsViolation 变异自检:
// 真违规三种形态必抓; 格式化串 (%q:\n) / 代码文本 (class A:\n) / URL 不得误报。
func TestHardcodeSentinelDetectsViolation(t *testing.T) {
	dir := t.TempDir()
	bad := "x = r\"D:\\forge\\x\"\ny = \"C:\\\\Users\\\\u\"\nz = 'E:/data/file.txt'\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.py"), []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	if viol := scanHardcodedDrivePaths(dir, []string{"bad.py"}); len(viol) != 3 {
		t.Errorf("变异自检失效: 三种真违规形态应全抓 (raw/转义/正斜杠), 实得 %d: %v", len(viol), viol)
	}
	good := "t.Errorf(\"缺 %q:\\n%s\", a, b)\ns = \"class A:\\n    pass\"\nu = \"https://x.com/a\"\n"
	if err := os.WriteFile(filepath.Join(dir, "good.py"), []byte(good), 0644); err != nil {
		t.Fatal(err)
	}
	if viol := scanHardcodedDrivePaths(dir, []string{"good.py"}); len(viol) != 0 {
		t.Errorf("误报: 格式化串/代码文本/URL 被判为盘符路径: %v", viol)
	}
}
