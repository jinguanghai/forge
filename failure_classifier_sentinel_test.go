package main

// failure_classifier_sentinel_test.go — 「文本不是类型」哨兵 (20260927 批次B / B4)
//
// 病灶: shouldFallback 与 isTransientError 都曾用 strings.Contains 从错误文本
// 嗅探环境性失败。实测三重失效:
//  ①反向误判: 用户代码报错含 "not found" 即被判环境失败 → 无意义换语言重跑;
//  ②正向漏判: 真实产生点文案是中文「不支持的语言: xx」, 与英文 "unsupported
//    language" 永不匹配 → 该分支从未生效过;
//  ③双口径: 一处已改类型化, 另一处漏改。
//
// 判据已收敛为产生点打标(EnvFailure / Timeout)。本文件把「不得回退成文本嗅探」
// 钉成可执行断言 —— 接线只能由死程序判定, 注释与约定都不算证据。
// (函数体提取复用 workdir_binding_test.go 的 extractFuncBody。)

import (
	"strings"
	"testing"
)

// forgeSource 返回全包生产源码。
//
// 原先只读 forge.go —— 源码按职责拆分后, 被判据守护的函数散落到 forge_retry.go /
// forge_audit.go / forge_gate_host.go 等, 写死单文件会让这些断言在搬家后
// 全部静默失效(实测踩到: 4 个用例同时报"函数体提取失败")。
func forgeSource(t *testing.T) string {
	t.Helper()
	return prodGoSources(t)
}

// TestNoTextSniffingInFailureClassifiers 钉死「文本当类型用」不得复辟。
func TestNoTextSniffingInFailureClassifiers(t *testing.T) {
	src := forgeSource(t)
	for _, sig := range []string{"func shouldFallback(", "func isTransientError(", "func isDeterministicFailure("} {
		body := extractFuncBody(src, sig)
		if body == "" {
			t.Fatalf("%s 函数体提取失败(函数被改名/移动? 判据会随之失守)", sig)
		}
		for _, banned := range []string{"strings.Contains", "strings.ToLower"} {
			if strings.Contains(body, banned) {
				t.Errorf("%s 函数体出现 %s: 环境性判据必须来自产生点打标(EnvFailure/Timeout), "+
					"不得从错误文本嗅探\n函数体:\n%s", sig, banned, body)
			}
		}
	}
}

// TestEnvFailureIsStampedAtProducers 钉死产生点打标数量。
// 环境性失败的产生点是可枚举的有限集合; 若有人删掉打标, 该失败会退化成
// 「代码自身失败」→ 不再换语言也不再重试, 静默降级。
func TestEnvFailureIsStampedAtProducers(t *testing.T) {
	src := forgeSource(t)
	// 三处产生点: 不支持的语言 / gate 二进制缺失 / go 工具链缺失
	if got := strings.Count(src, "EnvFailure: true"); got < 3 {
		t.Errorf("EnvFailure 打标点 = %d, want >= 3 (不支持的语言 / gate 二进制缺失 / go 工具链缺失)", got)
	}
	if !strings.Contains(src, "EnvFailure bool") {
		t.Error("ForgeGateResult 缺 EnvFailure 字段")
	}
}

// TestGateRejectedFieldIsStamped 钉死 A3 埋点(主动拒绝可计数)。
// 主动拒绝是 ok=false, 会被失败率统计吞掉; 不单独落字段则
// 「输入被拒」与「闸门坏了」两种信号无法区分。
func TestGateRejectedFieldIsStamped(t *testing.T) {
	src := forgeSource(t)
	if !strings.Contains(src, "GateRejected bool") {
		t.Error("ForgeGateResult 缺 GateRejected 字段")
	}
	if !strings.Contains(src, "parseGateReject") {
		t.Error("缺少 parseGateReject: 主动拒绝未被类型化提取")
	}
	if !strings.Contains(src, `entry["gate_rejected"] = true`) {
		t.Error("auditGate 缺 gate_rejected 埋点")
	}
}
