package main

// toolstream_wire_test.go — 工具代码流式显示的接线哨兵 + 行为用例。
//
// 教训 (20260922): toolstream.go 于 796feb2 (09-10) 入库, 提交信息写着
// "toolstream.go: 工具代码流式显示 (toolCodeStreamer), style.go 与 agent.go 均有调用",
// 但该提交只新增了 3 个文件 —— 调用点从未入库, 文件当了 12 天死代码, 用户看到的
// "流式显示"时有时无。故把"调用点存在性"固化为死程序判定(哨兵), 不靠声称。

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestToolCodeStreamerIsWired 接线哨兵: 主干必须真的创建并驱动流式显示器。
func TestToolCodeStreamerIsWired(t *testing.T) {
	// B3 批1: 判据由"agent.go 内含"放宽为"包内生产代码含" —— 去重判定
	// (streamed != params.Code) 随工具循环迁至 agent_stream.go。功能接线是
	// 包级属性, 不是文件级属性 (见 wiring_sentinel_test.go:prodGoSources)。
	s := prodGoSources(t)
	for _, need := range []string{
		"&toolCodeStreamer{}",     // 创建
		"codeStreamer.feed(",      // 分片喂入
		"codeStreamer.flush()",    // 收尾
		"codeStreamer.code()",     // 全文(供去重)
		"a.streamedCode",          // 去重判据落值
		"streamed != params.Code", // 去重判定
	} {
		if !strings.Contains(s, need) {
			t.Errorf("生产代码缺少 %q —— 流式显示未接线(死代码)", need)
		}
	}
}

// TestToolCodeStreamerFeedsLineByLine 分块喂入: 完整行立即产出, 半行不产出, flush 补齐。
func TestToolCodeStreamerFeedsLineByLine(t *testing.T) {
	var s toolCodeStreamer
	// 显式带 lang: 本用例测分块产出, lang 等待逻辑由 LangLate 用例单独覆盖
	out := s.feed(`{"lang":"python","code":"line1\nli`, "python")
	if !strings.Contains(out, "line1") {
		t.Fatalf("首个完整行未产出: %q", out)
	}
	if strings.Contains(out, "line2") {
		t.Fatalf("未完整行被提前产出: %q", out)
	}
	out2 := s.feed(`ne2\nline3`, "python")
	if !strings.Contains(out2, "line2") {
		t.Fatalf("第二块未产出 line2: %q", out2)
	}
	if strings.Contains(out2, "line3") {
		t.Fatalf("未完整行 line3 被提前产出: %q", out2)
	}
	out3 := s.flush()
	if !strings.Contains(out3, "line3") {
		t.Fatalf("flush 未补齐残余行: %q", out3)
	}
	if got := s.code(); got != "line1\nline2\nline3" {
		t.Fatalf("code() = %q, 期望 line1\\nline2\\nline3", got)
	}
	if !s.head {
		t.Fatal("s.head 应为 true (已流式打印过内容)")
	}
	if out := s.flush(); out != "" {
		t.Fatalf("重复 flush 应无输出, got %q", out)
	}
}

// TestToolCodeStreamerNoCodeArg 非代码参数(如 self gate 的 replace 指令)不产出内容。
func TestToolCodeStreamerNoCodeArg(t *testing.T) {
	var s toolCodeStreamer
	if out := s.feed(`{"action":"deploy"}`, "self"); out != "" {
		t.Fatalf("无 code 字段却产出: %q", out)
	}
	if out := s.flush(); out != "" {
		t.Fatalf("无 code 字段 flush 却产出: %q", out)
	}
	if s.head {
		t.Fatal("s.head 应为 false (未产出内容)")
	}
	if s.code() != "" {
		t.Fatalf("code() 应为空, got %q", s.code())
	}
}

