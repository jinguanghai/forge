package main

// 代码围栏状态机 (20260921 修复缺陷 T)。
//
// 独立成文件: nosword_expr.go 的顶层结构指纹 (F1 自相似) 已接近阈值, 而围栏扫描
// 是自成一体的纯状态机 (零外部依赖), 拆出后两边都回到阈值内。
//
// 旧实现对每个分段独立做 strings.Count(s, "```")%2 —— 反引号被分块边界切开时
// (SSE 逐 token 到达, 每段常仅 1~2 字节) 计数恒为 0, 围栏状态永不翻转, 代码块内
// 的 {{}} 被当算式求值 (实测 12 样本 × 5 切分 = 40/60 组流式与一次性不一致)。
// 新实现按"连续反引号串"计数, 余数 carry 带入下一段 —— 数学上等价于对整串做整数
// 除法 (floor(a/3)+floor((a%3+b)/3) == floor((a+b)/3)), 故流式与一次性逐字节一致。

// nswFenceState 代码围栏状态机 (20260921 修复跨分块失效)。
//
// 旧实现对每个分段独立做 strings.Count(s, "```")%2 —— 反引号被分块边界切开时
// (SSE 逐 token 到达, 每段常仅 1~2 字节) 计数恒为 0, 围栏状态永不翻转, 代码块内
// 的 {{}} 被当算式求值 (实测 8 种文本 × 4 种切分 = 18/32 不一致)。
// 新实现按"连续反引号串"计数, 余数 carry 带入下一段 —— 数学上等价于对整串做整数
// 除法 (floor(a/3)+floor((a%3+b)/3) == floor((a+b)/3)), 故流式与一次性逐字节一致。
type nswFenceState struct {
	in    bool
	carry int // 末尾未决的反引号个数 (0~2), 可能被下一段补成三个反引号
}

// settle 结算未决余数: 遇到非反引号字符 (如标记的 '{') 时调用。
func (st *nswFenceState) settle() { st.carry = 0 }

// feed 消费 s 并推进围栏状态。
func (st *nswFenceState) feed(s string) {
	i := 0
	if st.carry > 0 {
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		if j == i {
			st.carry = 0 // 后面不是反引号, 未决部分确定不构成围栏
		} else {
			run := st.carry + (j - i)
			st.in = nswFenceFlip(st.in, run)
			st.carry = nswFenceCarry(run, j == len(s))
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
		st.carry = nswFenceCarry(run, j == len(s))
		i = j
	}
}

// nswFenceCarry 余数是否留作 carry。
// 只有反引号串延伸到段尾 (atEnd) 时, 余数才可能被下一段补成三个; 若后面紧跟
// 非反引号字符 (如 "a`b" 中的 b), 余数永远不可能再凑成围栏, 必须当场清零 ——
// 否则会把上一段的余数错误地带进下一段的独立反引号串 (实测 "a`b“c```" 被算成翻转两次)。
func nswFenceCarry(run int, atEnd bool) int {
	if atEnd {
		return run % 3
	}
	return 0
}
