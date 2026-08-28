package main

import (
	"strings"
	"testing"
)

func TestBuildRouteText(t *testing.T) {
	cases := []struct {
		name    string
		flash   string
		pro     string
		mode    string
		textMax int
		wantSub string
		notWant string
	}{
		{"单模型(Flash==Pro)", "deepseek-v4-flash-vision-exp", "deepseek-v4-flash-vision-exp", "auto", 47, "未分档", " ⚡ ↔ "},
		{"单模型深度(仅未分档不重复模型名)", "deepseek-v4-flash-vision-exp", "deepseek-v4-flash-vision-exp", "auto", 47, "未分档（自动路由）", "vision-exp ⚡"},
		{"双模型(Flash!=Pro)", "deepseek-v4-flash", "deepseek-v4-pro", "auto", 47, "⚡", ""},
	}
	for _, c := range cases {
		cfg := &Config{ModelFlash: c.flash, ModelPro: c.pro, RouterMode: c.mode}
		got := buildRouteText(cfg, c.textMax)
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("[%s] 期望包含 %q，实际: %q", c.name, c.wantSub, got)
		}
		if c.notWant != "" && strings.Contains(got, c.notWant) {
			t.Errorf("[%s] 期望不含 %q，实际: %q", c.name, c.notWant, got)
		}
		if w := displayWidth(got); w > c.textMax {
			t.Errorf("[%s] 宽 %d>%d，实际: %q", c.name, w, c.textMax, got)
		}
		// 打印实质输出供人工核对
		t.Logf("[%s] => %s", c.name, got)
	}
}
