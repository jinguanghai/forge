package main

// workdir_write_sentinel_test.go — 哨兵: 测试代码不得把文件写进仓库工作目录 (20260927)
//
// 由来: vision_test.go 的 TestDetectImagesAbsPath 曾用 os.Getwd() 在仓库根建 _chk/ 存测试图,
// 只 defer os.Remove(pngPath) 删文件、不删目录 —— 每跑一次 go test 就留一个残留目录;
// 20260927 实测该目录内还躺着 11.8MB 旧 exe (测试残留与历史产物混堆)。
//
// 判据: 数据流污染分析 —— os.Getwd() 派生变量为污染源, 经 filepath.Join / 字符串拼接传播,
// 凡"写操作的目标根"引用了污染变量即违规。只读用途 (isolation 哨兵读生产文件核对) 不受影响。
//
// 两轮教训 (均由本文件的自检样本当场抓出, 自检不通过则哨兵拒绝给出绿灯):
//   1) 首版只查"写操作行内是否直接出现 filepath.Join(wd," —— 漏掉两步式
//      (sub := filepath.Join(wd,..) 后再 os.MkdirAll(sub,..)), 而那正是历史缺陷的真实形态。
//   2) 二版做了传播但作用域是整个文件 —— 函数 A 里污染的 sub 会误伤函数 B 里同名的合法 sub
//      (变异测试实测 5 处报警中 2 处是误报)。故改为按顶层函数分块, 各自独立传播。
//
// 范围: 根目录 *_test.go (与 fractal / relation 守卫同范围, 不递归子目录)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var writeOps = []string{"os.MkdirAll(", "os.Mkdir(", "os.WriteFile(", "os.Create("}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// refsIdent 判断 s 是否引用标识符 ident (按词边界, 避免 wd 命中 wdata)。
func refsIdent(s, ident string) bool {
	if ident == "" {
		return false
	}
	idx := 0
	for {
		i := strings.Index(s[idx:], ident)
		if i < 0 {
			return false
		}
		i += idx
		beforeOK := i == 0 || !isIdentChar(s[i-1])
		after := i + len(ident)
		afterOK := after >= len(s) || !isIdentChar(s[after])
		if beforeOK && afterOK {
			return true
		}
		idx = i + 1
	}
}

// splitTopFuncs 按顶层函数切块 (行首 "func ")。污染传播必须限定在函数作用域内,
// 否则同名局部变量会跨函数误伤。
func splitTopFuncs(src string) []string {
	var blocks []string
	var cur []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "func ") {
			if len(cur) > 0 {
				blocks = append(blocks, strings.Join(cur, "\n"))
			}
			cur = []string{l}
			continue
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		blocks = append(blocks, strings.Join(cur, "\n"))
	}
	return blocks
}

// taintedIdents 返回块内被 os.Getwd() 污染的标识符集合 (含经 Join / 拼接传播者)。
func taintedIdents(src string) map[string]bool {
	tainted := map[string]bool{}
	lines := strings.Split(src, "\n")
	// 1) 源头: X, err := os.Getwd()
	for _, line := range lines {
		t := strings.TrimSpace(line)
		i := strings.Index(t, ":=")
		if i < 0 || !strings.Contains(t[i:], "os.Getwd()") {
			continue
		}
		lhs := strings.TrimSpace(strings.Split(t[:i], ",")[0])
		if lhs != "" && !strings.ContainsAny(lhs, " .[](") {
			tainted[lhs] = true
		}
	}
	if len(tainted) == 0 {
		return tainted
	}
	// 2) 传播: Y := filepath.Join(<污染>, ...) / Y := ... + <污染> ...  (迭代到不动点)
	for changed := true; changed; {
		changed = false
		for _, line := range lines {
			t := strings.TrimSpace(line)
			i := strings.Index(t, ":=")
			if i < 0 {
				continue
			}
			lhs := strings.TrimSpace(strings.Split(t[:i], ",")[0])
			if lhs == "" || strings.ContainsAny(lhs, " .[](") || tainted[lhs] {
				continue
			}
			rhs := t[i+2:]
			if !strings.Contains(rhs, "filepath.Join(") && !strings.Contains(rhs, "path.Join(") && !strings.Contains(rhs, "+") {
				continue
			}
			for v := range tainted {
				if refsIdent(rhs, v) {
					tainted[lhs] = true
					changed = true
					break
				}
			}
		}
	}
	return tainted
}

