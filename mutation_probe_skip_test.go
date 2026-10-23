package main

import (
	"os/exec"
	"strings"
	"testing"
)

// mutation_probe_skip_test.go — 变异探针「基线已红」归因的判据 (20261013)
//
// 缺陷: 探针旧版对基线红只记一行 `SKIP 基线已红 rc=1` —— 实测 4 条 SKIP 跨 10 天
// 无法回溯「红在哪」; 而 SKIP 不改变退出码, hourly 只看 rc, 于是「判据从未被验证
// 有鉴别力」= 绿 (假绿)。与 20261012 的展示层假红同型: 检测到 ≠ 报出来。
//
// 修法 = 按基线输出里的 --- FAIL 测试名是否落在本条 sentinel_tests 里分类:
//
//	SKIP_JUDGE 本条哨兵自身红 -> 退出码 1 + 连续 2 次写质量告警
//	SKIP_ENV   无关测试红     -> 不改退出码 (环境抖动不制造噪音)
//	SKIP_BUILD 无 FAIL 行     -> 退出码 1
//
// 为什么 Go 侧还要钉一道: Python --selftest 无人调用即「备而未用」(20261012 教训),
// 归因口径必须进 go test 回归才有人跑。
func TestMutationProbeSkipClassification(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(guardGatePython(), mutationProbePyFile, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--selftest 失败 (SKIP 归因口径不符): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0 失败") {
		t.Errorf("selftest 未报「0 失败」: %s", out)
	}
}
