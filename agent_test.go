package main

import "testing"

// ---- 佐制重构(I-1) 纯函数测试 (20260808) ----

func TestEffectiveMaxFails(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 5}, {5, 5}, {-3, 5}, {1, 1}, {10, 10},
	}
	for _, c := range cases {
		if got := effectiveMaxFails(c.in); got != c.want {
			t.Errorf("effectiveMaxFails(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCheckRepeatedCall(t *testing.T) {
	h := map[string]int{}
	// 首次调用: 1 次, 未超限
	if blocked, n := checkRepeatedCall("abc", h, 2); blocked || n != 1 {
		t.Errorf("首次调用 blocked=%v n=%d, want false/1", blocked, n)
	}
	// 第二次: 2 次, 未超限(边界=不超)
	if blocked, n := checkRepeatedCall("abc", h, 2); blocked || n != 2 {
		t.Errorf("第二次调用 blocked=%v n=%d, want false/2", blocked, n)
	}
	// 第三次: 3 次, 超限
	if blocked, n := checkRepeatedCall("abc", h, 2); !blocked || n != 3 {
		t.Errorf("第三次调用 blocked=%v n=%d, want true/3", blocked, n)
	}
	// 不同 hash 独立计数
	if blocked, _ := checkRepeatedCall("xyz", h, 2); blocked {
		t.Errorf("不同hash不应超限")
	}
	if h["xyz"] != 1 {
		t.Errorf("xyz 计数=%d, want 1", h["xyz"])
	}
}

func TestShouldAbortOnFails(t *testing.T) {
	cases := []struct {
		consecutive, max int
		want             bool
	}{
		{0, 5, false}, {1, 5, false}, {4, 5, false},
		{5, 5, true}, {6, 5, true}, // 达限/超限中止
		{2, 2, true}, {2, 3, false}, // 边界
		{0, 0, true}, // max=0 时 0>=0 → 中止(与原逻辑一致)
	}
	for _, c := range cases {
		if got := shouldAbortOnFails(c.consecutive, c.max); got != c.want {
			t.Errorf("shouldAbortOnFails(%d,%d) = %v, want %v", c.consecutive, c.max, got, c.want)
		}
	}
}
