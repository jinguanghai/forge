package main

// media_gate_test.go — media gate 接线与成本护栏哨兵 (20260929)
//
// 动机: media gate 是新增的「外部付费 API」gate, 有两个不可退让的不变量:
//   ① 成本护栏拒绝必须让上层看到失败 —— 若 gate 以退出码 0 输出 rejected:true,
//      selfHostedGate 会判 OK:true, LLM 以为成功 (实测踩过: 12 元视频请求被放行上报为成功)。
//   ② 未知 action 同样不得静默成功。
// 这两条用死程序钉住, 不靠「下次注意」。
//
// 零成本: 只跑 models / 拒绝路径, 不触发真实出图出片 (调用真实生成会花钱)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mediaTestForge 构造 toolsDir 指回包目录的 Forge (缓存落 t.TempDir, 不污染真实工作目录)。
func mediaTestForge(t *testing.T) *Forge {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	_, cfg, _ := newHandleCmdAgent(t)
	f := NewForge(t.TempDir(), cfg)
	f.toolsDir = filepath.Join(wd, ForgeToolsDir)
	// 发布包不含 media gate (依赖私有 API 密钥, 见 pluginUnpublishedGates):
	// 脚本缺失时跳过, 否则开源仓库会因「未安装」而报红 —— 与「接线断了」是两回事。
	if _, err := os.Stat(filepath.Join(f.toolsDir, "media_gate.py")); err != nil {
		t.Skipf("media_gate.py 未安装 (发布包不含 media gate): %v", err)
	}
	t.Cleanup(f.Shutdown)
	return f
}

func TestMediaGate_ModelsDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 media gate 子进程端到端")
	}
	f := mediaTestForge(t)
	r := f.forgeGate(`{"action":"models"}`, "media", "")
	if !r.OK {
		t.Fatalf("models 应成功: %+v", r)
	}
	if r.Lang != "media" {
		t.Errorf("Lang = %q, want media", r.Lang)
	}
	for _, want := range []string{"image-01", "max_single_yuan", "draft"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("models 输出缺 %q: %s", want, covTrunc(r.Stdout, 200))
		}
	}
}

// TestMediaGate_CostGuardRejects 成本护栏是硬判据: 超限必须 GateRejected, 不得 OK。
func TestMediaGate_CostGuardRejects(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 media gate 子进程端到端")
	}
	f := mediaTestForge(t)
	cases := []struct {
		name, code, wantReason string
	}{
		{"单次超上限", `{"action":"video","prompt":"x","tier":"hd","duration":10}`, "上限"},
		{"未知档位", `{"action":"video","prompt":"x","tier":"bogus"}`, "档位"},
		{"图片数量越界", `{"action":"image","prompt":"x","n":99}`, "n 必须"},
		{"未知action", `{"action":"bogus"}`, "未知 action"},
	}
	for _, c := range cases {
		r := f.forgeGate(c.code, "media", "")
		if r.OK {
			t.Errorf("%s: 应失败但 OK=true (stdout=%s)", c.name, covTrunc(r.Stdout, 120))
			continue
		}
		msg := r.RejectReason + r.Error
		if !strings.Contains(msg, c.wantReason) {
			t.Errorf("%s: 理由缺 %q, 实际 %q", c.name, c.wantReason, covTrunc(msg, 150))
		}
	}
}

// TestMediaGate_RegistryWired 接线哨兵: 清单/表/注册表/分派四处必须都有 media。
func TestMediaGate_RegistryWired(t *testing.T) {
	found := false
	for _, g := range 铸剑炉_GATES {
		if g == "media" {
			found = true
		}
	}
	if !found {
		t.Fatal("铸剑炉_GATES 缺 media")
	}
	comp, ok := 铸剑炉_COMPILERS["media"]
	if !ok {
		t.Fatal("铸剑炉_COMPILERS 缺 media (超时/执行配置将落默认)")
	}
	if !comp.SelfHosted {
		t.Error("media 必须标 SelfHosted")
	}
	if comp.ExecTimeout <= 0 {
		t.Error("media 必须有正超时")
	}
	regFound := false
	for _, g := range gateRegistry {
		if g.Name == "media" {
			regFound = true
		}
	}
	if !regFound {
		t.Fatal("gateRegistry 缺 media (专家路由 prompt 不会提到它)")
	}
	if !mustHaveOutputGates["media"] {
		t.Error("media 必须列入 mustHaveOutputGates (空输出=判定缺席, 不可当成功)")
	}
}

// TestMediaGate_ScriptPresent gate 脚本必须存在, 否则分派到死边界直接失败。
func TestMediaGate_ScriptPresent(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(wd, ForgeToolsDir, "media_gate.py")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("media_gate.py 未安装 (开源/发布环境属正常): %v", err)
	}
	if st, _ := os.Stat(p); st != nil && st.Size() == 0 {
		t.Fatal("media_gate.py 存在但为空 —— 分派目标不可用")
	}
}
