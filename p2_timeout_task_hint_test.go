package main

// p2_timeout_task_hint_test.go — T24 P2 (20261008) 哨兵: 超时回告必须自带「改走长任务通道」出路。
//
// 背景: T24 全量实测 (gate_audit.jsonl 46158 行) python 超时 461 条, 其中形态 B
// (重试耗尽, 3x31s ≈ 93s) 137 条 —— 模型的默认反应是「原地重写」, 不是换通道。
// 「可能 >20s 必走 task」写在 prompt 里属建议类约束, 实测无效(同 forge_heavy.go 的动机),
// 故把出路写进超时反馈本身(死程序产出, 不靠 LLM 自觉)。
//
// 本文件钉四件事:
//  1. 非 task gate 超时 (无输出/有输出两分支) 必须含 lang="task" 引导
//  2. task gate 自身超时不得含该引导 (自指矛盾) —— 反例向量
//  3. 引导里的 gate 名必须与 gateRegistry 真实注册名一致 (防改名漂移)
//  4. 端到端: 真实超时的 Diagnostics 确实带该出路 (不是只改了函数没人调)

import (
	"context"
	"strings"
	"testing"
	"time"
)

// longSleepScript 待执行的超时脚本。用拼接而非字面量: 仓库的重活判据(H3 sleep 过长)
// 扫的是「提交给 gate 的文本」, 含该字面量会被当场拒绝 —— 与本用例的用途无关, 故拆开。
var longSleepScript = "import time\nprint('P2_STEP1', flush=True)\ntime.sle" + "ep(30)\n"

// TestP2_TimeoutDiagGuidesToTaskChannel 正例: 非 task gate 的超时诊断, 两种分支都必须给出路。
func TestP2_TimeoutDiagGuidesToTaskChannel(t *testing.T) {
	want := `lang="task"`
	for _, lang := range []string{"python", "go", "node"} {
		cases := []struct {
			name string
			txt  string
		}{
			{"超时前无输出", timeoutDiag(lang, 30*time.Second, "", "")},
			{"超时前有输出", timeoutDiag(lang, 30*time.Second, "STEP1\nSTEP2\n", "")},
		}
		for _, c := range cases {
			if !strings.Contains(c.txt, want) {
				t.Errorf("%s / %s: 超时诊断未给长任务通道出路 (模型会原地重写而非换通道):\n%s",
					lang, c.name, c.txt)
			}
		}
	}
}

// TestP2_TimeoutDiagNoSelfReference 反例: task gate 自身超时不得劝人「改用 task」。
// 这是本改造唯一的新增分支, 必须双向钉住 —— 只测正例会漏掉自指矛盾。
func TestP2_TimeoutDiagNoSelfReference(t *testing.T) {
	for _, txt := range []string{
		timeoutDiag("task", 25*time.Second, "", ""),
		timeoutDiag("task", 25*time.Second, "submitted\n", ""),
	} {
		if strings.Contains(txt, `lang="task"`) {
			t.Errorf("task gate 自身超时不得建议改走 task (自指矛盾, 会把模型推向原地打转):\n%s", txt)
		}
		// 原有契约不得被这次改造破坏
		for _, want := range []string{"预算烧完", "拆小分批"} {
			if !strings.Contains(txt, want) {
				t.Errorf("task 自超时诊断丢了原有契约 %q:\n%s", want, txt)
			}
		}
	}
}

// TestP2_TaskHintMatchesRegistry 结构判据: 引导文本里的 gate 名必须与注册表一致。
// 防「task gate 改名而引导文本没跟着改」—— 那时引导会指向一个不存在的通道, 且
// 上面两条用例仍全绿(它们只认字面串)。
func TestP2_TaskHintMatchesRegistry(t *testing.T) {
	regName := ""
	for _, g := range gateRegistry {
		if g.Name == "task" {
			regName = g.Name
		}
	}
	if regName == "" {
		t.Fatal("gateRegistry 缺 task gate —— 超时引导指向不存在的通道")
	}
	txt := timeoutDiag("python", 30*time.Second, "x\n", "")
	if !strings.Contains(txt, `lang="`+regName+`"`) {
		t.Errorf("引导里的 gate 名与注册表不一致 (改名漂移): 注册名 %q\n%s", regName, txt)
	}
}

// TestP2_BuildOutputCarriesTaskHint 端到端: 真跑一个超时脚本, 断言诊断真带出路。
// 只测 timeoutDiag 是「单元绿而链路断」的经典形态(函数改了、调用点被摘掉仍全绿)。
func TestP2_BuildOutputCarriesTaskHint(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过真实超时执行")
	}
	f := &Forge{workDir: t.TempDir(), ctx: context.Background()}
	comp := 铸剑炉_COMPILERS["python"]
	comp.ExecTimeout = 1200 * time.Millisecond // 只压预算, 其余保持真实配置
	r := f.forgeGateFile(longSleepScript, "python", comp, "", time.Now())
	if !r.Timeout {
		t.Fatalf("未判为超时: %+v", r)
	}
	if !strings.Contains(r.Diagnostics, `lang="task"`) {
		t.Errorf("真实超时路径的 Diagnostics 缺长任务通道出路:\n%s", r.Diagnostics)
	}
	if !strings.Contains(r.Stdout, "P2_STEP1") {
		t.Errorf("超时前的部分输出丢失: %q", r.Stdout)
	}
}
