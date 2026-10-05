package main

// ── regex_engine_degraded_test.go — 穷举降级路径判据 (20261004, P1-3 配套) ──
//
// 为什么需要: greenery 装上后, regex_engine.py 的 ImportError 降级分支变成
// 「永不执行」的死代码 —— 死代码会腐化, 而它承载的是一条硬契约:
//   穷举只能证伪, 找不到反例必须 verdict=undetermined, 不许冒充等价。
// 旧版正是在这里报 likely_equivalent + ok:true, 实测 a+ vs a{1,7} 真值
// not_equivalent(反例 aaaaaaaa) 却被读成「等价」= 假阳性且无降级痕迹。
//
// 判据用 RG_FORCE_BRUTEFORCE=1 强制走降级路径, 直接 exec Python 脚本
// (不经 forge gate, 故不受 gate 缓存影响)。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRegexEngine_DegradedPathIsExplicit(t *testing.T) {
	script := filepath.Join(".forge", "forge-tools", "regex_engine.py")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("regex_engine.py 缺失, 判据失效: %v", err)
	}
	cmd := exec.Command("python", script)
	// pythonUTF8Env 是 python 子进程的统一环境 (强制 UTF-8 + 不留 __pycache__);
	// 直接用 os.Environ() 会被 python_utf8_sentinel_test.go 判红 (中文输出按 GBK 落地)。
	cmd.Env = append(pythonUTF8Env(),
		"RG_TYPE=equivalent",
		"RG_PATTERN=a|b",
		"RG_PATTERN2=b|a",
		"RG_FORCE_BRUTEFORCE=1",
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("执行 regex_engine.py 失败: %v (out=%s)", err, out)
	}
	var res struct {
		Verdict  string `json:"verdict"`
		Engine   string `json:"engine"`
		Degraded bool   `json:"degraded"`
		Reason   string `json:"degraded_reason"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("输出非 JSON: %v (%s)", err, out)
	}
	if res.Verdict != "undetermined" {
		t.Errorf("穷举找不到反例必须 verdict=undetermined(不许冒充等价), 实际 %q", res.Verdict)
	}
	if !res.Degraded || res.Engine != "bruteforce" || res.Reason == "" {
		t.Errorf("降级必须显式可见 (degraded/engine/degraded_reason), 实际 %+v", res)
	}
}
