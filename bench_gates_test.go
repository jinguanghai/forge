package main

// bench_gates_test.go — 性能回归基线 (20260927 批次C / C1)
//
// 缺口(实测): 全仓库 0 个 Benchmark 函数 —— 改了不知道快没快, 退化了不知道。
// 这里只钉住「调用路径」的量级(gate 真实子进程 vs 缓存命中), 不做微基准。
//
// 运行: go test -run '^$' -bench . -benchtime=3x .
// 注意: BenchmarkGateMath 会起真实 gate 子进程(含 python 启动, 单次 ~0.5s),
//       benchtime 用 x 后缀固定次数, 不要用默认的时间后缀。

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func benchForge(b *testing.B) *Forge {
	b.Helper()
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.WorkDir = wd
	cfg.RetryMax = 0
	f := NewForge(wd, cfg)
	f.toolsDir = filepath.Join(wd, ForgeToolsDir)
	b.Cleanup(f.Shutdown)
	return f
}

// BenchmarkGateMath 真实 math gate 调用路径(死边界子进程 + python/sympy)。
func BenchmarkGateMath(b *testing.B) {
	f := benchForge(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := f.forgeGateSkipCache(fmt.Sprintf("%d+%d", i+1, i+2), "math", "", true)
		if !r.OK {
			b.Fatalf("math gate 失败: %+v", r)
		}
	}
}

// BenchmarkGateRegex 真实 regex gate 调用路径(死边界子进程, 无 python 启动开销)。
func BenchmarkGateRegex(b *testing.B) {
	f := benchForge(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := f.forgeGateSkipCache(fmt.Sprintf("^a{%d}$", i+1), "regex", "", true)
		if !r.OK {
			b.Fatalf("regex gate 失败: %+v", r)
		}
	}
}

// BenchmarkCacheHitDispatch 缓存命中路径(纯内存, 无子进程)。
// 与上面两个对比即可看出「缓存省了多少」—— 这是 TTL 与容量决策的依据。
func BenchmarkCacheHitDispatch(b *testing.B) {
	f := benchForge(b)
	if r := f.forgeGate("1+1", "math", ""); !r.OK {
		b.Fatalf("预热失败: %+v", r)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := f.forgeGate("1+1", "math", "")
		if !r.OK || r.CachedAt == 0 {
			b.Fatalf("缓存未命中: %+v", r)
		}
	}
}
