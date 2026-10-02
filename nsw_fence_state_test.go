package main

// 围栏状态机直测 (20260930 补 nswFenceCarry / settle 的测试缺口)。
//
// 缺口: nsw_fence_test.go 只断言"任意切分下流式渲染与一次性渲染逐字节一致"(端到端),
// 而状态机自身的两条规则在测试里零提及 (静态扫描: nswFenceCarry 0 处 / settle 0 处)。
// 端到端一致 != 规则正确 —— 样本里的反引号串恰好整段到达时, 把 carry 规则改坏也测不出来。
//
// 本文件钉三件事:
//  1. nswFenceCarry 的规则: 余数只在反引号串延伸到段尾时留存 (atEnd 是唯一开关)
//  2. settle 的语义: 非反引号字符打断未决串, 余数当场作废 —— 否则上一段的余数会被
//     错误地并入下一段开头的独立反引号串 (注释记录的实测反例)
//  3. 判据自检: 内联变异体证明这批样本真有鉴别力 (样本不会"全绿"地放过错误规则)

import (
	"math/rand"
	"testing"
)

// refFenceIn 参考模型: 对整串统计每段连续反引号, 翻转次数 = 各段 run/3 之和的奇偶。
// 状态机逐段 feed 必须与它一致 —— 数学依据是整数除法结合律:
// floor(a/3) + floor((a%3+b)/3) == floor((a+b)/3)。
func refFenceIn(s string) bool {
	flips := 0
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		flips += (j - i) / 3
		i = j
	}
	return flips%2 == 1
}

// refFenceTailCarry 参考模型: 结算后的残余 carry = 整串末尾连续反引号数 % 3。
// 不以反引号结尾时余数不可能再被补成围栏, 故为 0。
func refFenceTailCarry(s string) int {
	k := len(s)
	for k > 0 && s[k-1] == '`' {
		k--
	}
	return (len(s) - k) % 3
}

// feedAll 把 parts 依次喂给状态机 (模拟 SSE 分块到达)。
func feedAll(st *nswFenceState, parts []string) {
	for _, p := range parts {
		st.feed(p)
	}
}

// fenceStateSamples 覆盖: 单/双/三/四/五反引号、跨段拼接、非围栏反引号混排、
// 含标记的正文、纯文本、空串。
var fenceStateSamples = []string{
	"```\n{{1+1}}\n```",
	"前\n```go\n{{1+1}}\n```\n后 {{2+3}}",
	"a`b`c```{{1+1}}",
	"x`y``z```w",
	"`", "``", "```", "````", "`````",
	"a``b``c``d", "`a`b`c`",
	"```\ncode\n``````\n{{2+2}}\n```",
	"`x", "x`", "``` ```` ```",
	"无标记文本", "",
}

// fenceStateSplit 按指定粒度切分 (按字节切, 与真实 SSE 分块一致)。
func fenceStateSplit(s, mode string, rnd *rand.Rand) []string {
	var out []string
	switch mode {
	case "whole":
		return []string{s}
	case "by1":
		for i := 0; i < len(s); i++ {
			out = append(out, s[i:i+1])
		}
		return out
	case "by2":
		for i := 0; i < len(s); i += 2 {
			out = append(out, s[i:min(i+2, len(s))])
		}
		return out
	case "by3":
		for i := 0; i < len(s); i += 3 {
			out = append(out, s[i:min(i+3, len(s))])
		}
		return out
	}
	for i := 0; i < len(s); {
		e := min(i+1+rnd.Intn(4), len(s))
		out = append(out, s[i:e])
		i = e
	}
	return out
}

