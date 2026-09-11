package main

import "testing"

// 缺陷H (20260911): 时段组与前导零被误算成算式。
//
// 现场: 助手输出 "周一-五 9-12 / 14-18" 与 "12:00 / 14:00" 残片,
// 嗅探器按"非算式字符"切分 —— 这两串全是算式字符, 于是合成单个候选:
//   "9-12 / 14-18"  → 9-(12/14)-18 = -69/7 ≈ -9.85714285714   (语义: 时段, 非算式)
//   "00/14"         → 0/14         = 0                          (语义: 时间残片)
// 与缺陷 A/C/D/E/F 同源: 死程序产出错值污染 LLM 上下文, 危害远大于漏算。
//
// 拒绝对策 (宁可漏算, 不可误算):
//   规则1 nswRangeDivRe: 区间/区间 (H-H / H-H) 全拒
//   规则2 nswLeadZeroRe: 数字字面量前导零全拒

func TestNSW_TimeRangeGroup_DefectH(t *testing.T) {
	// 时段组: 门诊/营业时段写法, 中文文本高频 → 必须拒绝
	rejects := []string{
		"9-12 / 14-18",
		"9-12/14-18",
		"8-10 / 13-17",
		"9-12 / 14-18 门诊",
	}
	for _, s := range rejects {
		if got, ok := nswEval(s); ok {
			t.Errorf("%q 应拒绝(时段组), 却算出 %s", s, got)
		}
	}
}

func TestNSW_LeadingZero_DefectH(t *testing.T) {
	// 前导零: "12:00 / 14:00" 被嗅探切剩的残片 → 必须拒绝
	rejects := []string{"00 / 14", "00/14", "9/00", "001+2"}
	for _, s := range rejects {
		if got, ok := nswEval(s); ok {
			t.Errorf("%q 应拒绝(前导零), 却算出 %s", s, got)
		}
	}
}

// 新规则不得误伤合法算式 —— 这是本补丁最重要的约束。
// 特别地: 内嵌零 (100+2 的 "00"、1.05 的 "05") 前有数字或小数点, 判据 [^\d.] 不命中。
func TestNSW_DefectH_NoCollateralDamage(t *testing.T) {
	cases := []struct{ expr, want string }{
		{"10/4", "2.5"},             // 两数除法 (M/D 日期规则若实施会误杀此例, 故未实施)
		{"3/4", "0.75"},             // 同上
		{"100+2", "102"},            // 内嵌零: "00" 前是数字
		{"100*3", "300"},            // 同上
		{"1.05*2", "2.1"},           // 内嵌零: "05" 前是小数点
		{"-69/7", "-9.85714285714"}, // 带符号除法: 无歧义, 必须保留
	}
	for _, c := range cases {
		got, ok := nswEval(c.expr)
		if !ok {
			t.Errorf("%q 被误拒 (新规则误伤合法算式)", c.expr)
			continue
		}
		if got != c.want {
			t.Errorf("%q = %s, 期望 %s", c.expr, got, c.want)
		}
	}
}

// 既有行为记录: "1000/2" 被 nswYearMonRe (YYYY/MM 年月) 拒绝, 属本次补丁之前的取舍
// ("2026/09" 在中文语境高频, 宁漏勿误)。此处固化为回归哨兵 —— 若有人放宽年月规则
// 导致 1000/2 被算成 500, 该测试会失败提醒重新评估取舍。
func TestNSW_YearMonTradeoff_PreExisting(t *testing.T) {
	if got, ok := nswEval("1000/2"); ok {
		t.Errorf("1000/2 放行 = %s, 年月规则取舍已变, 请重新评估", got)
	}
	if got, ok := nswEval("2026/09"); ok {
		t.Errorf("2026/09 放行 = %s, 年月应被拒绝", got)
	}
}