// TestToolCodeStreamerLangFromArgs 语言标签取自 arguments 的 lang 字段(工具名不是语言)。
func TestToolCodeStreamerLangFromArgs(t *testing.T) {
	var s toolCodeStreamer
	out := s.feed(`{"code":"x=1\ny=2\n","lang":"python"}`, "forge")
	if !strings.Contains(out, "PYTHON") {
		t.Fatalf("语言标签未从 arguments 取: %q", out)
	}
}

// TestToolCodeStreamerHighlightPreserved 高亮只插 ANSI 转义, 不改动原始字符。
func TestToolCodeStreamerHighlightPreserved(t *testing.T) {
	var s toolCodeStreamer
	out := s.feed("{\"code\":\"def f():\\n    return 1\\n\",\"lang\":\"python\"}", "forge")
	plain := reAnsiStrip.ReplaceAllString(out, "")
	if !strings.Contains(plain, "def f():") || !strings.Contains(plain, "return 1") {
		t.Fatalf("高亮改动了原始字符: %q", out)
	}
	// 不变量: 去 ANSI 后的代码行必须与输入逐字节一致(高亮只加色, 不增删字符)
	if !strings.Contains(plain, "def f():\n") || !strings.Contains(plain, "    return 1\n") {
		t.Fatalf("去色后应逐字节还原代码: %q", plain)
	}
}

// TestToolCodeStreamerDecodeNoHang 未知转义/不完整 \u 不得死循环(旧实现在此空转)。
func TestToolCodeStreamerDecodeNoHang(t *testing.T) {
	done := make(chan string, 1)
	go func() {
		done <- extractStringField(`{"code":"a\x41b"}`, "code")
	}()
	select {
	case got := <-done:
		if got == "" {
			t.Fatal("未知转义应返回已解码前缀")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("decodeJSONString 疑似死循环")
	}
	if got := extractStringField(`{"code":"abc\u12`, "code"); got == "" {
		t.Fatal("不完整 \\u 应返回已解码前缀")
	}
}

// TestToolCodeStreamerMaxLines 行数上限生效(默认 0 = 不限)。
func TestToolCodeStreamerMaxLines(t *testing.T) {
	t.Setenv("FORGE_CODE_MAX_LINES", "2")
	var s toolCodeStreamer
	out := s.feed("{\"lang\":\"go\",\"code\":\"a\\nb\\nc\\nd\\n\"}", "go")
	if !strings.Contains(out, "a") || !strings.Contains(out, "b") {
		t.Fatalf("上限内应正常输出: %q", out)
	}
	if strings.Contains(out, "c") {
		t.Fatalf("超上限行不应输出: %q", out)
	}
	if !strings.Contains(out, "FORGE_CODE_MAX_LINES") {
		t.Fatalf("应提示已达上限: %q", out)
	}
}

// TestProcessStreamStreamsToolCode 端到端: 工具代码必须落到 stderr, 语言标签取自
// arguments.lang, streamedCode 落值供主循环去重, 且同段代码不重复显示。
func TestProcessStreamStreamsToolCode(t *testing.T) {
	f := sinkStderr(t)
	a := &AgentRunner{stats: &SessionStats{}}
	ch := make(chan StreamEvent, 4)
	var content, reason strings.Builder
	var tc []ToolCall

	args1 := `{"lang":"python","code":"print(1)\nprint(`
	args2 := `2)\n"}`
	ch <- StreamEvent{Type: "tool_call_delta", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "forge", Arguments: args1}}}}
	ch <- StreamEvent{Type: "tool_call_delta", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "forge", Arguments: args2}}}}
	ch <- StreamEvent{Type: "tool_call_done", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "forge", Arguments: args1 + args2}}}}
	close(ch)

	if err := a.processStream(ch, &content, &reason, &tc, false, nil); err != nil {
		t.Fatalf("processStream: %v", err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("读 stderr 失败: %v", err)
	}
	plain := reAnsiStrip.ReplaceAllString(string(b), "")
	if !strings.Contains(plain, "print(1)") || !strings.Contains(plain, "print(2)") {
		t.Fatalf("工具代码未流式落到 stderr: %q", plain)
	}
	if n := strings.Count(plain, "print(1)"); n != 1 {
		t.Fatalf("同段代码重复显示 %d 次: %q", n, plain)
	}
	if !strings.Contains(plain, "PYTHON") {
		t.Fatalf("语言标签缺失(应取自 arguments.lang): %q", plain)
	}
	// code 值本身以 \n 结尾 → 解码全文含尾换行; 主循环按此与 params.Code 逐字节比对去重
	if a.streamedCode != "print(1)\nprint(2)\n" {
		t.Fatalf("streamedCode 未落值(主循环去重失效): %q", a.streamedCode)
	}
}

