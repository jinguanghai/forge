package main

import (
	"strings"
	"testing"
)

func TestReasoningRendererBasic(t *testing.T) {
	r := newReasoningRenderer()
	out := r.feed("第一行思考内容\n第二行也有\n")
	if !strings.Contains(out, "推理") {
		t.Fatalf("expected header, got %q", out)
	}
	if !strings.Contains(out, "第一行思考内容") {
		t.Fatalf("expected line1, got %q", out)
	}
	if !strings.Contains(out, "│") {
		t.Fatalf("expected prefix bars, got %q", out)
	}
	closeOut := r.close()
	if !strings.Contains(closeOut, "└") {
		t.Fatalf("expected closing frame, got %q", closeOut)
	}
}

func TestReasoningRendererNoOp(t *testing.T) {
	r := newReasoningRenderer()
	if got := r.close(); got != "" {
		t.Fatalf("expected empty close, got %q", got)
	}
}

func TestReasoningRendererPartialLine(t *testing.T) {
	r := newReasoningRenderer()
	out := r.feed("未换行")
	if strings.Contains(out, "│") {
		t.Fatalf("partial line should be buffered, got %q", out)
	}
	closeOut := r.close()
	if !strings.Contains(closeOut, "未换行") {
		t.Fatalf("expected buffered content in close, got %q", closeOut)
	}
}

func TestReasoningRendererCloseOnce(t *testing.T) {
	r := newReasoningRenderer()
	r.feed("一行\n")
	first := r.close()
	second := r.close()
	if first == "" {
		t.Fatalf("first close should emit frame, got empty")
	}
	if second != "" {
		t.Fatalf("second close should be empty (only once), got %q", second)
	}
}

func TestWrapReasoningLine(t *testing.T) {
	if segs := wrapReasoningLine("短行"); len(segs) != 1 {
		t.Fatalf("short line should not wrap, got %d", len(segs))
	}
	long := strings.Repeat("这是一个很长的中文句子用于测试软换行功能 ", 5)
	segs := wrapReasoningLine(long)
	if len(segs) < 2 {
		t.Fatalf("long line should wrap, got %d segs", len(segs))
	}
	avail := terminalAvail()
	for _, sg := range segs {
		if displayWidth(sg) > avail {
			t.Fatalf("segment width %d > avail %d", displayWidth(sg), avail)
		}
	}
}
