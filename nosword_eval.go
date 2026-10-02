// nosword_eval.go: 求值入口与数字格式化/比较 (括号平衡/分数精度/容差判定)

package main

import (
	"math"
	"strconv"
	"strings"
)

// nswFloatExactMax float64 整数精确上界 (2^53)。达到该值后相邻可表示整数间隔 >1,
// 任何超出此范围的整数结果都不再可信 (2^53+1 == 2^53)。
const nswFloatExactMax = 1 << 53

// nswParenBalanced 括号配平检查: 深度中途不得为负, 结束时必须归零。
// 死程序宁可漏算也不产出错值 —— 未闭合括号的算式语义不确定, 一律拒绝。
func nswParenBalanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// nswEval 嗅探路径求值 (一期口径): 全部歧义闸生效。
// 输入是从自由文本切出的片段 —— 死程序不知道模型是否"想算",
// 故日期/区间/编号形态一律拒绝 (误算污染上下文的危害远大于漏算)。
func nswEval(expr string) (string, bool) { return nswEvalMode(expr, false) }

// nswEvalExplicit 显式标记路径求值 (20260920 三十三期)。
//
// 与嗅探路径的唯一差别: 跳过"数值范围歧义"类闸 (nswRangeRe / nswParenRangeRe /
// nswUnaryBare / nswLeadZeroRe / nswSlashPlusRe / 裸数对升序)。
// 根因: 那些闸的存在理由是"输入无位置信息, 分不清算式与区间"; 而 {{}} 标记
// 本身就是模型的意图声明, 歧义已被消解。若显式路径仍套用嗅探闸, 升级的信息
// 价值等于零 —— 实测 {{100-37}} 被拒 → 拒绝权回告 → 模型多烧一轮改写。
//
// 保留的闸 (不是"范围歧义", 而是语法/形态/精度):
//   - 字符集白名单 / 悬空点 / 无算符纯常量 (语法非法)
//   - 日期 2026-09-20 / 年月 / 3 段以上纯数字串 (电话·编号, 形态高度可信)
//   - 16 位以上整数 / NaN / Inf / 2^53 (精度: 算不准就不算)
func nswEvalExplicit(expr string) (string, bool) { return nswEvalMode(expr, true) }

func nswEvalMode(expr string, explicit bool) (string, bool) {
	if !nswIsCandidateMode(expr, explicit) {
		return "", false
	}
	// 括号配平闸 (20260921 缺陷 S): factor() 遇未闭合 '(' 曾静默跳过, 使末尾的
	// p.pos != len(p.s) 兜底形同虚设 —— "(1853-1861" 被算成 -8 注入 LLM 上下文。
	// 实测该形态绕过了已修的两道闸: (1853-1861) 被拒 / (1853-1861 却算出值。
	if !nswParenBalanced(expr) {
		return "", false
	}
	p := &nswParser{s: expr}
	v, ok := p.expr()
	if !ok {
		return "", false
	}
	p.skip()
	if p.pos != len(p.s) {
		return "", false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "", false
	}
	// 精度闸 (20260910 缺陷D): float64 尾数 53 bit, |v| >= 2^53 后相邻可表示整数间隔 >1,
	// "2^53+1" 会被静默算成 9007199254740992 —— 错值会污染 LLM 上下文, 危害远大于漏算。
	if math.Abs(v) >= nswFloatExactMax {
		return "", false
	}
	return nswFmtNum(v), true
}