// scanWorkdirWrites 返回违规行: 写操作引用了 os.Getwd() 派生 (污染) 变量。
func scanWorkdirWrites(name, src string) []string {
	var bad []string
	for _, blk := range splitTopFuncs(src) {
		tainted := taintedIdents(blk)
		if len(tainted) == 0 {
			continue
		}
		for _, line := range strings.Split(blk, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "//") {
				continue
			}
			isWrite := false
			for _, op := range writeOps {
				if strings.Contains(t, op) {
					isWrite = true
					break
				}
			}
			if !isWrite {
				continue
			}
			for v := range tainted {
				if refsIdent(t, v) {
					bad = append(bad, name+": "+t)
					break
				}
			}
		}
	}
	return bad
}

func TestNoTestWritesIntoWorkDir(t *testing.T) {
	// 自检: 判据必须同时抓得住两种违规形态、且不误伤三类合法写法。
	// 前两版判据都在这里被当场推翻 —— 自检是哨兵的哨兵。
	samples := []struct {
		name string
		src  []string
		want int
	}{
		{"违规-两步式(历史真实形态)", []string{
			"func x(t *testing.T) {",
			"\twd, _ := os.Getwd()",
			"\tsub := filepath.Join(wd, \"_chk\")",
			"\tos.MkdirAll(sub, 0755)",
			"}",
		}, 1},
		{"违规-内联式", []string{
			"func x(t *testing.T) {",
			"\twd, _ := os.Getwd()",
			"\tos.WriteFile(filepath.Join(wd, \"a.png\"), nil, 0644)",
			"}",
		}, 1},
		{"违规-跨函数同名只算违规那个", []string{
			"func a(t *testing.T) {",
			"\twd, _ := os.Getwd()",
			"\tsub := filepath.Join(wd, \"_chk\")",
			"\tos.MkdirAll(sub, 0755)",
			"}",
			"func b(t *testing.T) {",
			"\tsub := t.TempDir()",
			"\tos.WriteFile(filepath.Join(sub, \"a.png\"), nil, 0644)",
			"}",
		}, 1},
		{"合法-t.TempDir", []string{
			"func x(t *testing.T) {",
			"\tdir := t.TempDir()",
			"\tos.WriteFile(filepath.Join(dir, \"a.png\"), nil, 0644)",
			"}",
		}, 0},
		{"合法-只读Getwd", []string{
			"func x(t *testing.T) {",
			"\twd, _ := os.Getwd()",
			"\tb, _ := os.ReadFile(filepath.Join(wd, \"gate_audit.jsonl\"))",
			"\t_ = b",
			"}",
		}, 0},
		{"合法-词边界(wd 不命中 wdata)", []string{
			"func x(t *testing.T) {",
			"\twd, _ := os.Getwd()",
			"\twdata := []byte{1}",
			"\tos.WriteFile(\"a.bin\", wdata, 0644)",
			"}",
		}, 0},
	}
	for _, s := range samples {
		got := len(scanWorkdirWrites(s.name+"_test.go", strings.Join(s.src, "\n")+"\n"))
		if got != s.want {
			t.Fatalf("哨兵自检失败 [%s]: 违规数=%d, 期望 %d (判据已失效, 拒绝给出绿灯)", s.name, got, s.want)
		}
	}

	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("未找到根目录测试文件: err=%v n=%d", err, len(files))
	}
	var bad []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		bad = append(bad, scanWorkdirWrites(f, string(b))...)
	}
	if len(bad) > 0 {
		t.Fatalf("测试写入落在仓库工作目录 (%d 处) —— 请改用 t.TempDir() 或环境变量隔离路径:\n  %s",
			len(bad), strings.Join(bad, "\n  "))
	}
	t.Logf("扫描 %d 个测试文件, 无工作目录写入", len(files))
}
