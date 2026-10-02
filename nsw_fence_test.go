package main

// 流式围栏一致性回归 (20260921 修复缺陷 T)。
//
// 背景: 旧实现 nswFenceToggle 对每个分段独立做 strings.Count(s, "```")%2, 而 SSE
// 逐 token 到达时每段常仅 1~2 字节 —— 被切开的反引号永远凑不满三个, 计数恒为 0,
// 围栏状态永不翻转, 代码块内的 {{}} 被当算式求值。后果是同一句回复这次对下次错
// (切分边界由服务端决定, 客户端不可控), 且代码块里的模板语法 {{}} 会被替换成数字。
//
// 判据: 同一输入, 任意切分方式下的流式输出必须与一次性渲染逐字节相同。
// 这是"确定性"契约的直接体现 —— 输出不得依赖分块边界。

import (
	"math/rand"
	"strings"
	"testing"
)

// 覆盖: 单/双围栏、围栏内外混排、3/4/5/6 个反引号、非围栏反引号混排、
// 未闭合标记 + 未闭合围栏、无标记文本。
var exprFenceSamples = []string{
	"```\n{{1+1}}\n```",
	"前\n```go\n{{1+1}}\n```\n后 {{2+3}}",
	"{{1+1}} 与 ```\n{{2+2}}\n```",
	"```\ncode\n```\n```\n{{3+3}}\n```",
	"````\n{{1+1}}\n````",
	"文字 ```{{1+1}}``` 文字 {{2*2}}",
	"无标记文本",
	"```\n{{1+1",
	"```\n{{1+1}}\n``` 尾 {{5*5}} 再 ```\n{{6*6}}\n```",
	"a`b``c```{{1+1}}",
	"`````\n{{1+1}}\n`````",
	"```\n{{1+1}}\n``````\n{{2+2}}\n```",
}

// exprFenceSplit 按指定粒度切分 (模拟 SSE delta 的到达边界)。
func exprFenceSplit(s, mode string, rnd *rand.Rand) []string {
	var out []string
	switch mode {
	case "whole":
		return []string{s}
	case "by1":
		for _, r := range s {
			out = append(out, string(r))
		}
	case "by2":
		for i := 0; i < len(s); i += 2 {
			out = append(out, s[i:min(i+2, len(s))])
		}
	case "by3":
		for i := 0; i < len(s); i += 3 {
			out = append(out, s[i:min(i+3, len(s))])
		}
	case "rand":
		for i := 0; i < len(s); {
			e := min(i+1+rnd.Intn(4), len(s))
			out = append(out, s[i:e])
			i = e
		}
	}
	return out
}

func TestExprFenceStreamConsistency(t *testing.T) {
	modes := []string{"whole", "by1", "by2", "by3", "rand"}
	bad, total := 0, 0
	for si, s := range exprFenceSamples {
		want, _ := nswExprRender(s)
		for _, mode := range modes {
			total++
			rnd := rand.New(rand.NewSource(int64(si*100 + len(mode))))
			var f nswExprFilter
			var sb strings.Builder
			for _, c := range exprFenceSplit(s, mode, rnd) {
				sb.WriteString(f.feed(c))
			}
			sb.WriteString(f.flush())
			if got := sb.String(); got != want {
				bad++
				t.Errorf("样本%d mode=%s 流式与一次性不一致\n  want=%q\n  got =%q", si, mode, want, got)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d/%d 组不一致 —— 输出依赖了分块边界 (确定性契约被破坏)", bad, total)
	}
	t.Logf("围栏一致性: %d 组全部一致", total)
}
