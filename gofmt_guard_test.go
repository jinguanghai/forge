package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGofmtClean 强制守卫: 根目录主包 .go 必须全部 gofmt 干净 (提交前防线)
func TestGofmtClean(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") {
			files = append(files, name)
		}
	}
	args := append([]string{"-l"}, files...)
	out, err := exec.Command("gofmt", args...).Output()
	if err != nil {
		t.Fatalf("gofmt -l: %v", err)
	}
	list := strings.TrimSpace(string(out))
	if list != "" {
		t.Fatalf("以下文件未 gofmt:\n%s", list)
	}
}

// TestNoIsolatedBlankLines 守卫边界补充 (DMAIC M1): gofmt 不删「代码行之间的孤立空行」。
// 实测 forge.go 曾被批量插入 532 处孤立空行(占非空行约 30%), 文件虚胖 18%,
// 而 gofmt 检查照样全绿 —— 落在守卫的能力边界之外。
//
// 阈值取「绝对数 >80 处」或「比例 >10% 且非空行 ≥200」:
// 全库实测其余文件孤立空行 1~46 处 / 2~8%(LLM 写码时的自然分段风格), 只有批量插入
// 才会同时冲破两条线(forge.go 污染时 532 处/30%)。阈值过高=漏报, 过低=误伤正常风格。
func TestNoIsolatedBlankLines(t *testing.T) {
	const (
		maxIsolated = 80
		maxRatio    = 0.10
		minLines    = 200
	)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		lines := strings.Split(string(raw), "\n")
		isolated, nonBlank := 0, 0
		for i, cur := range lines {
			if strings.TrimSpace(cur) != "" {
				nonBlank++
				continue
			}
			if i == 0 || i == len(lines)-1 {
				continue
			}
			prev, nxt := lines[i-1], lines[i+1]
			if strings.TrimSpace(prev) == "" || strings.TrimSpace(nxt) == "" {
				continue
			}
			ns := strings.TrimLeft(nxt, " \t")
			if strings.HasPrefix(ns, "//") {
				continue // 注释前的空行: 有意分隔
			}
			ps := strings.TrimSpace(prev)
			if strings.HasSuffix(ps, "}") && (strings.HasPrefix(ns, "func ") || strings.HasPrefix(ns, "type ") ||
				strings.HasPrefix(ns, "var ") || strings.HasPrefix(ns, "const ")) {
				continue // 顶层声明之间的空行: gofmt 风格
			}
			isolated++
		}
		if nonBlank == 0 {
			continue
		}
		checked++
		ratio := float64(isolated) / float64(nonBlank)
		if isolated > maxIsolated || (ratio > maxRatio && nonBlank >= minLines) {
			t.Errorf("%s: 孤立空行 %d 处 (占非空行 %.1f%%) 超出上限(%d 处 / %.0f%%) —— gofmt 抓不到, 靠本哨兵",
				e.Name(), isolated, ratio*100, maxIsolated, maxRatio*100)
		}
	}
	if checked == 0 {
		t.Fatal("未检查到任何 .go 文件")
	}
	t.Logf("孤立空行哨兵已检查 %d 个 .go 文件", checked)
}
