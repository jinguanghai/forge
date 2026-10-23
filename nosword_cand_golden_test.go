package main

// nosword_cand_golden_test.go — T61 行为快照 (20261018): 拆分 nosword_cand.go 前固化
// 闸门判决与候选切分 (rune 偏移) 的当前行为, 使「拆结构不改行为」成为可判定的。

import (
	"fmt"
	"strings"
	"testing"
)

var nswGoldenInputs = []string{
	"2026/09/10",
	"2026-09-20",
	"2026-9-3",
	"2026/09",
	"2026-09",
	"138-1234-5678",
	"50-78-2",
	"1-2-3",
	"12345678901234567",
	"12-3/4-5",
	"1-2/3/4",
	"3-5",
	"12-15",
	"-3",
	"+3.5",
	"01+2",
	"0.5+1",
	"(1853-1861)",
	"(10-4)",
	"((10-4))",
	"( 1853 - 1861 )",
	"0.45/+0.12",
	"+0.45/0.12",
	"70/-4",
	"+3/-2",
	"-3/4",
	"1/2+1/3",
	"12.5-18.9",
	"18.9-12.5",
	"12.5-12.5",
	"12.5-18.9+1",
	"0-9.",
	"1.+2",
	".5+1",
	"1+1.",
	"1+1个",
	"3×5",
	"1–12",
	"1—12",
	"1−12",
	"50%",
	"1+1 ",
	"  1+1",
	"1_000+1",
	"1:5",
	"9:30+60",
	"- 10-14",
	"-1-2-3",
	"- 3+5",
	"+ 2*3",
	"-  ",
	"--3",
	"- 10-14 全天",
	"",
	"   ",
	"3.14",
	"abc",
	"sqrt(16)",
	"2^10",
	"(3+4)",
	"1,000",
	"1,000+5",
	"10/4",
	"37/36^2/45/43^1",
	"3.14+0",
}

var nswGoldenTexts = []string{
	"一共 {{35*30}} 元。",
	"价格 12.5–18.9 元",
	"改动 3 文件, +70/-4 行",
	"**0/4** 与 *2/3*",
	"中文前缀 1+1 后",
	"a  1+1  b",
	"50% 1+1",
	"（1080/1081）",
	"1–12 / 1–31",
	"交付率 8/13, 正确率 5/8",
	"9:30+60 与 1:5",
	"0.02/1/4 三档",
}

func nswGoldenRender() string {
	var b strings.Builder
	for _, in := range nswGoldenInputs {
		fmt.Fprintf(&b, "IN %q 隐=%v 显=%v\n", in, nswIsCandidate(in), nswIsCandidateMode(in, true))
	}
	for _, txt := range nswGoldenTexts {
		fmt.Fprintf(&b, "TXT %q\n", txt)
		for i, c := range nswSniffCands(txt) {
			fmt.Fprintf(&b, "  [%d] expr=%q start=%d end=%d tl=%q tr=%q\n", i, c.expr, c.start, c.end, c.trimmedLeft, c.trimmedRight)
		}
	}
	return b.String()
}

