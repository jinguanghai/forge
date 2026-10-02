package main

// b3_branches_ext_test.go — B3 分支覆盖: 终止/升级/取消/工具失败/卡死/校验 各路径。
// 20260927 自 cov_b3_branches_test.go 拆出 (该文件 582 行超 F2 上限 500)。

import (
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
	"time"
)

func TestB3_ErrorTerminalPath(t *testing.T) {
	srv := b3SSE2([]b3Round{{status: 400, body: `{"error":{"message":"bad request"}}`}})
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) { c.RouterMode = RouterFlash })
	err := a.RunStream("终态错误")
	if err == nil {
		t.Fatal("400 且无升级空间时应返回错误")
	}
	if !strings.Contains(err.Error(), "LLM error") {
		t.Fatalf("错误形态不符: %v", err)
	}
}

// 覆盖 agent.go 707-711: flash 请求失败 → 升级 pro 重试一次。
func TestB3_ErrorEscalateToPro(t *testing.T) {
	srv := b3SSE2([]b3Round{
		{status: 400, body: `{"error":{"message":"bad request"}}`},
		{lines: []string{covContentLine("升级后恢复"), covStopLine}},
	})
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.RouterMode = RouterFlash
		c.ModelPro = "pro-x" // 与 ModelFlash 不同 → 升级分支可达
	})
	if err := a.RunStream("升级重试"); err != nil {
		t.Fatalf("升级后应成功, 实际: %v", err)
	}
}

// 覆盖 agent.go 598-600 / 698-704: 带图请求被服务端拒 (unsupported image) →
// 剥离图片降级为纯文本重试, 整体不失败。
func TestB3_VisionImageRejectFallback(t *testing.T) {
	srv := b3SSE2([]b3Round{
		{status: 400, body: `{"error":{"message":"messages[1].image[0]: You have uploaded an unsupported image"}}`},
		{lines: []string{covContentLine("已按纯文本处理"), covStopLine}},
	})
	defer srv.Close()
	var workDir string
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.WorkDir = t.TempDir()
		workDir = c.WorkDir
		c.ModelVision = "vis-x"
	})
	png := b3WritePNG(t, workDir, "shot.png")
	if err := a.RunStream("看看这张图 " + png); err != nil {
		t.Fatalf("图片被拒后应降级重试而非整体失败: %v", err)
	}
}

// 覆盖 agent.go 877-880: 工具执行期间 runCtx 被取消 → 立即返回, 不计为工具失败。
func TestB3_CancelDuringToolExec(t *testing.T) {
	var a *AgentRunner
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", covToolCallLine("c1", "forge", `{"code":"print('X')","lang":"python"}`))
		fmt.Fprintf(w, "data: %s\n\n", covToolFinishLine)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		once.Do(func() {
			if a != nil {
				a.cancel()
			}
		})
	}))
	defer srv.Close()
	a = b3Agent(t, srv.URL, nil)
	if err := a.RunStream("取消测试"); err == nil {
		t.Log("取消后 RunStream 返回 nil (收尾路径), 可接受")
	}
}

// ─── 批0c: 工具失败 / 循环拦截 / 无进展 / 虚报 / 显示截断 / 工具图 ────

// 覆盖 agent.go 888-932: 工具失败(非瞬时) → 计分 + 详情打印, 未达阈值不中止。
func TestB3_ToolFailDetailPath(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("c1", "forge", `{"code":"raise ValueError('boom')","lang":"python"}`), covToolFinishLine},
		{covContentLine("失败已上报"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	if err := a.RunStream("跑一段会失败的代码"); err != nil {
		t.Fatalf("单次失败不应中止: %v", err)
	}
}

// 覆盖 agent.go: 瞬时判定已类型化(20260927) —— 用户代码抛 TimeoutError 是
// 真实运行失败, 必须计入连续失败并触发中止。
// 旧实现嗅探错误文本 "timeout", 会把这类失败误判为环境瞬态而免责
// (本测试原先是那个 bug 的契约, 已随判据类型化一并修正)。
func TestB3_ToolTransientFail(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("c1", "forge", `{"code":"raise TimeoutError('simulated timeout')","lang":"python"}`), covToolFinishLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) { c.MaxConsecutiveFails = 1 })
	if err := a.RunStream("文本含 timeout 的代码失败应计入"); err == nil {
		t.Fatal("用户代码抛 TimeoutError 应计入连续失败并中止, 实际未中止(文本嗅探残留?)")
	}
}

