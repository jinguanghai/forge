package main

// sh_retired_test.go — sh gate 退役判据 (六西格玛 DMAIC 改善项, 20261001)。
//
// 架构即测试(公理七): "把 gate 删掉"本身不是约束, 死程序判据才是。本文件钉住六件事:
//   V1 入口已关  —— gate 清单 / tool schema enum 不得含 sh (防"删了又加回来")
//   V2 拒绝生效  —— shell 形态 → stage=retired, 且代码确实未执行(探针文件不存在)
//   V3 自带出路  —— 拒绝文本必须含 subprocess 示例(否则退化成"只报错不指路")
//   V4 哨兵契约  —— 检测层仍返回 "sh" 是**故意设计**: 审计据此统计"还有多少请求想用 sh"
//   V5 分支位置  —— 拒绝必须发生在 retryGate 之前(先拒后执行)
//   V6 僵尸分支  —— pickFallback 不得再给 sh 兜底(它已不执行, 兜底无意义)
//
// 背景实测(47 天 31239 次调用): sh 失败率 48.2% (DPMO 482301, σ=1.54 全炉最低档),
// 占全炉 fallback 100% (13/13), 累计白耗 1809s; 自动检测判入的失败率高达 82.1%。
// 根因: forge_lang.go 的 shellCmdRE 把"疑似 shell 形态"一律路由到 sh, 而 sh 在 Windows
// 上执行的是 cmd.exe —— 名实不符, 52.3% 缺陷是 Unix 命令打给 cmd。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// V1a: gate 清单不得含 sh。
func TestShRetired_GateListHasNoSh(t *testing.T) {
	for _, g := range 铸剑炉_GATES {
		if g == "sh" {
			t.Fatalf("gate 清单仍含 sh —— 退役被回退(公理七: 新约束须有判据钉住)")
		}
	}
}

// V1b: 给 LLM 看的 enum 不得含 sh。enum 是"邀请函": 列着 sh 就是在邀请模型用它。
func TestShRetired_SchemaEnumHasNoSh(t *testing.T) {
	var schema struct {
		Function struct {
			Parameters struct {
				Properties struct {
					Lang struct {
						Enum        []string `json:"enum"`
						Description string   `json:"description"`
					} `json:"lang"`
				} `json:"properties"`
			} `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(ForgeToolSchema(), &schema); err != nil {
		t.Fatalf("解析 tool schema 失败: %v", err)
	}
	lang := schema.Function.Parameters.Properties.Lang
	for _, e := range lang.Enum {
		if e == "sh" {
			t.Fatalf("enum 仍含 sh: %v", lang.Enum)
		}
	}
	if !strings.Contains(lang.Description, "退役") {
		t.Errorf("lang description 未告知 sh 退役 —— 模型被拒时无从知道原因")
	}
}

// V2+V3: shell 形态被拒绝, 代码确实未执行, 且回告自带出路。
func TestShRetired_RejectsAndDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	f := NewForge(dir, testCfg())
	defer f.Shutdown()

	probe := filepath.Join(dir, "sh_retired_probe.txt")
	out, res, _ := f.Build("echo probe > sh_retired_probe.txt", "sh", "")
	if res == nil {
		t.Fatal("Build 返回 nil result")
	}
	if res.OK {
		t.Fatalf("sh 请求应被拒绝, 却返回 OK=true")
	}
	if res.Stage != "retired" {
		t.Fatalf("stage = %q, want \"retired\"", res.Stage)
	}
	// 未执行的强证据: 探针文件不存在(若真跑了 cmd.exe, 重定向会建出它)。
	if _, err := os.Stat(probe); err == nil {
		t.Fatalf("sh 代码被执行了 —— 拒绝分支失效, 探针文件已创建: %s", probe)
	}
	// V3: 拒绝必须自带出路。
	if !strings.Contains(out, "subprocess") {
		t.Errorf("拒绝回告未含 subprocess 示例 —— 拒绝不自带出路会退化成瞎试")
	}
	// 别名 bash 同样被拦(防"换个名字绕过")。
	if _, r2, _ := f.Build("ls -la", "bash", ""); r2 == nil || r2.Stage != "retired" {
		t.Errorf("lang=bash 未被拦截: %+v", r2)
	}
	// 自动检测判为 sh 的形态同样被拦(这是实际最高频来源, 失败率 82.1%)。
	if _, r3, _ := f.Build("ls -la", "", ""); r3 == nil || r3.Stage != "retired" {
		t.Errorf("自动检测判 sh 未被拦截: %+v", r3)
	}
}

// V4: 检测层仍返回 "sh" —— 故意保留的哨兵值。
// 若改成返回别的值, 审计就失去"还有多少请求想用 sh"这个度量(也失去来源区分能力)。
func TestShRetired_DetectorKeepsShSentinel(t *testing.T) {
	for _, code := range []string{"ls -la", "echo hi", "#!" + "/bin/sh"} {
		if got := forgeDetectLang(code, ""); got != "sh" {
			t.Errorf("forgeDetectLang(%q) = %q, want \"sh\"(哨兵值)", code, got)
		}
	}
}

// V5: 源码结构断言 —— 拒绝分支必须存在且位于执行之前。
// 防的是"把拒绝分支删掉、enum 也被一并改回去"的连锁退化(判据自身也要有判据)。
func TestShRetired_RetireBranchBeforeExecution(t *testing.T) {
	src, err := os.ReadFile("forge.go")
	if err != nil {
		t.Fatalf("读 forge.go: %v", err)
	}
	s := string(src)
	iRetire := strings.Index(s, "Stage: \"retired\"")
	if iRetire < 0 {
		t.Fatal("forge.go 找不到 Stage: retired —— 拒绝分支被删")
	}
	iExec := strings.Index(s, "result = f.retryGate(")
	if iExec < 0 {
		t.Fatal("forge.go 找不到 retryGate 调用点")
	}
	if iRetire > iExec {
		t.Fatal("拒绝分支位于执行之后 —— 先执行后拒绝等于没拒绝")
	}
	if !strings.Contains(s, "shGateRetiredText") {
		t.Fatal("shGateRetiredText 常量被删")
	}
}

// V6: sh 已不执行, fallback 不应再指向它(僵尸分支)。
func TestShRetired_NoFallbackFromSh(t *testing.T) {
	for _, l := range []string{"sh", "bash"} {
		if got := pickFallback(l); got != "" {
			t.Errorf("pickFallback(%q) = %q, want \"\"(sh 已退役, 无 fallback)", l, got)
		}
	}
}
