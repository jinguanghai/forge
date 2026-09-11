package main

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// TestNswFmtNumFixedPrecision 定点精度回归 (20260911 缺陷G)。
// 背景: nswFmtNum 对 |v| < 1e-4 走 'g' 会切科学计数法, 原实现用 'f',-1 转定点,
// 但 -1 会吐 float64 全位尾数 (1/30000 -> 0.000033333333333333335, 末 5 位是
// 浮点残差不是真值), 把不可靠位数伪装成有效数字污染 LLM 上下文 —— 与缺陷D/F 同源。
// 修复: 按十进制指数反推小数位, 与 'g',12 同口径收敛到 12 位有效数字。
func TestNswFmtNumFixedPrecision(t *testing.T) {
	cases := []struct {
		expr string
		v    float64
		want string
	}{
		{"1/6", 1.0 / 6, "0.166666666667"},
		{"1/30000", 1.0 / 30000, "0.0000333333333333"}, // 修复前 0.000033333333333333335 (17位)
		{"1/10001", 1.0 / 10001, "0.0000999900009999"}, // 修复前 0.00009999000099990002 (16位)
		{"12/17", 12.0 / 17, "0.705882352941"},
		{"13/181", 13.0 / 181, "0.0718232044199"},
		{"14/27", 14.0 / 27, "0.518518518519"},
		{"36726/88233", 36726.0 / 88233, "0.416238822209"},
		{"74318/88233", 74318.0 / 88233, "0.842292566273"},
		{"1/3", 1.0 / 3, "0.333333333333"},
		{"1/7", 1.0 / 7, "0.142857142857"},
		{"2/3", 2.0 / 3, "0.666666666667"},
		{"9989/1199739", 9989.0 / 1199739, "0.00832597756679"},
		{"0/1199739", 0.0 / 1199739, "0"},
	}
	for _, c := range cases {
		if got := nswFmtNum(c.v); got != c.want {
			t.Errorf("%s = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestNswFmtNumBoundaries 边界: 噪声闸 / 科学计数法切换点 / 大数回退 / 去尾零 / 负零。
func TestNswFmtNumBoundaries(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		want string
	}{
		{"噪声闸下界 1e-16 归零", 1e-16, "0"},
		{"噪声闸上界 1e-15 保留", 1e-15, "0.000000000000001"},
		{"极小值 1e-10", 1e-10, "0.0000000001"},
		{"切换点 1e-5", 1e-5, "0.00001"},
		{"切换点外侧 1e-4", 1e-4, "0.0001"},
		{"负极小值", -1e-10, "-0.0000000001"},
		{"大数 1e12", 1e12, "1000000000000"},
		{"大数 1e13", 1e13, "10000000000000"},
		{"大数非整数 1e12+0.5", 1000000000000.5, "1.00000000000e+12"},
		{"大数非整数 1e12+1/3", 1e12 + 1.0/3, "1.00000000000e+12"},
		{"大数非整数 1e15+0.1", 1e15 + 0.1, "1.00000000000e+15"},
		{"大数非整数 1.23456789012345e12", 1.23456789012345e12, "1.23456789012e+12"},
		{"零", 0, "0"},
		{"负零", math.Copysign(0, -1), "0"},
	}
	for _, c := range cases {
		if got := nswFmtNum(c.v); got != c.want {
			t.Errorf("%s: nswFmtNum(%v) = %q, want %q", c.name, c.v, got, c.want)
		}
	}
}

// oldNswFmtNum 修复前的实现 (仅测试用, 作对照基线): 'f',-1 吐 float64 全位尾数。
func oldNswFmtNum(v float64) string {
	if v == 0 {
		return "0"
	}
	if v == math.Round(v) {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	if math.Abs(v) < 1e-15 {
		return "0"
	}
	s := strconv.FormatFloat(v, 'g', 12, 64)
	if strings.ContainsAny(s, "eE") {
		s = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return s
}

// sigDigits 有效数字个数 (去符号 / 小数点 / 前导零)。科学计数法只数尾数。
func sigDigits(s string) int {
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "-")
	s = strings.Replace(s, ".", "", 1)
	s = strings.TrimLeft(s, "0")
	return len(s)
}

// TestNswFmtNumSigDigitsBound 全量扫描 1/i (i>=10001 落入 |v|<1e-4 的 FALLBACK 区间,
// 即缺陷面): 旧算法必然超标, 新算法必须 0 例超标 —— 自带对照, 防止扫描空跑。
func TestNswFmtNumSigDigitsBound(t *testing.T) {
	badOld, badNew, checked := 0, 0, 0
	var sampleOld, sampleNew []string
	for i := 1; i <= 1199739; i++ {
		v := 1.0 / float64(i)
		checked++

		o := oldNswFmtNum(v)
		if n := sigDigits(o); n > 12 {
			badOld++
			if len(sampleOld) < 3 {
				sampleOld = append(sampleOld, fmt.Sprintf("1/%d=%s(%d位)", i, o, n))
			}
		}

		s := nswFmtNum(v)
		if strings.ContainsAny(s, "eE") {
			t.Fatalf("1/%d = %q 仍含科学计数法", i, s)
		}
		if n := sigDigits(s); n > 12 {
			badNew++
			if len(sampleNew) < 3 {
				sampleNew = append(sampleNew, fmt.Sprintf("1/%d=%s(%d位)", i, s, n))
			}
		}
	}
	t.Logf("扫描 %d 例 | 旧算法超标 %d 例 样例=%v | 新算法超标 %d 例", checked, badOld, sampleOld, badNew)
	if badOld == 0 {
		t.Errorf("对照失效: 旧算法应超标 >0 例, 说明扫描未覆盖 FALLBACK 区间")
	}
	if badNew != 0 {
		t.Errorf("新算法超 12 位有效数字 %d 例: %v", badNew, sampleNew)
	}
}

// TestNswEvalEndToEnd 端到端: 表达式文本 -> 解析 -> 求值 -> 定点格式化 全链路。
func TestNswEvalEndToEnd(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"1/30000", "0.0000333333333333"},
		{"1/10001", "0.0000999900009999"},
		{"1/3", "0.333333333333"},
		{"9989/1199739", "0.00832597756679"},
		{"0/1199739", "0"},
	}
	for _, c := range cases {
		got, ok := nswEval(c.expr)
		if !ok {
			t.Errorf("nswEval(%q) 未识别", c.expr)
			continue
		}
		if got != c.want {
			t.Errorf("nswEval(%q) = %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestNswFmtNumBigNonIntRange |v| >= 1e12 非整数区间 (缺陷G 残留面, 20260911)。
// 旧实现该区间 100% 走 'f',-1, 吐 14~17 位浮点残差; 新实现改用 'e',11,
// 有效位数恒为 12。含对照, 防扫描空跑。
func TestNswFmtNumBigNonIntRange(t *testing.T) {
	rng := rand.New(rand.NewSource(20260911))
	badOld, badNew, checked := 0, 0, 0
	var sampleOld, sampleNew []string
	for i := 0; i < 20000; i++ {
		v := (rng.Float64()*9000 + 1) * 1e12
		if v == math.Round(v) {
			continue
		}
		checked++
		if o := oldNswFmtNum(v); sigDigits(o) > 12 {
			badOld++
			if len(sampleOld) < 3 {
				sampleOld = append(sampleOld, fmt.Sprintf("%v=%s(%d位)", v, o, sigDigits(o)))
			}
		}
		if s := nswFmtNum(v); sigDigits(s) > 12 {
			badNew++
			if len(sampleNew) < 3 {
				sampleNew = append(sampleNew, fmt.Sprintf("%v=%s(%d位)", v, s, sigDigits(s)))
			}
		}
	}
	t.Logf("扫描 %d 例 | 旧超标 %d 例 样例=%v | 新超标 %d 例 样例=%v", checked, badOld, sampleOld, badNew, sampleNew)
	if badOld == 0 {
		t.Errorf("对照失效: 旧算法应超标 >0 例, 说明扫描未覆盖 |v|>=1e12 区间")
	}
	if badNew != 0 {
		t.Errorf("新算法超 12 位有效数字 %d 例: %v", badNew, sampleNew)
	}
}
