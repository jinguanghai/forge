// cache_reqshape_test.go: 请求形状埋点的判据。
//
// 防的是"埋点自己说谎": LCP 若恒为 -1 或恒等于 Msgs, 埋点就退化成噪音,
// 断裂依然只能靠猜。故每个用例都断言一个"必须能区分"的形态 ——
// 纯追加 / 中间改 / 头部删 / 固定头漂移, 四态各自可辨。
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// mkMsgs 造 n 条内容互不相同的消息 (内容相同会让指纹判据失去区分度)。
func mkMsgs(n int) []ChatMessage {
	msgs := make([]ChatMessage, n)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = ChatMessage{Role: role, Content: "m" + strconv.Itoa(i)}
	}
	return msgs
}

func TestReqShape_FirstRequestHasNoBaseline(t *testing.T) {
	resetReqShape()
	t.Cleanup(resetReqShape)
	setReqShape(mkMsgs(5))
	got := getReqShape()
	if got.LCP != -1 || got.Prev != -1 {
		t.Fatalf("首次请求应无基线 (LCP=-1, Prev=-1), 实际 LCP=%d Prev=%d", got.LCP, got.Prev)
	}
	if got.Msgs != 5 {
		t.Errorf("Msgs 应为 5, 实际 %d", got.Msgs)
	}
	if got.PID == 0 {
		t.Error("PID 不应为 0")
	}
}

func TestReqShape_PureAppendKeepsPrefix(t *testing.T) {
	resetReqShape()
	t.Cleanup(resetReqShape)
	setReqShape(mkMsgs(5))
	setReqShape(mkMsgs(8))
	got := getReqShape()
	if got.LCP != 5 || got.Prev != 5 {
		t.Fatalf("纯追加时 LCP 应等于上次条数 5, 实际 LCP=%d Prev=%d", got.LCP, got.Prev)
	}
}

func TestReqShape_MidChangeLocatesBreak(t *testing.T) {
	resetReqShape()
	t.Cleanup(resetReqShape)
	setReqShape(mkMsgs(8))
	m := mkMsgs(8)
	m[3].Content = "changed"
	setReqShape(m)
	if got := getReqShape(); got.LCP != 3 {
		t.Fatalf("第 3 条被改, LCP 应为 3, 实际 %d", got.LCP)
	}
}

func TestReqShape_HeadDeletionDetected(t *testing.T) {
	resetReqShape()
	t.Cleanup(resetReqShape)
	base := mkMsgs(10)
	setReqShape(base)
	setReqShape(base[2:])
	got := getReqShape()
	if got.LCP != 0 {
		t.Fatalf("头部删除后与基线零公共前缀, LCP 应为 0, 实际 %d", got.LCP)
	}
	if got.Prev != 10 || got.Msgs != 8 {
		t.Errorf("Prev 应为 10 / Msgs 应为 8, 实际 %d / %d", got.Prev, got.Msgs)
	}
}

func TestReqShape_PfxTracksFixedHead(t *testing.T) {
	resetReqShape()
	t.Cleanup(resetReqShape)
	setReqShape(mkMsgs(6))
	before := getReqShape().Pfx
	setReqShape(mkMsgs(6))
	if again := getReqShape().Pfx; again != before {
		t.Errorf("同输入 Pfx 应稳定: %q vs %q", before, again)
	}
	m := mkMsgs(6)
	m[0].Content = "system drifted"
	setReqShape(m)
	if after := getReqShape().Pfx; after == before {
		t.Error("首条内容变化后 Pfx 必须变化 (否则固定头漂移无法定位)")
	}
}

func TestReqShape_RecordedInCacheStatRow(t *testing.T) {
	dir := t.TempDir()
	withCacheStatDir(t, dir)
	resetReqShape()
	t.Cleanup(resetReqShape)
	setReqShape(mkMsgs(7))
	recordCacheStat("deepseek-flash", 100, 200, 5, "h", false)

	b, err := os.ReadFile(filepath.Join(dir, defaultCacheStatName))
	if err != nil {
		t.Fatalf("未写出统计文件: %v", err)
	}
	var got CacheStat
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &got); err != nil {
		t.Fatalf("统计行不是合法 JSON: %v", err)
	}
	if got.Msgs != 7 {
		t.Errorf("落盘 Msgs 应为 7, 实际 %d", got.Msgs)
	}
	if got.LCP != -1 {
		t.Errorf("首次请求落盘 LCP 应为 -1, 实际 %d", got.LCP)
	}
	if got.PID == 0 {
		t.Error("落盘 PID 不应为 0")
	}
	if got.Pfx == "" {
		t.Error("落盘 Pfx 不应为空")
	}
	if got.Est <= 0 || got.Chars <= 0 {
		t.Errorf("落盘 Est/Chars 应 > 0, 实际 %d / %d", got.Est, got.Chars)
	}
}
