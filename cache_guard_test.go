package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReorderMemoryJSON(t *testing.T) {
	raw := []byte(`{"active_task":{"x":1},"identity":"id","last_updated":"20260805","axioms":"ax","zeta":"z"}`)
	out := reorderMemoryJSON(raw)
	s := string(out)
	ti := strings.Index(s, "identity")
	ta := strings.Index(s, "active_task")
	tl := strings.Index(s, "last_updated")
	tz := strings.Index(s, "zeta")
	if ti == -1 || ta == -1 || tl == -1 {
		t.Fatalf("字段缺失: %s", s)
	}
	if !(ti < tl && tl < ta) {
		t.Fatalf("顺序错误 (identity 应在前, active_task 沉底): %s", s)
	}
	// 未知字段追加尾部且排序
	if !(tz > ta) {
		t.Fatalf("未知字段应追加尾部: %s", s)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("非法 JSON: %v", err)
	}
	if m["identity"] != "id" {
		t.Fatalf("identity 值丢失: %v", m)
	}
	out2 := reorderMemoryJSON(raw)
	if string(out) != string(out2) {
		t.Fatalf("非确定性输出")
	}
	// 解析失败应原样返回
	if string(reorderMemoryJSON([]byte("{bad"))) != "{bad" {
		t.Fatalf("坏输入应原样返回")
	}
}

func TestSystemHashSet(t *testing.T) {
	_ = buildSystemPrompt(".")
	if currentSystemHash == "" {
		t.Fatal("currentSystemHash 未设置")
	}
	if len(currentSystemHash) != 16 {
		t.Fatalf("指纹长度应为16: %q", currentSystemHash)
	}
}

func TestPeakHourLogic(t *testing.T) {
	// 高峰边界: 9-11点高峰, 12-13非, 14-17高峰, 18非
	peak := func(h int) bool { return (h >= 9 && h < 12) || (h >= 14 && h < 18) }
	for _, c := range []struct{ h int; want bool }{
		{8, false}, {9, true}, {11, true}, {12, false}, {13, false},
		{14, true}, {17, true}, {18, false}, {19, false},
	} {
		if got := peak(c.h); got != c.want {
			t.Fatalf("hour=%d got=%v want=%v", c.h, got, c.want)
		}
	}
}
