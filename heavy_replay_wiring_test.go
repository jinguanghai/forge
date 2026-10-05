package main

// heavy_replay_wiring_test.go — 重活判据「上线门槛」工具的接线哨兵 (20261003)
//
// 背景: forge_heavy.go 的注释写明「任何新判据上线前必须先跑 heavy_replay.py,
// 过不了门槛就是不能上线」。但注释不是约束 —— 公理三: 架构约束只有两种存在形式,
// 死程序可执行的判据, 或不存在。本哨兵把那句话钉成可执行判据:
//
//	① 工具存在 —— 否则「判据准入」是愿望, 且脚本会被当孤儿清掉
//	② 门槛逻辑自检通过 —— 含变异自检(恒真/恒假判据必须被挡)与特征向量正反例
//	③ 真实回放确实在拦人 —— 概率型候选判据被门槛挡下
//
// 注意断言③钉住的是「门槛在拦人」这个行为, 不是某个具体数字: 若未来某条候选判据
// 真的过了门槛(精确率 >= 50%), 用例报红是提醒人工复核结论并同步 forge_heavy.go
// 的决策记录, 不是误报。
import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// heavyReplayRel 门槛工具的仓库相对路径 (单一数据源)。
const heavyReplayRel = "defense_system/heavy_replay.py"

// heavyReplayRun 跑门槛工具。fail-closed: 脚本缺失即 Fatal, 不静默跳过。
// 环境走 pythonUTF8Env —— 中文断言在 GBK 下永不匹配(20261002 事故同源)。
func heavyReplayRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if _, err := os.Stat(heavyReplayRel); err != nil {
		t.Fatalf("重活判据门槛工具缺失(%s): %v —— 判据准入退化为愿望", heavyReplayRel, err)
	}
	cmd := exec.Command(guardGatePython(), append([]string{heavyReplayRel}, args...)...)
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestHeavyReplaySelfTestPasses 门槛逻辑自身可信(判据的判据)。
func TestHeavyReplaySelfTestPasses(t *testing.T) {
	t.Parallel()
	out, err := heavyReplayRun(t, "--self-test")
	if err != nil {
		t.Fatalf("门槛自检未通过: %v\n%s", err, out)
	}
	if !strings.Contains(out, "自检通过") {
		t.Fatalf("自检输出异常(中文断言失效?): %s", out)
	}
	// 自检必须真的覆盖三类: 门槛判定 / 变异 / 特征向量
	for _, k := range []string{"门槛判定", "变异", "特征向量"} {
		if !strings.Contains(out, k) {
			t.Errorf("自检覆盖面缩水, 缺 %q: %s", k, out)
		}
	}
}

// heavyReplayFacts 门槛工具的 --json 事实 (死程序判定, 不做字符串嗅探)。
type heavyReplayFacts struct {
	Samples  int `json:"samples"`
	Timeouts int `json:"timeouts"`
	Results  []struct {
		Name      string  `json:"name"`
		Hit       int     `json:"hit"`
		TP        int     `json:"tp"`
		Precision float64 `json:"precision"`
		Recall    float64 `json:"recall"`
		Pass      bool    `json:"pass"`
		Why       string  `json:"why"`
	} `json:"results"`
}

// heavyReplayFactsOf 跑真实回放拿 JSON 事实 (fail-closed: 跑不起来或不是 JSON 即 Fatal)。
func heavyReplayFactsOf(t *testing.T) (heavyReplayFacts, error) {
	t.Helper()
	out, err := heavyReplayRun(t, "--json")
	var f heavyReplayFacts
	// 取【第一个】'{' 起的内容: 输出可能带 stderr 前缀 (bench_report 同款教训)。
	body := []byte(out)
	if i := bytes.IndexByte(body, '{'); i >= 0 {
		body = body[i:]
	}
	if uerr := json.Unmarshal(body, &f); uerr != nil {
		t.Fatalf("回放输出不是合法 JSON (fail-closed): %v\n%s", uerr, out)
	}
	return f, err
}

// TestHeavyReplayGatesProbabilisticCandidates 真实回放: 概率型候选必须被挡在门外。
func TestHeavyReplayGatesProbabilisticCandidates(t *testing.T) {
	t.Parallel()
	f, err := heavyReplayFactsOf(t)
	if err == nil {
		t.Fatal("门槛工具在存在不过门槛的候选时仍返回成功(退出码 0): 门槛失效")
	}
	if f.Samples <= 0 || f.Timeouts <= 0 {
		t.Fatalf("回放样本/超时标签缺失: samples=%d timeouts=%d —— 判据失去数据基础", f.Samples, f.Timeouts)
	}
	if len(f.Results) == 0 {
		t.Fatal("候选判据表为空 —— 门槛无人可拦, 判据准入机制退化为空转")
	}
	blocked := 0
	for _, r := range f.Results {
		if r.Name == "" {
			t.Errorf("候选缺名 —— 命中率无法按判据归因: %+v", r)
		}
		if r.Hit < 0 || r.TP < 0 || r.TP > r.Hit {
			t.Errorf("计数非法(真超时不得多于命中): %+v", r)
		}
		if r.Precision > 1 || r.Recall > 1 {
			t.Errorf("比率越界: %+v", r)
		}
		if !r.Pass {
			blocked++
			if r.Why == "" {
				t.Errorf("判不过门槛却不给理由(拒绝必须自带出路): %+v", r)
			}
		}
	}
	if blocked == 0 {
		t.Error("没有任何候选被判不过门槛 —— 要么门槛被放宽, 要么判据表被清空")
	}
}

// TestHeavyReplayVerdictFloor 门槛下限本身不得被调松 (判据的判据的判据)。
//
// 理由: 门槛是「精确率 >= 50%」。若有人把它改成 0.01, 上一条用例照样全绿 ——
// 那正是「判据被一起改坏」的典型形态。本用例把下限钉在源码常量上。
func TestHeavyReplayVerdictFloor(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(heavyReplayRel)
	if err != nil {
		t.Fatalf("读门槛工具失败: %v", err)
	}
	src := string(raw)
	for _, want := range []string{"TIMEOUT_MS = 25000", `"precision": 0.50`, `"recall": 0.05`} {
		if !strings.Contains(src, want) {
			t.Errorf("门槛常量被改动或丢失: 期望含 %q —— 调松门槛等于取消门槛", want)
		}
	}
}
