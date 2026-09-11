package main

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestNSWCrossCases 与标准语义对拍 (黄金样本 nosword_cross_cases.txt)。
//
// 样本由独立实现 (Python eval, ^ -> **) 生成, 每行 "表达式\t标准值"。
// 判据: 错值必须为 0 —— 漏算可接受 (宁漏勿误), 错值不可接受 (污染 LLM 上下文)。
// 数值按相对 1e-9 比较, 吸收 12 位有效数字的格式化差异。
func TestNSWCrossCases(t *testing.T) {
	data, err := os.ReadFile("nosword_cross_cases.txt")
	if err != nil {
		t.Skipf("对拍样本缺失: %v", err)
	}
	var okN, missN, wrongN int
	var wrongEx []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		want, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			t.Fatalf("样本格式错误: %q", line)
		}
		got, ok := nswEval(parts[0])
		if !ok {
			missN++
			continue
		}
		g, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Fatalf("求值输出不可解析: %q -> %q", parts[0], got)
		}
		if math.Abs(g-want) > math.Max(1e-9, math.Abs(want)*1e-9) {
			wrongN++
			if len(wrongEx) < 20 {
				wrongEx = append(wrongEx, fmt.Sprintf("%s => got %s want %s", parts[0], got, parts[1]))
			}
			continue
		}
		okN++
	}
	t.Logf("对拍 %d 例: 通过=%d 漏算=%d 错值=%d", okN+missN+wrongN, okN, missN, wrongN)
	for _, e := range wrongEx {
		t.Errorf("错值: %s", e)
	}
}