// nswFmtNum 数值 -> 展示字符串。
//
// 定点位数取 12 位有效数字 (缺陷G, 20260911): 末位与真值的错率 ≈ 2.22e-16 × 10^(d-1),
// 1211 例精确有理数复算实测 d=12 -> 0.00%, d=13 -> 0.08%, d=14 -> 0.25%, d=15 -> 1.65%,
// d=16 -> 20.8%, d=17 -> 82.8%, 与公式吻合 —— 12 位是"每一位都反映真值"的上限,
// 勿因"精度越高越好"上调; 15 位虽多 3 位精度, 末位错率却恶化 1000 倍。
//
// 整数判定必须用精确比较 (20260910 缺陷F): 原用 math.Abs(v-math.Round(v)) < 1e-9,
// 把判据混进了"接近零"的语义 —— 任何 |v| < 1e-9 的非零值都被舍成 0,
// "1/10000000000" 输出 "0"、负值输出 "-0"。与缺陷D同源: 死程序产出错值,
// 污染 LLM 上下文, 危害远大于漏算。v 本身是整数时 v == Round(v) 精确成立;
// 浮点误差导致的近似整数 (如 sqrt(2)^2 = 2.0000000000000004) 走 'g' 12 分支照样显示 2。
//
// "接近零"另设浮点噪声闸: 运算结果的舍入残差 (如 0.1+0.2-0.3 = 5.55e-17) 不是真值,
// 展示成长串只会污染上下文。float64 机器精度 2.22e-16, 量级 ~1 的算式累积残差可达 ~1e-15,
// 故 |v| < 1e-15 视为噪声归零; 1e-15 以上 (如 1e-10) 是真实量级, 必须原样保留。
func nswFmtNum(v float64) string {
	if v == 0 { // 含 -0.0: 统一输出 "0", 不吐 "-0"
		return "0"
	}
	if v == math.Round(v) {
		return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
	}
	if math.Abs(v) < 1e-15 { // 浮点噪声闸 (见上)
		return "0"
	}
	s := strconv.FormatFloat(v, 'g', 12, 64)
	if strings.ContainsAny(s, "eE") {
		// 'g' 对极小/极大值切科学计数法, 转定点更易读; 但 'f',-1 会吐 float64 全位尾数
		// (1/30000 -> 0.000033333333333333335, 末 5 位是浮点残差不是真值), 伪装成高精度
		// 污染 LLM 上下文 —— 与缺陷D/F 同源: 死程序产出错值危害远大于漏算。
		// 按十进制指数反推小数位, 与 'g',12 同口径收敛到 12 位有效数字。
		if dec := nswFracDigits(v); dec >= 0 {
			s = strconv.FormatFloat(v, 'f', dec, 64)
			s = strings.TrimRight(s, "0")
			s = strings.TrimSuffix(s, ".")
		} else {
			// dec < 0 即 |v| >= 1e12 (缺陷G 残留, 20260911): 整数部分已占满 12 位有效数字,
			// 定点承载不下 —— 'f',-1 会吐 14~17 位浮点残差 (1e12+1/3 -> 1000000000000.3334),
			// 实测该区间 100% 超标; 'g',12 去尾零后只剩 1 位 (1e+12), 同样失真。
			// 改用 'e',11: 显式 12 位有效数字, 与定点路径同口径。
			s = strconv.FormatFloat(v, 'e', 11, 64)
		}
	}
	return s
}

// nswFracDigits 定点小数位数: 使定点输出与 'g',12 同为 12 位有效数字。
// 指数由 'e' 格式精确解析 —— math.Log10 在 10 的整数幂附近会差 1
// (log10(1e-5) 可能算出 -4.999999999999999), 用它会让位数错一位。
// 返回 -1 表示整数部分已占满 12 位有效数字 (|v| >= 1e12), 定点承载不下,
// 调用方改用 'e',11 科学计数法。
func nswFracDigits(v float64) int {
	e := strconv.FormatFloat(v, 'e', -1, 64) // 形如 "3.3333333333333335e-05"
	i := strings.IndexByte(e, 'e')
	if i < 0 {
		return 11
	}
	exp, err := strconv.Atoi(e[i+1:])
	if err != nil {
		return 11
	}
	dec := 11 - exp
	if dec < 0 {
		return -1
	}
	if dec > 40 {
		dec = 40
	}
	return dec
}

// nswHasDanglingDot 检测悬空点号: 点号后不是数字 (如 "0-9." "1+2.")。
// 这类片段来自正则/文本, 不是合法数字字面量 → 拒绝 (宁可漏算, 不可误算)。
// 注意 ".5" 属合法小数 (点号后是数字), 不受影响。
func nswHasDanglingDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i+1 >= len(s) || s[i+1] < '0' || s[i+1] > '9') {
			return true
		}
	}
	return false
}
