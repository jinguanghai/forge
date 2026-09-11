package main

// ── nsw_audit_test.go — 无剑嗅探器真实世界文本审计 (误算率量化) ──
// 教训来源: 20260910 压测"负样本误算 9/16"(日期→2007 等)。边界收紧后重新量化。

import "testing"

func TestNSWAudit_RealWorldNegatives(t *testing.T) {
	neg := []string{
		// 日期/时间
		"今天是 2026-09-10", "2026/09/10", "2026-09-10 10:44:40", "2026-09",
		"会议时间 2026-09-10 14:30", "9:30-11:00", "第1-2季度", "2026年9月10日",
		// 区间/序号
		"3-5个工作日", "页码 12-15", "共 5-8 项", "2-3天", "第 3-5 章",
		"温度 -3 到 5 度", "区间 [0,1]",
		// 小数区间 (20260910 缺陷A修复: 原被当减法算出 -6.4)
		"价格 12.5-18.9 元", "体重 45.5-62.5kg", "温度 36.2-37.3 度", "区间 1.5-2",
		// 版本/编号/标识
		"版本 v2.0.1", "2.0", "12", "(16)", "订单号 20260910123456",
		"hash a3f5b9", "SHA256", "a1b2c3", "5G网络", "3D打印", "H2O",
		"pH=7.4", "地址 A座12层", "车牌 京A12345",
		// 网络/隐私
		"IP 192.168.1.1", "电话 13800138000", "身份证 11010119900307123X",
		"https://api.deepseek.com/v1", "www.example.com/a/b",
		// 数字+符号混排
		"约 100+ 人", "增长 30%", "1,234.56", "99999999999999999999+1",
		"价格12元，版本2.0和3.5倍", "温度 -3 度",
	}
	var bad []string
	for _, in := range neg {
		if as := nswEvaluate(in); len(as) != 0 {
			bad = append(bad, in)
			t.Logf("误算: %q -> %+v", in, as)
		}
	}
	t.Logf("负样本 %d 条, 误算 %d 条", len(neg), len(bad))
	if len(bad) > 0 {
		t.Errorf("误算清单: %v", bad)
	}
}

func TestNSWAudit_RealWorldPositives(t *testing.T) {
	pos := map[string]string{
		"2*3+4":     "10",
		"帮我算 3 * 7": "21",
		"10/4":      "2.5",
		"sqrt(16)":  "4",
		"1+1=2":     "2",
		"(1+2)*3":   "9",
		// 注: "100-30" 不列入正样本 —— 与 "3-5个工作日" 形态相同, 无法区分,
		// nswRangeRe 按"宁可漏算不可误算"整体拒绝整数区间 (设计取舍)
		"2^10":            "1024",
		"18.9-12.5":       "6.4",
		"0.1+0.2":         "0.3",
		"round(28.274,2)": "28.27",
	}
	var miss []string
	for in, want := range pos {
		as := nswEvaluate(in)
		if len(as) == 0 {
			miss = append(miss, in)
			t.Logf("漏算: %q (期望 %s)", in, want)
			continue
		}
		if as[0].val != want {
			miss = append(miss, in)
			t.Logf("错值: %q -> %s (期望 %s)", in, as[0].val, want)
		}
	}
	t.Logf("正样本 %d 条, 漏/错 %d 条", len(pos), len(miss))
	if len(miss) > 0 {
		t.Errorf("正样本问题: %v", miss)
	}
}

// TestNSW_DecimalRange_DefectA 缺陷A专项回归 (20260910):
// 小数裸数对按数值序消歧 —— 升序=区间(拒), 降序=减法(算)。
func TestNSW_DecimalRange_DefectA(t *testing.T) {
	reject := []string{"价格 12.5-18.9 元", "12.5-18.9", "1.5-2", "36.2-37.3", "0.1-0.2"}
	for _, in := range reject {
		if as := nswEvaluate(in); len(as) != 0 {
			t.Errorf("升序小数区间应拒绝, 实际算出: %q -> %+v", in, as)
		}
	}
	accept := map[string]string{"18.9-12.5": "6.4", "2-1.5": "0.5", "10.5-0.5": "10"}
	for in, want := range accept {
		as := nswEvaluate(in)
		if len(as) == 0 {
			t.Errorf("降序小数减法应可算, 实际漏算: %q (期望 %s)", in, want)
			continue
		}
		if as[0].val != want {
			t.Errorf("值错: %q -> %s (期望 %s)", in, as[0].val, want)
		}
	}
}

// ── 20260910 缺陷C/D/E 专项回归 ──

// TestNSW_DanglingDot_DefectC 缺陷C回归:
// 正则/文本片段 "0-9." 经嗅探切分后被当算式算出 -9 (number() 把 "9." 读成 9)。
// 悬空点号 (点号后非数字) 一律拒绝; ".5" 属合法小数, 不受影响。
func TestNSW_DanglingDot_DefectC(t *testing.T) {
	reject := []string{
		"0-9.", "1+2.", "[0-9.]+", "[0-9.]+$", "\\d+\\.\\d*", "0-9.]", "1.2.",
		"数字 0-9. 表示", "正则 [0-9.]+ 匹配",
	}
	for _, in := range reject {
		if as := nswEvaluate(in); len(as) != 0 {
			t.Errorf("悬空点号应拒绝, 实际算出: %q -> %+v", in, as)
		}
	}
	accept := map[string]string{
		"0.1+0.2":         "0.3",
		"3.14 * 2":        "6.28",
		"round(28.274,2)": "28.27",
		"18.9-12.5":       "6.4",
		".5+.5":           "1",
	}
	for in, want := range accept {
		as := nswEvaluate(in)
		if len(as) == 0 {
			t.Errorf("合法小数应可算, 实际漏算: %q (期望 %s)", in, want)
			continue
		}
		if as[0].val != want {
			t.Errorf("值错: %q -> %s (期望 %s)", in, as[0].val, want)
		}
	}
}