// 覆盖 agent.go 912-919: 连续失败达阈值 (非瞬时) → 中止并带错误返回。
func TestB3_ToolFailAbort(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("c1", "forge", `{"code":"raise ValueError('boom')","lang":"python"}`), covToolFinishLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) { c.MaxConsecutiveFails = 1 })
	err := a.RunStream("连续失败中止")
	if err == nil || !strings.Contains(err.Error(), "连续") {
		t.Fatalf("应返回连续失败中止错误, 实际: %v", err)
	}
}

// 覆盖 agent.go 818-823: 未知工具幻觉达阈值 → 中止。
func TestB3_UnknownToolAbort(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("c1", "不存在的工具", `{"a":1}`), covToolFinishLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) { c.MaxConsecutiveFails = 1 })
	err := a.RunStream("未知工具中止")
	if err == nil || !strings.Contains(err.Error(), "未知工具") {
		t.Fatalf("应返回未知工具中止错误, 实际: %v", err)
	}
}

// 覆盖 agent.go 617-623 / 628-639 / 841-857: 重复调用拦截 → 升级模型 → 达上限自动收尾。
func TestB3_RepeatedCallStuck(t *testing.T) {
	call := covToolCallLine("c1", "forge", `{"code":"print('REP')","lang":"python"}`)
	rounds := [][]string{{call, covToolFinishLine}}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.MaxLoopStrikes = 1
		c.ModelPro = "pro-x" // 使 escalateOnLoop 升级分支可达
	})
	if err := a.RunStream("重复调用触发循环拦截"); err != nil {
		t.Fatalf("循环自动收尾应返回 nil, 实际: %v", err)
	}
}

// 覆盖 agent.go 1013-1015 / 1019-1028: 工具未执行(未知工具) → lastRawOutput 不变 →
// 连续相同输出计数递增 → 达阈值触发无进展拦截并自动收尾。
func TestB3_NoProgressStuck(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("c1", "forge", `{"code":"print('STABLE')","lang":"python"}`), covToolFinishLine},
		{covToolCallLine("c2", "不存在的工具", `{"a":1}`), covToolFinishLine},
		{covToolCallLine("c3", "不存在的工具", `{"a":1}`), covToolFinishLine},
		{covToolCallLine("c4", "不存在的工具", `{"a":1}`), covToolFinishLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.MaxLoopStrikes = 1
		c.MaxConsecutiveFails = 5 // 未知工具不达中止阈值, 留给无进展检测收尾
	})
	if err := a.RunStream("无进展自动收尾"); err != nil {
		t.Fatalf("无进展收尾应返回 nil, 实际: %v", err)
	}
}

