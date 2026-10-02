package main

// math_inline_bench_test.go — 内联快速路径的实测耗时 (P0 性能验证)
//
// 用途: 给出「内联 vs spawn」的确定性数字, 供 bench/perf 报告与后续回归对照。
// 运行: go test -run ^$ -bench MathInline -benchtime 5000x .

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mathInlineBenchForge 带真实 toolsDir 的 Forge (内联前置条件见 mathInlineBinaryPresent)。
func mathInlineBenchForge(b *testing.B) *Forge {
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	return &Forge{toolsDir: filepath.Join(wd, ForgeToolsDir)}
}

// BenchmarkMathInlineEval 纯求值 (big.Rat 解析 + 四则/幂/取模)。
func BenchmarkMathInlineEval(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, _, ok := mathInlineEval("(1234*5678+9012)/34-77%13"); !ok {
			b.Fatal("应内联")
		}
	}
}

// BenchmarkMathInlineGate 内联 gate 全路径 (JSON 形态判定 + 求值 + 输出序列化)。
func BenchmarkMathInlineGate(b *testing.B) {
	f := mathInlineBenchForge(b)
	for i := 0; i < b.N; i++ {
		if _, ok := f.mathInlineGate("12345*6789+246/8", time.Now()); !ok {
			b.Fatal("应内联")
		}
	}
}

// BenchmarkMathInlineGateBig 大整数/大指数 (结果 3011 位) —— 确认无爆炸。
func BenchmarkMathInlineGateBig(b *testing.B) {
	f := mathInlineBenchForge(b)
	for i := 0; i < b.N; i++ {
		if _, ok := f.mathInlineGate("2^10000", time.Now()); !ok {
			b.Fatal("应内联")
		}
	}
}