// TestNSWFenceCarryRule 直接钉住 nswFenceCarry 的规则。
func TestNSWFenceCarryRule(t *testing.T) {
	cases := []struct {
		run   int
		atEnd bool
		want  int
	}{
		{0, true, 0}, {1, true, 1}, {2, true, 2}, {3, true, 0}, {4, true, 1},
		{5, true, 2}, {6, true, 0}, {7, true, 1}, {8, true, 2}, {9, true, 0},
		{0, false, 0}, {1, false, 0}, {2, false, 0}, {3, false, 0},
		{4, false, 0}, {5, false, 0}, {6, false, 0}, {7, false, 0},
	}
	for _, c := range cases {
		if got := nswFenceCarry(c.run, c.atEnd); got != c.want {
			t.Errorf("nswFenceCarry(run=%d, atEnd=%v) = %d, want %d", c.run, c.atEnd, got, c.want)
		}
	}
	// 规则性质: atEnd=true 时余数 == run%3 且 ∈ [0,2]; atEnd=false 时恒 0。
	for run := 0; run <= 64; run++ {
		if got := nswFenceCarry(run, true); got != run%3 {
			t.Errorf("atEnd=true run=%d: got %d, want run%%3=%d", run, got, run%3)
		}
		if got := nswFenceCarry(run, false); got != 0 {
			t.Errorf("atEnd=false run=%d: got %d, want 0 (后面紧跟非反引号字符, 余数永不可补成围栏)", run, got)
		}
	}
}

// TestNSWFenceStateChunkBoundary 分块边界下的状态推进 (期望值经整串参考模型交叉验证)。
func TestNSWFenceStateChunkBoundary(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		in    bool
		carry int
	}{
		{"跨段拼成三反引号", []string{"x`", "``y"}, true, 0},
		{"逐字节到达", []string{"`", "`", "`"}, true, 0},
		{"余数 1 留在段尾", []string{"x`"}, false, 1},
		{"余数 2 留在段尾", []string{"x``"}, false, 2},
		{"非段尾余数当场清零", []string{"a`b"}, false, 0},
		{"段内自成围栏", []string{"a`b`c```"}, true, 0},
		{"四反引号落在段尾", []string{"x````"}, true, 1},
		{"两段各两反引号合成四", []string{"``", "``"}, true, 1},
		{"五反引号分三段", []string{"`", "``", "``"}, true, 2},
		{"非围栏反引号混排", []string{"x`y``z```w"}, true, 0},
		{"空段不改变状态", []string{"", ""}, false, 0},
	}
	for _, c := range cases {
		var st nswFenceState
		feedAll(&st, c.parts)
		if st.in != c.in || st.carry != c.carry {
			t.Errorf("%s: feed(%q) -> in=%v carry=%d, want in=%v carry=%d",
				c.name, c.parts, st.in, st.carry, c.in, c.carry)
		}
	}
}

// TestNSWFenceStateSettle 钉住 settle 的语义: 未决余数遇到非反引号字符即作废。
//
// 调用点: nswExprRenderFrom 在处理标记前调用 st.settle() —— 标记以 '{' 开头,
// 必然打断反引号串。若不结算, 上一段残留的 carry 会被并入下一段开头的独立反引号串。
func TestNSWFenceStateSettle(t *testing.T) {
	cases := []struct {
		name    string
		parts   []string
		in      bool
		carry   int
		noSettl bool // 不调 settle 时的 in (鉴别力对照, 必须不同)
	}{
		{"余数不带入下一段", []string{"a`", "``"}, false, 2, true},
		{"混排正文仍正确", []string{"a`", "``b```"}, true, 0, false},
	}
	for _, c := range cases {
		var st nswFenceState
		st.feed(c.parts[0])
		st.settle()
		feedAll(&st, c.parts[1:])
		if st.in != c.in || st.carry != c.carry {
			t.Errorf("%s: feed(%q) + settle -> in=%v carry=%d, want in=%v carry=%d",
				c.name, c.parts, st.in, st.carry, c.in, c.carry)
		}
		// 判据自检: 同一输入故意不调 settle, 结果必须与期望不同 —— 否则该样本无鉴别力。
		var raw nswFenceState
		feedAll(&raw, c.parts)
		if raw.in != c.noSettl {
			t.Errorf("%s: 鉴别力失效 —— 不结算时 in=%v, 期望 %v (该样本测不出 settle 的作用)",
				c.name, raw.in, c.noSettl)
		}
	}
	// settle 只清余数, 不翻转已确定的围栏状态。
	var st nswFenceState
	st.feed("```\nx")
	if !st.in {
		t.Fatalf("前置条件不成立: feed(\"```\\nx\") 后应处于围栏内")
	}
	st.settle()
	if !st.in {
		t.Error("settle 不得翻转已确定的围栏状态")
	}
	if st.carry != 0 {
		t.Errorf("settle 后 carry = %d, want 0", st.carry)
	}
}

