package main

// cov_b3_branches_test.go — B3 批0 (六西格玛 Improve): RunStream 未覆盖分支补测。
// 目标: 把 RunStream(agent.go:516-1055) 行覆盖率从 60.5% 提到 >=85%,
// 为后续 B3 拆分(批1-批5)提供"改错就红"的回归网。
// 全部用例走 httptest 假端点, 确定性, 不触外网。
// 批0a: 结构/状态类分支 (检查点 / panic 兜底 / 入口取消 / 固定头断言 / turn 裁剪 / 双调用显示兜底)
//
// ── 残留未覆盖 (全量口径 260/263 = 98.9%, 已超 85% 目标) ──
//
//  • agent.go 679-680  主循环头 runCtx.Done() -> return runCtx.Err()
//    需"轮间取消"时序: 第1轮 nsw 反馈 continue 之后、第2轮循环头之前取消 runCtx。
//    尝试: httptest handler 写响应后调 a.cancel()。结果: 测试进程未正常完成
//    (covprofile 只落 mode 行、无覆盖数据, go test 进程中途消失), 已回退未纳入。
//    性质: 防御性冗余检查 (入口 547 已查过一次), 保留为不覆盖, 非漏测。
//
//  • agent.go 902-904  errDetail = execErr.Error()  (result 为 nil / result.Error 为空时的兜底)
//    现有实现下不可达, 逐条排除:
//      - ErrCancelled (forge.go:427): 返回后 RunStream:877 的 runCtx.Err() 必非 nil,
//        提前 return cerr, 永远走不到 899-904;
//      - ErrShuttingDown (forge.go:429): 要求 f.runCtx == nil 或未取消; 而 RunStream:872
//        已 SetRunCtx(runCtx), effCtx() 恒为 runCtx -> RunStream 内不可达;
//      - ErrTooBusy (forge.go:431): 需等满 60s 的 sem 超时, 测试代价不可接受;
//      - 其余 20+ 处 OK=false 出口 (forge.go 787/801/993/1066/1129/1182/1231/...)
//        均显式设置非空 Error -> 900 行 result.Error != "" 恒真, 永远走 901。
//    结论: 保留为不覆盖, 不在测试中伪装通过。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ─── 通用工具 ─────────────────────────────────────────────

// b3Env 设置环境变量并在用例结束时还原。
func b3Env(t *testing.T, k, v string) {
	t.Helper()
	old, had := os.LookupEnv(k)
	if err := os.Setenv(k, v); err != nil {
		t.Fatalf("setenv %s: %v", k, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(k, old)
		} else {
			_ = os.Unsetenv(k)
		}
	})
}

// b3Agent 构造测试用 AgentRunner; tweak 可在 NewAgentRunner 之前改配置。
func b3Agent(t *testing.T, srvURL string, tweak func(*Config)) *AgentRunner {
	t.Helper()
	cfg := DefaultConfig()
	cfg.APIKey = "test-key"
	cfg.BaseURL = srvURL
	cfg.Model = "test-model"
	cfg.ModelFlash = "test-model"
	cfg.ModelPro = "test-model"
	cfg.ModelVision = ""
	cfg.MiniMaxAPIKey = ""
	cfg.CompactEnabled = false
	cfg.WorkDir = t.TempDir()
	cfg.CachePersistFile = filepath.Join(cfg.WorkDir, "cache.gob")
	if tweak != nil {
		tweak(cfg)
	}
	a, err := NewAgentRunner(cfg)
	if err != nil {
		t.Fatalf("NewAgentRunner: %v", err)
	}
	a.SaveCheckpoint = false
	t.Cleanup(func() { a.Shutdown() })
	return a
}