// TestNSW_PrecisionGate_DefectD 缺陷D回归:
// float64 尾数 53 bit -> "2^53+1" 被静默算成 9007199254740992 (错值污染 LLM 上下文)。
// 出口精度闸: |v| >= 2^53 一律拒绝 (宁可漏算, 不可误算)。
func TestNSW_PrecisionGate_DefectD(t *testing.T) {
	reject := []string{"2^53+1", "2^53", "2^60", "2^53+2", "2^64", "10^20"}
	for _, in := range reject {
		if as := nswEvaluate(in); len(as) != 0 {
			t.Errorf("超出 float64 精确范围应拒绝, 实际算出: %q -> %+v", in, as)
		}
	}
	accept := map[string]string{
		"2^53-1": "9007199254740991",
		"2^50":   "1125899906842624",
		"2^10":   "1024",
	}
	for in, want := range accept {
		as := nswEvaluate(in)
		if len(as) == 0 {
			t.Errorf("精确范围内应可算, 实际漏算: %q (期望 %s)", in, want)
			continue
		}
		if as[0].val != want {
			t.Errorf("值错: %q -> %s (期望 %s)", in, as[0].val, want)
		}
	}
}

// TestNSW_DedupByExpr_DefectE 缺陷E回归:
// 去重键从"结果值"改为"表达式" —— 同值不同式 ("2+3"/"4+1") 必须各留锚点,
// 否则锚点列表不完整, 且同值错值会被掩盖。
func TestNSW_DedupByExpr_DefectE(t *testing.T) {
	as := nswEvaluate("2+3 和 4+1")
	if len(as) != 2 {
		t.Fatalf("同值不同式应各留锚点, 实际 %d 个: %+v", len(as), as)
	}
	got := map[string]string{}
	for _, a := range as {
		got[a.expr] = a.val
	}
	if got["2+3"] != "5" || got["4+1"] != "5" {
		t.Errorf("锚点内容错: %+v", as)
	}
	if dup := nswEvaluate("1+2 和 1+2"); len(dup) != 1 {
		t.Errorf("同表达式仍应去重, 实际 %d 个: %+v", len(dup), dup)
	}
}

// TestNSW_SmallValue_DefectF 极小值不得被静默归零 (20260910 缺陷F)
// 活体证据: 用户真实反馈中出现 「1/10000000000 = 0」——原 nswFmtNum 用
// math.Abs(v-Round(v)) < 1e-9 判整数, 使 |v| < 1e-9 的非零值全被舍成 0, 负值更输出 "-0"。
// 无剑的输出是被 LLM 直接采信的"事实", 错值危害远大于漏算。
func TestNSW_SmallValue_DefectF(t *testing.T) {
	accept := map[string]string{
		"1/10000000000": "0.0000000001",
		"2/10000000000": "0.0000000002",
		"5/10000000000": "0.0000000005",
		"1/1000000000":  "0.000000001",
		"1/100000000":   "0.00000001",
	}
	for in, want := range accept {
		as := nswEvaluate(in)
		if len(as) == 0 {
			t.Errorf("应可算但被漏算: %q (期望 %s)", in, want)
			continue
		}
		if as[0].val != want {
			t.Errorf("极小值被错误格式化: %q -> %s (期望 %s)", in, as[0].val, want)
		}
		if as[0].val == "0" || as[0].val == "-0" {
			t.Errorf("极小值被静默归零: %q -> %s", in, as[0].val)
		}
	}

	// 负极小值不得输出 "-0"
	if as := nswEvaluate("-1/10000000000"); len(as) == 0 {
		t.Error("负极小值应可算")
	} else if as[0].val == "-0" || as[0].val == "0" {
		t.Errorf("负极小值被归零: %q -> %s", "-1/10000000000", as[0].val)
	}

	// 浮点噪声归零: 0.1+0.2-0.3 的残差是舍入噪声, 不是真值 (见 nswFmtNum 噪声闸)
	if as := nswEvaluate("0.1+0.2-0.3"); len(as) == 0 || as[0].val != "0" {
		t.Errorf("浮点噪声应归零: 0.1+0.2-0.3 -> %+v", as)
	}
	// 真实极小量必须保留
	if as := nswEvaluate("1/10000000000000"); len(as) > 0 && as[0].val == "0" {
		t.Errorf("1e-13 是真实量级, 不应归零: %+v", as)
	}

	// 无退化: 近似整数与常规算式格式必须保持原样
	noRegress := map[string]string{
		"sqrt(2)*sqrt(2)": "2",
		"1/3*3":           "1",
		"0.1+0.2":         "0.3",
		"2/3":             "0.666666666667",
		"3*7":             "21",
		"10/4":            "2.5",
		"2+53":            "55",
	}
	for in, want := range noRegress {
		as := nswEvaluate(in)
		if len(as) == 0 {
			t.Errorf("回归: 应可算但漏算 %q", in)
			continue
		}
		if as[0].val != want {
			t.Errorf("回归: 值变 %q -> %s (期望 %s)", in, as[0].val, want)
		}
	}
}
