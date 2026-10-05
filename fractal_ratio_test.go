package main

// fractal_ratio_test.go — 判据体量比值上限 (20261004, TODO P3-1)
//
// 背景: TODO P3 结构风险第 1 条记「判据 : 核心 = 2.74 倍 ... 比值本身无判据」。
// 复算发现 2.74 无口径可复现 —— 同一仓库换口径即得 1.20 / 1.80 / 1.92 / 2.01。
// 这正是「无判据」的代价: 数字不可被检验, 也就不可被质疑。本判据把口径与阈值
// 一并钉死, 让「涨到多少该报警」有确定答案。
//
// 口径 (唯一, 可复现): git 跟踪的 *.go —— 测试行(_test.go) / 源码行(其余)。
//   · 只认 git 跟踪: _archive 归档件被忽略(实测含 1226 个测试文件 / 197589 行,
//     混入即把比值稀释到 1.20 —— 口径一混, 结论反向)
//   · 按行计, 不按文件数(20261004 实测: 行数比 1.80 vs 文件数比 2.17)
//
// 20261004 基线: 测试 261 文件 / 43144 行, 源码 120 文件 / 23981 行 → 1.80。
// 上限 2.30 = 基线 +28% 缓冲。超限含义: 判据增速显著超实现, 结构失衡(削减或补实现)。

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// criteriaRatioCeiling 是「判据行数 / 源码行数」的上限 —— 只应改小, 不应改大。
// 改大 = 一次显式决策(必须同批更新本文件注释与 TODO.md 的口径说明),
// 否则比值会静默爬升, 判据退化成台账。
const criteriaRatioCeiling = 2.30

// criteriaRatioProblems 判定的纯函数形态 (便于自检注入变异输入)。
// 返回问题描述列表, 空 = 通过。
func criteriaRatioProblems(testLines, srcLines int) []string {
	var bad []string
	if testLines <= 0 {
		bad = append(bad, fmt.Sprintf("测试行数为 %d —— 口径失效(扫描集合为空?), 不可判绿", testLines))
	}
	if srcLines <= 0 {
		bad = append(bad, fmt.Sprintf("源码行数为 %d —— 口径失效(扫描集合为空?), 不可判绿", srcLines))
		return bad
	}
	if r := float64(testLines) / float64(srcLines); r > criteriaRatioCeiling {
		bad = append(bad, fmt.Sprintf("判据/源码行数比 %.2f 超上限 %.2f (测试 %d 行 / 源码 %d 行)",
			r, criteriaRatioCeiling, testLines, srcLines))
	}
	return bad
}

// criteriaRatioStat 是口径的统计结果。
type criteriaRatioStat struct {
	TestFiles, SrcFiles int
	TestLines, SrcLines int
	ArchiveFiles        int // 口径污染探针: 归档件混入数, 必须为 0
}

// criteriaRatioFileLines 按行计数: 与 python 的 sum(1 for _ in open(f)) 同语义
// (末行无换行也算一行, 空文件算 0 行)。
func criteriaRatioFileLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte("\n"))
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// criteriaRatioCount 按口径统计: 只认 git 跟踪的 *.go。
// git 不可用即返回 error —— fail-closed, 不静默退化成"扫到 0 个文件"的假绿。
func criteriaRatioCount() (criteriaRatioStat, error) {
	var st criteriaRatioStat
	out, err := exec.Command("git", "ls-files", "*.go").Output()
	if err != nil {
		return st, fmt.Errorf("git ls-files *.go 失败(口径失效): %w", err)
	}
	for _, name := range strings.Split(string(out), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, "_archive/") {
			st.ArchiveFiles++
			continue
		}
		n := criteriaRatioFileLines(name)
		if strings.HasSuffix(name, "_test.go") {
			st.TestFiles++
			st.TestLines += n
		} else {
			st.SrcFiles++
			st.SrcLines += n
		}
	}
	return st, nil
}

// TestFractalCriteriaRatio 判据本体: 比值超上限即报红。
// 函数名带 TestFractal 前缀 = 被 defense_system/fractal_check.py 的
// `go test -run TestFractal` 捕获, 每小时随分形守卫一起通电(无需改脚本)。
func TestFractalCriteriaRatio(t *testing.T) {
	if _, err := os.Stat(".git"); err != nil {
		t.Skip("非主仓库环境(无 .git), 跳过")
	}
	st, err := criteriaRatioCount()
	if err != nil {
		t.Fatalf("%v", err)
	}
	// 判据自身的判据: 口径必须真的扫到东西 —— 扫到 0 个文件时比值无从谈起,
	// 若放行则"口径坏掉"与"结构健康"无法区分。
	if st.TestFiles < 50 || st.SrcFiles < 50 {
		t.Fatalf("扫描集合异常: 测试 %d 文件 / 源码 %d 文件 —— git 口径失效?",
			st.TestFiles, st.SrcFiles)
	}
	if st.ArchiveFiles != 0 {
		t.Errorf("口径污染: _archive 归档件混入 %d 个 —— 比值被稀释, 结论失真", st.ArchiveFiles)
	}
	for _, p := range criteriaRatioProblems(st.TestLines, st.SrcLines) {
		t.Error(p)
	}
	t.Logf("判据/源码比值: 测试 %d 文件/%d 行, 源码 %d 文件/%d 行 → %.2f (上限 %.2f)",
		st.TestFiles, st.TestLines, st.SrcFiles, st.SrcLines,
		float64(st.TestLines)/float64(st.SrcLines), criteriaRatioCeiling)
}

// TestFractalCriteriaRatioSharp 判据的判据: 变异输入必报红, 健康输入必放行。
func TestFractalCriteriaRatioSharp(t *testing.T) {
	if got := criteriaRatioProblems(30000, 10000); len(got) != 1 {
		t.Fatalf("超限输入应报 1 条, got %d: %v", len(got), got)
	}
	if got := criteriaRatioProblems(18000, 10000); len(got) != 0 {
		t.Fatalf("健康输入不该报红: %v", got)
	}
	if got := criteriaRatioProblems(23000, 10000); len(got) != 0 {
		t.Fatalf("恰在上限(闭区间上界)不该报红: %v", got)
	}
	// 空口径必须 fail-closed: 扫不到东西 = 假绿, 比超限更危险
	if got := criteriaRatioProblems(0, 0); len(got) != 2 {
		t.Fatalf("空口径应报 2 条(测试/源码各一), got %d: %v", len(got), got)
	}
}