// TestProcessStreamEmitsBeforeDone 时序哨兵: 代码必须在 tool_call_done 之前就落
// stderr —— 这正是"流式"与"整块"的分水岭。旧实现(死代码)此断言必失败。
func TestProcessStreamEmitsBeforeDone(t *testing.T) {
	f := sinkStderr(t)
	a := &AgentRunner{stats: &SessionStats{}}
	ch := make(chan StreamEvent)
	var content, reason strings.Builder
	var tc []ToolCall
	firstSent := make(chan struct{})
	proceed := make(chan struct{})

	go func() {
		ch <- StreamEvent{Type: "tool_call_delta", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "forge", Arguments: `{"lang":"go","code":"var a = 1\n"`}}}}
		close(firstSent)
		<-proceed // 卡住: 在测试读完 stderr 之前不发 tool_call_done
		ch <- StreamEvent{Type: "tool_call_done", ToolCalls: []ToolCall{{Function: FunctionCall{Name: "forge", Arguments: `{"lang":"go","code":"var a = 1\n"`}}}}
		close(ch)
	}()
	done := make(chan error, 1)
	go func() { done <- a.processStream(ch, &content, &reason, &tc, false, nil) }()

	<-firstSent
	var plain string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(f.Name()); err == nil {
			plain = reAnsiStrip.ReplaceAllString(string(b), "")
			if strings.Contains(plain, "var a = 1") {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(plain, "var a = 1") {
		t.Fatalf("tool_call_done 之前代码未落 stderr —— 流式未生效: %q", plain)
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("processStream: %v", err)
	}
}

// TestToolCodeStreamerLangLate lang 在 code 之后的分片里到达时,
// 边框语言标签不得被钉死成 CODE(实测 e2e 缺陷)。
func TestToolCodeStreamerLangLate(t *testing.T) {
	var s toolCodeStreamer
	out := s.feed(`{"code":"x = 1\ny = 2\n`, "forge") // 首片: 只有 code, 无 lang
	out += s.feed(`","lang":"python"}`, "forge")      // 次片: 补上 lang
	out += s.flush()
	plain := reAnsiStrip.ReplaceAllString(out, "")
	if !strings.Contains(plain, "PYTHON") {
		t.Fatalf("lang 晚到时语言标签应为 PYTHON: %q", plain)
	}
	if strings.Contains(plain, "─── CODE") {
		t.Fatalf("语言标签被钉死成 CODE: %q", plain)
	}
	if !strings.Contains(plain, "x = 1") || !strings.Contains(plain, "y = 2") {
		t.Fatalf("代码内容丢失: %q", plain)
	}
}

// TestToolCodeStreamerEmitsWhenLangUnknownButLong lang 始终缺失但内容够多时,
// 必须照常流式输出(不能因为等 lang 把流式卡死)。
func TestToolCodeStreamerEmitsWhenLangUnknownButLong(t *testing.T) {
	var s toolCodeStreamer
	big := strings.Repeat("z", toolCodeLangWaitBytes+10)
	out := s.feed(`{"code":"`+big+`\n`, "forge")
	if out == "" {
		t.Fatal("内容超过等待阈值时应照常输出")
	}
	if !strings.Contains(reAnsiStrip.ReplaceAllString(out, ""), "zzz") {
		t.Fatalf("内容缺失: %q", out)
	}
}
