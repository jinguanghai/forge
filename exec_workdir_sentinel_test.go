package main

// exec_workdir_sentinel_test.go — 子进程工作目录绑定的结构哨兵 (20261007)
//
// 由来: 20261007 实测 verifyClaim.go 的 runVerification 用裸 exec.Command 且不设
// cmd.Dir —— 全部 gate 子进程都经 newCmd 绑定 workDir, 它是唯一绕过统一入口的命令。
// 从仓库外启动时 git/go 全数失败 (实测 rc=128/128/1), 错误文本被当作"模型虚报"的
// 反证注入上下文, 反过来诱导模型去修正并不存在的错误。
//
// 三层判据分工 (缺一层就有盲区):
//   workdir_binding_test.go   TestNewCmdBindsWorkDir        — 钉住统一入口 newCmd 本身
//   verify_claim_cwd_test.go  行为判据                      — 钉住 runVerification 这一个点
//   本文件                    结构判据                      — 钉住"整个包", 新增裸调用必报红
//
// 判据: 每个 exec.Command* 调用点必须在同函数体内 (或紧随其后的几行内) 出现
// `.Dir =` / `Dir:` 绑定; 否则必须登记进 execWorkdirExempt 并写明理由。
// 豁免只接受一类理由: 命令与全部路径参数均为绝对路径, 进程 cwd 不参与解析。

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// execWorkdirExempt: 免绑定调用点, 键 = "文件名|调用行文本(去缩进)"。
// 新增条目 = 明确声明"该命令与进程 cwd 无关"; 缺理由的条目等于把盲区写进台账。
var execWorkdirExempt = map[string]string{
	`asr.go|cmd := exec.CommandContext(ctx, "python", script, path)`:                                              "script=asrToolDir() 绝对路径; path=调用方音频绝对路径",
	`asr.go|cmd := exec.CommandContext(ctx, "python", script, "--seconds", "0")`:                                  "script=asrToolDir() 绝对路径, 无 cwd 依赖",
	`forge_self_deploy.go|cmd := exec.CommandContext(ctx, exePath, "--version")`:                                  "exePath=就位产物绝对路径",
	`main_startup.go|senseCmd := exec.Command(pythonCommand(), senseScript)`:                                      "senseScript=filepath.Join(cfg.WorkDir, \"sense.py\") 绝对路径; pythonCommand()=PATH 探测 (T25)",
	`proc_guard_windows.go|tk := exec.CommandContext(killCtx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))`: "系统工具 + PID 参数, 无 cwd 依赖",
	`tts.go|cmd := exec.Command("python", scriptFile, textFile, voice, out)`:                                      "scriptFile/textFile/out 均 os.TempDir() 绝对路径",
}

// scanExecNoWorkdir 返回 src 中"未绑定工作目录且未豁免"的 exec.Command 调用点。
func scanExecNoWorkdir(file, src string) []string {
	lines := strings.Split(src, "\n")

	// 顶层函数块 (以行首 "func " 起, 花括号深度归零止)。
	type block struct{ lo, hi int }
	var blocks []block
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "func ") {
			continue
		}
		depth, started, j := 0, false, i
		for ; j < len(lines); j++ {
			depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
			if strings.Contains(lines[j], "{") {
				started = true
			}
			if started && depth <= 0 {
				break
			}
		}
		blocks = append(blocks, block{i, j})
		i = j
	}

	dirRe := regexp.MustCompile(`\.Dir\s*=|\bDir:\s`)
	hasDir := func(lo, hi int) bool {
		if lo < 0 {
			lo = 0
		}
		if hi >= len(lines) {
			hi = len(lines) - 1
		}
		for k := lo; k <= hi; k++ {
			if dirRe.MatchString(lines[k]) {
				return true
			}
		}
		return false
	}

	var bad []string
	for i, l := range lines {
		if !strings.Contains(l, "exec.Command") || strings.HasPrefix(strings.TrimSpace(l), "//") {
			continue
		}
		// 紧随其后的绑定 (var 块内函数字面量走这条: 无顶层 func 归属)。
		bound := hasDir(i, i+5)
		for _, b := range blocks {
			if i >= b.lo && i <= b.hi && hasDir(b.lo, b.hi) {
				bound = true
			}
		}
		if bound {
			continue
		}
		if _, ok := execWorkdirExempt[file+"|"+strings.TrimSpace(l)]; ok {
			continue
		}
		bad = append(bad, fmt.Sprintf("%s:%d %s", file, i+1, strings.TrimSpace(l)))
	}
	return bad
}

func TestExecCommandBindsWorkDir(t *testing.T) {
	// 判据自身的判据: 扫描器必须能区分绑定/未绑定, 否则绿灯无意义。
	samples := []struct {
		name string
		src  string
		want int
	}{
		{"绑定-cmd.Dir", "func f() {\n\tcmd := exec.Command(\"go\", \"vet\", \".\")\n\tcmd.Dir = wd\n\t_ = cmd\n}\n", 0},
		{"未绑定", "func f() {\n\tcmd := exec.Command(\"go\", \"vet\", \".\")\n\t_ = cmd\n}\n", 1},
		{"var块函数字面量-紧随绑定", "var (\n\tspawn = func(p string) error {\n\t\tcmd := exec.Command(p)\n\t\tcmd.Dir = wd\n\t\treturn nil\n\t}\n)\n", 0},
		{"注释不算", "// exec.Command(\"go\") 未绑定\nfunc f() {}\n", 0},
	}
	for _, s := range samples {
		if got := len(scanExecNoWorkdir("sample.go", s.src)); got != s.want {
			t.Fatalf("哨兵自检失败 [%s]: 违规数=%d, 期望 %d (判据已失效, 拒绝给出绿灯)", s.name, got, s.want)
		}
	}

	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("未找到根目录 Go 文件: err=%v n=%d", err, len(files))
	}
	var bad []string
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		scanned++
		bad = append(bad, scanExecNoWorkdir(f, string(b))...)
	}
	if len(bad) > 0 {
		t.Fatalf("发现未绑定工作目录的 exec.Command (%d 处) —— 请设 cmd.Dir, 或登记 execWorkdirExempt 并写明理由:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
	t.Logf("扫描 %d 个生产 Go 文件: exec.Command 全部已绑定或显式豁免", scanned)
}