// b3SSE 按轮次回放原始 SSE data 行; status!=0/200 时直接返回错误体。
// 轮次用尽后重复最后一轮。
func b3SSE(rounds [][]string, status int, errBody string) *httptest.Server {
	var mu sync.Mutex
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		n := idx
		idx++
		mu.Unlock()
		if status != 0 && status != 200 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, errBody)
			return
		}
		if n >= len(rounds) {
			n = len(rounds) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range rounds[n] {
			fmt.Fprintf(w, "data: %s\n\n", line)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
}

// b3ToolCallLine 指定 index 的工具调用 delta (covToolCallLine 硬编码 index 0, 无法同轮双调用)。
func b3ToolCallLine(idx int, id, name, args string) string {
	ab, _ := json.Marshal(args)
	return fmt.Sprintf(`{"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":%d,"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}]},"finish_reason":null}]}`,
		idx, id, name, ab)
}

// b3PNGBase64 1x1 透明 PNG (供 magic 校验)。
const b3PNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// b3WritePNG 在 dir 下写一张合法 PNG, 返回完整路径。
func b3WritePNG(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(b3PNGBase64)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	return p
}

// ─── 批0a: 结构 / 状态类分支 ─────────────────────────────

// 覆盖 agent.go 522-525: 成功收尾时保存检查点。
func TestB3_CheckpointSavedOnSuccess(t *testing.T) {
	srv := covSSEServer([][]string{{covContentLine("记住了"), covStopLine}}, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	a.SaveCheckpoint = true
	if err := a.RunStream("记住这条"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if _, err := os.Stat(checkpointPath(a.cfg.WorkDir)); err != nil {
		t.Fatalf("检查点未落盘: %v", err)
	}
}

// 覆盖 agent.go 530-533: 函数内 panic 被兜底为错误返回, 不炸进程。
func TestB3_PanicRecovered(t *testing.T) {
	srv := covSSEServer([][]string{{covContentLine("x"), covStopLine}}, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	saved := a.cfg
	a.cfg = nil // 制造 nil 解引用 panic
	err := a.RunStream("触发 panic")
	a.cfg = saved
	if err == nil || !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("期望 panic recovered 错误, 实际: %v", err)
	}
}

// 覆盖 agent.go 549-550: 入口处 runCtx 已取消 → 直接返回 ctx 错误。
func TestB3_EntryCtxCanceled(t *testing.T) {
	srv := covSSEServer([][]string{{covContentLine("x"), covStopLine}}, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	a.cancel()
	if err := a.RunStream("已取消"); err == nil {
		t.Fatal("ctx 已取消时 RunStream 应返回错误")
	}
}

// 覆盖 agent.go 565-569: 固定头一致性断言失败 → 告警并记缓存异常。
func TestB3_HeadInvariantBroken(t *testing.T) {
	srv := covSSEServer([][]string{{covContentLine("好"), covStopLine}}, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	a.headLen = a.headLen + 999 // 破坏不变式
	if err := a.RunStream("断言失败"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
}

// 覆盖 agent.go 1042-1044: messages 超 300 条 → trimTurnMessages。
func TestB3_TrimTurnMessagesOverLimit(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("call_t", "forge", `{"code":"print('T')","lang":"python"}`), covToolFinishLine},
		{covContentLine("完成"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	for i := len(a.history); i < 300; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		a.history = append(a.history, ChatMessage{Role: role, Content: fmt.Sprintf("历史消息 %d", i)})
	}
	if err := a.RunStream("超长历史"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
}

// 覆盖 agent.go 865-867: 同轮两个工具调用 → 第二个的 streamedCode 已被清空,
// 走 displayToolCode 兜底整块打印。
func TestB3_TwoToolCallsDisplayFallback(t *testing.T) {
	rounds := [][]string{
		{
			b3ToolCallLine(0, "call_a", "forge", `{"code":"print('A')","lang":"python"}`),
			b3ToolCallLine(1, "call_b", "forge", `{"code":"print('B')","lang":"python"}`),
			covToolFinishLine,
		},
		{covContentLine("都跑完了"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	if err := a.RunStream("跑两段"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
}

// ─── 批0b: 错误路径 / 识图降级 / 升级重试 / 取消 ─────────────

// b3Round 单轮响应: status 非 200 时返回错误体, 否则回放 SSE 行。
type b3Round struct {
	status int
	body   string
	lines  []string
}

// b3SSE2 按轮次回放不同 status / SSE 行的假 LLM 端点。
func b3SSE2(rounds []b3Round) *httptest.Server {
	var mu sync.Mutex
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		n := idx
		idx++
		mu.Unlock()
		if n >= len(rounds) {
			n = len(rounds) - 1
		}
		rd := rounds[n]
		if rd.status != 0 && rd.status != 200 {
			w.WriteHeader(rd.status)
			_, _ = io.WriteString(w, rd.body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range rd.lines {
			fmt.Fprintf(w, "data: %s\n\n", l)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
}

// 覆盖 agent.go 694-698 / 716-724: 流式请求失败且无升级空间 → 终态错误返回。