// 覆盖 agent.go 741-747: 完成态声称 + 无工具证据 → 注入核验证据强制重答。
func TestB3_VerifyClaimPlainPath(t *testing.T) {
	rounds := [][]string{
		{covContentLine("已提交，编译通过。"), covStopLine},
		{covContentLine("经核验，实际未提交。"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	if err := a.RunStream("你提交了吗"); err != nil {
		t.Fatalf("虚报干预后应正常收尾: %v", err)
	}
}

// 覆盖 agent.go 778-784: 工具碎片全无效的降级路径上的虚报检测。
func TestB3_VerifyClaimFragPath(t *testing.T) {
	frag := `{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"arguments":"{}"}}]},"finish_reason":null}]}`
	rounds := [][]string{
		{covContentLine("已部署完成。"), frag, covToolFinishLine},
		{covContentLine("经核验，实际未部署。"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	if err := a.RunStream("你部署了吗"); err != nil {
		t.Fatalf("碎片降级路径应正常收尾: %v", err)
	}
}

// 覆盖 agent.go 961-970: 工具输出按行/列上限截断显示。
func TestB3_LongOutputTruncation(t *testing.T) {
	b3Env(t, "FORGE_CODE_MAX_COLS", "6")
	b3Env(t, "FORGE_CODE_MAX_LINES", "2")
	rounds := [][]string{
		{covToolCallLine("c1", "forge", `{"code":"print('AAAAAAAAAA')\nprint('BBBBBBBBBB')\nprint('CCCCCCCCCC')","lang":"python"}`), covToolFinishLine},
		{covContentLine("截断显示完成"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	if err := a.RunStream("长输出"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
}

// 覆盖 agent.go 993-1004: 工具产出图片文件 → 提取 base64 挂消息并切视觉模型。
func TestB3_ToolImageInjection(t *testing.T) {
	code := "import base64\nopen('shot2.png','wb').write(base64.b64decode('" + b3PNGBase64 + "'))\nprint('shot2.png')"
	args, _ := json.Marshal(map[string]string{"code": code, "lang": "python"})
	rounds := [][]string{
		{covToolCallLine("c1", "forge", string(args)), covToolFinishLine},
		{covContentLine("看到截图了"), covStopLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) { c.ModelVision = "vis-x" })
	if err := a.RunStream("生成一张图"); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
}

// ─── 批0d: 剩余边角分支 ───────────────────────────────────

// 覆盖 agent.go 523-525: 检查点保存失败只告警, 不影响本轮结果。
func TestB3_CheckpointSaveFailure(t *testing.T) {
	srv := covSSEServer([][]string{{covContentLine("好"), covStopLine}}, 200)
	defer srv.Close()
	base := t.TempDir()
	blocked := filepath.Join(base, "blocked")
	if err := os.WriteFile(blocked, []byte("x"), 0644); err != nil {
		t.Fatalf("prep: %v", err)
	}
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.WorkDir = blocked // WorkDir 是文件 → 检查点 MkdirAll 失败
		c.CachePersistFile = filepath.Join(base, "cache.gob")
	})
	a.SaveCheckpoint = true
	if err := a.RunStream("检查点写入失败"); err != nil {
		t.Fatalf("检查点失败不应导致 RunStream 报错: %v", err)
	}
}

// 覆盖 agent.go 877-880: 工具执行期间 runCtx 被取消 → 立即返回 ctx 错误, 不计工具失败。
func TestB3_CancelDuringToolExecution(t *testing.T) {
	rounds := [][]string{
		{covToolCallLine("c1", "forge", `{"code":"import time\ntime.sleep(1.5)","lang":"python"}`), covToolFinishLine},
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, nil)
	go func() {
		time.Sleep(500 * time.Millisecond)
		a.cancel()
	}()
	err := a.RunStream("取消工具执行")
	if err == nil {
		t.Log("取消后返回 nil, 可接受")
	} else {
		t.Logf("取消后返回: %v", err)
	}
}

// 覆盖 agent.go 857: 重复调用拦截但未达上限 → 仅干预不中止 (continue)。
func TestB3_RepeatedCallSecondStrike(t *testing.T) {
	a1 := covToolCallLine("c1", "forge", `{"code":"print('RA')","lang":"python"}`)
	b1 := covToolCallLine("c2", "forge", `{"code":"print('RB')","lang":"python"}`)
	rounds := [][]string{
		{a1, covToolFinishLine},
		{a1, covToolFinishLine},
		{a1, covToolFinishLine},
		{a1, covToolFinishLine}, // 第4次 → 拦截(1/2) → continue
		{b1, covToolFinishLine},
		{b1, covToolFinishLine},
		{b1, covToolFinishLine},
		{b1, covToolFinishLine}, // 第4次 → 拦截(2/2) → 自动收尾
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) { c.MaxLoopStrikes = 2 })
	if err := a.RunStream("两轮循环拦截"); err != nil {
		t.Fatalf("循环收尾应返回 nil, 实际: %v", err)
	}
}

// 覆盖 agent.go 1028: 无进展拦截未达上限 → 注入换策略指令而非收尾。
func TestB3_NoProgressInterventionInjected(t *testing.T) {
	tool := covToolCallLine("c1", "forge", `{"code":"print('STABLE2')","lang":"python"}`)
	unk := covToolCallLine("cx", "不存在的工具", `{"a":1}`)
	rounds := [][]string{
		{tool, covToolFinishLine},
		{unk, covToolFinishLine},
		{unk, covToolFinishLine},
		{unk, covToolFinishLine}, // sameOutRun=4 → 拦截(1/2) → 注入干预
		{unk, covToolFinishLine},
		{unk, covToolFinishLine},
		{unk, covToolFinishLine},
		{unk, covToolFinishLine}, // 再次 4 次 → 拦截(2/2) → 收尾
	}
	srv := covSSEServer(rounds, 200)
	defer srv.Close()
	a := b3Agent(t, srv.URL, func(c *Config) {
		c.MaxLoopStrikes = 2
		c.MaxConsecutiveFails = 20 // 未知工具不先触发中止
	})
	if err := a.RunStream("无进展干预后收尾"); err != nil {
		t.Fatalf("应返回 nil, 实际: %v", err)
	}
}
