package main

import "testing"

// 缺陷V (20261015): 缺陷S 的对偶缺口 —— 斜杠右侧带显式负号的数值对无闸。
// 基线用真实 nswEvaluate 实测复现 (修复前):
//
//	V1 "改动 3 文件, +70/-4" -> -17.5  (git 统计记法被当除法, 误算注入 LLM 上下文)
//	V2 "增减 +3/-2"          -> -1.5
//
// 根因: 缺陷S 的闸 (原 nswSlashPlusRe) 当年只认斜杠右侧的 '+' —— 注释明写
// "只认 '+' 不认 '-' —— 负分数 -3/4 不得误伤", 于是 "/-数字" 整族无闸。
// 鉴别点: 符号在斜杠右侧 (自然语言里恒为增减/正负项记法) vs 在分子 (一元负号算式)。
func TestNSWDefectV_SlashRightSign(t *testing.T) {
	// 实测形态及其同族: 一律不得产出锚点
	reject := []string{
		"改动 3 文件, +70/-4",
		"增减 +3/-2",
		"涨跌 +12.5/-2.5",
		"比例 0.45/+0.12", // 缺陷S 原形, 防泛化时回归
	}
	for _, s := range reject {
		if a := nswEvaluate(s); len(a) != 0 {
			t.Errorf("斜杠带号列举误算: %q -> %+v", s, a)
		}
	}
	// 直接判定 (不经位置上下文): 候选闸本身必须拒绝
	for _, s := range []string{"+70/-4", "70/-4", "+3/-2", "+12.5/-2.5", "70/-4/-3", "0.45/+0.12", "+0.45/0.12"} {
		if nswIsCandidate(s) {
			t.Errorf("nswIsCandidate 未拒绝斜杠带号形态: %q", s)
		}
	}
	// 真算式必须保留 (与缺陷P/S keep 集合同口径)
	keep := map[string]string{
		"-3/4":      "-0.75",
		"+1/2+1/3":  "0.833333333333",
		"3/4-1/2":   "0.25",
		"12-5/2":    "9.5",
		"18.9-12.5": "6.4",
	}
	for e, want := range keep {
		v, ok := nswEval(e)
		if !ok || v != want {
			t.Errorf("真算式被误伤: %q -> ok=%v v=%q want=%q", e, ok, v, want)
		}
	}
	// 闸只在嗅探口径生效: 显式 {{70/-4}} 是意图声明, 照旧求值 (与缺陷S 同口径)
	if !nswIsCandidateMode("70/-4", true) {
		t.Error("显式口径下 70/-4 应仍是候选 (带号闸仅嗅探生效)")
	}
}