// TestNSWFenceStateMatchesReference 任意切分下状态机与整串参考模型一致。
// 这是"确定性契约"在状态层的表述: 状态推进不得依赖分块边界。
func TestNSWFenceStateMatchesReference(t *testing.T) {
	modes := []string{"whole", "by1", "by2", "by3", "rand"}
	total := 0
	for si, s := range fenceStateSamples {
		wantIn, wantCarry := refFenceIn(s), refFenceTailCarry(s)
		for _, mode := range modes {
			total++
			rnd := rand.New(rand.NewSource(int64(si*31 + len(mode))))
			var st nswFenceState
			feedAll(&st, fenceStateSplit(s, mode, rnd))
			if st.in != wantIn {
				t.Errorf("样本%d mode=%s: in=%v, want %v (输入 %q)", si, mode, st.in, wantIn, s)
			}
			if st.carry != wantCarry {
				t.Errorf("样本%d mode=%s: carry=%d, want %d (输入 %q)", si, mode, st.carry, wantCarry, s)
			}
		}
	}
	if total == 0 {
		t.Fatal("样本集为空 —— 测试没有覆盖任何输入")
	}
	t.Logf("状态机一致性: %d 组 (样本 %d x 切分 %d) 全部与整串参考模型一致",
		total, len(fenceStateSamples), len(modes))
}

// mutantFenceState 变异体: carry 规则固定, 模拟 nswFenceCarry 被改坏。
type mutantFenceState struct {
	in     bool
	carry  int
	always bool // true = 无条件留存余数; false = 永不留存
}

func (st *mutantFenceState) carryOf(run int, atEnd bool) int {
	if st.always {
		return run % 3
	}
	return 0
}

// feed 与 nswFenceState.feed 逐行同构, 唯一差异是 carry 规则 (供鉴别力自检)。
func (st *mutantFenceState) feed(s string) {
	i := 0
	if st.carry > 0 {
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j == i {
			st.carry = 0
		} else {
			run := st.carry + (j - i)
			st.in = nswFenceFlip(st.in, run)
			st.carry = st.carryOf(run, j == len(s))
			i = j
		}
	}
	for i < len(s) {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		run := j - i
		st.in = nswFenceFlip(st.in, run)
		st.carry = st.carryOf(run, j == len(s))
		i = j
	}
}

// TestNSWFenceCarryMutantDetected 判据自检: 样本集必须能识别出错误的 carry 规则。
// 否则"测试全绿"只说明样本没有鉴别力, 不说明规则正确。
func TestNSWFenceCarryMutantDetected(t *testing.T) {
	modes := []string{"whole", "by1", "by2", "by3", "rand"}
	for _, always := range []bool{true, false} {
		diff := 0
		for si, s := range fenceStateSamples {
			wantIn := refFenceIn(s)
			for _, mode := range modes {
				rnd := rand.New(rand.NewSource(int64(si*31 + len(mode))))
				mu := mutantFenceState{always: always}
				for _, p := range fenceStateSplit(s, mode, rnd) {
					mu.feed(p)
				}
				if mu.in != wantIn {
					diff++
				}
			}
		}
		if diff == 0 {
			t.Errorf("变异体 always=%v 在全部样本上与正确规则同结果 —— 样本集无鉴别力, 需补样本", always)
			continue
		}
		t.Logf("变异体 always=%v: %d 组与正确规则不一致 (鉴别力成立)", always, diff)
	}
}