// nswGoldenWant 是 20261018 拆分前的实测渲染 (T61 基线), 逐字节固化。
// 任何一行变化都意味着闸门判决或候选切分 (含 rune 偏移) 漂移 —— 拆分不得改行为。
var nswGoldenWant = `IN "2026/09/10" 隐=false 显=false
IN "2026-09-20" 隐=false 显=false
IN "2026-9-3" 隐=false 显=false
IN "2026/09" 隐=false 显=false
IN "2026-09" 隐=false 显=false
IN "138-1234-5678" 隐=false 显=false
IN "50-78-2" 隐=false 显=false
IN "1-2-3" 隐=false 显=false
IN "12345678901234567" 隐=false 显=false
IN "12-3/4-5" 隐=false 显=false
IN "1-2/3/4" 隐=false 显=false
IN "3-5" 隐=false 显=true
IN "12-15" 隐=false 显=true
IN "-3" 隐=false 显=true
IN "+3.5" 隐=false 显=true
IN "01+2" 隐=false 显=true
IN "0.5+1" 隐=true 显=true
IN "(1853-1861)" 隐=false 显=true
IN "(10-4)" 隐=false 显=true
IN "((10-4))" 隐=true 显=true
IN "( 1853 - 1861 )" 隐=false 显=true
IN "0.45/+0.12" 隐=false 显=true
IN "+0.45/0.12" 隐=false 显=true
IN "70/-4" 隐=false 显=true
IN "+3/-2" 隐=false 显=true
IN "-3/4" 隐=true 显=true
IN "1/2+1/3" 隐=true 显=true
IN "12.5-18.9" 隐=false 显=true
IN "18.9-12.5" 隐=true 显=true
IN "12.5-12.5" 隐=true 显=true
IN "12.5-18.9+1" 隐=true 显=true
IN "0-9." 隐=false 显=false
IN "1.+2" 隐=false 显=false
IN ".5+1" 隐=true 显=true
IN "1+1." 隐=false 显=false
IN "1+1个" 隐=false 显=false
IN "3×5" 隐=false 显=false
IN "1–12" 隐=false 显=false
IN "1—12" 隐=false 显=false
IN "1−12" 隐=false 显=false
IN "50%" 隐=false 显=false
IN "1+1 " 隐=true 显=true
IN "  1+1" 隐=true 显=true
IN "1_000+1" 隐=false 显=true
IN "1:5" 隐=false 显=false
IN "9:30+60" 隐=false 显=false
IN "- 10-14" 隐=false 显=true
IN "-1-2-3" 隐=true 显=true
IN "- 3+5" 隐=true 显=true
IN "+ 2*3" 隐=true 显=true
IN "-  " 隐=false 显=false
IN "--3" 隐=true 显=true
IN "- 10-14 全天" 隐=false 显=false
IN "" 隐=false 显=false
IN "   " 隐=false 显=false
IN "3.14" 隐=false 显=false
IN "abc" 隐=false 显=false
IN "sqrt(16)" 隐=true 显=true
IN "2^10" 隐=true 显=true
IN "(3+4)" 隐=true 显=true
IN "1,000" 隐=false 显=false
IN "1,000+5" 隐=false 显=true
IN "10/4" 隐=true 显=true
IN "37/36^2/45/43^1" 隐=true 显=true
IN "3.14+0" 隐=true 显=true
TXT "一共 {{35*30}} 元。"
  [0] expr="35*30" start=5 end=10 tl="" tr=""
TXT "价格 12.5–18.9 元"
  [0] expr="12.5–18.9" start=3 end=12 tl="" tr=""
TXT "改动 3 文件, +70/-4 行"
  [0] expr="3" start=3 end=4 tl="" tr=""
  [1] expr=" +70/-4" start=8 end=15 tl="," tr=""
TXT "**0/4** 与 *2/3*"
  [0] expr="0/4" start=2 end=5 tl="**" tr="**"
  [1] expr="2/3" start=11 end=14 tl="*" tr="*"
TXT "中文前缀 1+1 后"
  [0] expr="1+1" start=5 end=8 tl="" tr=""
TXT "a  1+1  b"
  [0] expr="a" start=0 end=1 tl="" tr=""
  [1] expr="1+1" start=3 end=6 tl="" tr=""
  [2] expr="b" start=8 end=9 tl="" tr=""
TXT "50% 1+1"
  [0] expr="50% 1+1" start=0 end=7 tl="" tr=""
TXT "（1080/1081）"
  [0] expr="1080/1081" start=1 end=10 tl="" tr=""
TXT "1–12 / 1–31"
  [0] expr="1–12 / 1–31" start=0 end=11 tl="" tr=""
TXT "交付率 8/13, 正确率 5/8"
  [0] expr="8/13" start=4 end=8 tl="" tr=","
  [1] expr="5/8" start=14 end=17 tl="" tr=""
TXT "9:30+60 与 1:5"
  [0] expr="9" start=0 end=1 tl="" tr=""
  [1] expr="30+60" start=2 end=7 tl="" tr=""
  [2] expr="1" start=10 end=11 tl="" tr=""
  [3] expr="5" start=12 end=13 tl="" tr=""
TXT "0.02/1/4 三档"
  [0] expr="0.02/1/4" start=0 end=8 tl="" tr=""
`

func TestNSWCandGolden(t *testing.T) {
	got := nswGoldenRender()
	if got == nswGoldenWant {
		t.Logf("行为快照一致: %d 条输入 / %d 段文本 / %d 行", len(nswGoldenInputs), len(nswGoldenTexts), strings.Count(got, "\n"))
		return
	}
	wl := strings.Split(nswGoldenWant, "\n")
	gl := strings.Split(got, "\n")
	n := len(wl)
	if len(gl) < n {
		n = len(gl)
	}
	shown := 0
	for i := 0; i < n && shown < 12; i++ {
		if wl[i] != gl[i] {
			t.Errorf("第 %d 行漂移:\n  want %s\n  got  %s", i+1, wl[i], gl[i])
			shown++
		}
	}
	if shown == 0 {
		t.Fatalf("行数不一致: want %d got %d", len(wl), len(gl))
	}
}
