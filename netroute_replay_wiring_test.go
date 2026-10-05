package main

// netroute_replay_wiring_test.go — GitHub 红线判据「上线门槛 + 双实现同源」接线哨兵 (20261003)
//
// 背景: netroute.go 的注释写明「任何新判据上线前必须先跑 netroute_replay.py, 过不了门槛
// 就是不能上线」。但注释不是约束 —— 公理三: 架构约束只有两种存在形式, 死程序可执行的
// 判据, 或不存在。本哨兵把那句话钉成可执行判据:
//
//	① 门槛工具存在且自检通过 —— 含变异自检(门槛恒真必须被自己抓到)
//	② 真实回放零假阳性 —— 历史合规样本上命中必须为 0(物理不可能型判据的准入标准)
//	③ 门槛下限钉在源码上 —— 把 MAX_FALSE_POSITIVE 改成 5 会让上一条用例照样全绿,
//	   这正是「判据被一起改坏」的典型形态, 故单独钉住
//	④ 双实现同源 —— Go 侧 mirrorHosts/credMarks/writeMarks 与 Python 侧 _MIRRORS/_CRED/
//	   _WRITE 必须逐字一致。两套实现无法共享代码, 只能共享"表"; 表一旦分叉, 同一条红线
//	   在两处会有两个答案(实测同型事故: 同一份审计被 4 个消费者解读出 3 个数字)
//	⑤ 出口工具自检通过 —— 拒绝文本指的路必须真能走(假出路比没有出路更糟)
//
// 注意断言②钉住的是「零假阳性」这个事实: 若未来某次回放出现命中, 用例报红是提醒人工
// 逐条核验(是真违规还是判据误伤), 不是误报。
import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// netrouteReplayRel / netrouteToolRel 仓库相对路径(单一数据源)。
const (
	netrouteReplayRel = "defense_system/netroute_replay.py"
	netrouteToolRel   = ".forge/forge-tools/netroute.py"
)

// netrouteRun 跑工具。fail-closed: 脚本缺失即 Fatal, 不静默跳过。
// 环境走 pythonUTF8Env —— 中文断言在 GBK 下永不匹配(20261002 事故同源)。
func netrouteRun(t *testing.T, rel string, args ...string) (string, error) {
	t.Helper()
	if _, err := os.Stat(rel); err != nil {
		t.Fatalf("工具缺失(%s): %v —— 判据准入/出口退化为愿望", rel, err)
	}
	cmd := exec.Command(guardGatePython(), append([]string{rel}, args...)...)
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestNetRouteReplaySelfTestPasses 门槛逻辑自身可信(判据的判据)。
func TestNetRouteReplaySelfTestPasses(t *testing.T) {
	t.Parallel()
	out, err := netrouteRun(t, netrouteReplayRel, "--self-test")
	if err != nil {
		t.Fatalf("门槛自检未通过: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"ok": true`) {
		t.Fatalf("自检输出异常: %s", out)
	}
}

// netrouteReplayFacts 回放的机器可读事实(死程序判定, 不做字符串嗅探)。
type netrouteReplayFacts struct {
	OK            bool `json:"ok"`
	Samples       int  `json:"samples"`
	Hit           int  `json:"hit"`
	FalsePositive int  `json:"false_positive"`
	Hits          []struct {
		Src      string `json:"src"`
		Rule     string `json:"rule"`
		Evidence string `json:"evidence"`
	} `json:"hits"`
}

// TestNetRouteReplayZeroFalsePositive 真实回放: 历史合规样本上不得有任何命中。
func TestNetRouteReplayZeroFalsePositive(t *testing.T) {
	t.Parallel()
	out, err := netrouteRun(t, netrouteReplayRel, "--json")
	if err != nil {
		t.Fatalf("回放未过门槛(假阳性或数据源缺失): %v\n%s", err, out)
	}
	body := []byte(out)
	if i := bytes.IndexByte(body, '{'); i >= 0 { // 输出可能带 stderr 前缀
		body = body[i:]
	}
	var f netrouteReplayFacts
	if uerr := json.Unmarshal(body, &f); uerr != nil {
		t.Fatalf("回放输出不是合法 JSON (fail-closed): %v\n%s", uerr, out)
	}
	if !f.OK {
		t.Fatalf("回放判定不过门槛: %s", out)
	}
	if f.Samples <= 0 {
		t.Fatal("回放样本为空 —— 判据失去数据基础(零样本时零命中毫无意义)")
	}
	if f.Hit != 0 || f.FalsePositive != 0 {
		t.Errorf("回放出现 %d 条命中, 逐条核验是真违规还是误伤:\n%s", f.Hit, out)
	}
	for _, h := range f.Hits {
		if h.Rule == "" || h.Src == "" {
			t.Errorf("命中缺归因(判据名/来源): %+v", h)
		}
	}
}

// TestNetRouteReplayVerdictFloor 门槛下限本身不得被调松(判据的判据的判据)。
//
// 理由: 门槛是「合规样本上假阳性 == 0」。若有人把它改成 5, 上一条用例照样全绿 ——
// 那正是「判据被一起改坏」的典型形态。本用例把下限钉在源码常量上。
func TestNetRouteReplayVerdictFloor(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(netrouteReplayRel)
	if err != nil {
		t.Fatalf("读门槛工具失败: %v", err)
	}
	if !strings.Contains(string(raw), "MAX_FALSE_POSITIVE = 0") {
		t.Error("门槛常量被改动或丢失: 期望含 MAX_FALSE_POSITIVE = 0 —— 调松门槛等于取消门槛")
	}
}

// netrouteJudgeTables Python 侧判据表(dump 子命令输出)。
type netrouteJudgeTables struct {
	Mirrors []string `json:"mirrors"`
	Cred    []string `json:"cred"`
	Write   []string `json:"write"`
	Routes  []string `json:"routes"`
}

// TestNetRouteJudgeTablesInSync 双实现同源: Go 侧表与 Python 侧表必须逐字一致。
//
// 这是「判据自身也有判据」的落地: 判据表分叉不会让任何用例报红(两边各自自洽),
// 只会在真实事故里表现为"按工具查是安全的、按 gate 判是被拒的"这种鬼故事。
func TestNetRouteJudgeTablesInSync(t *testing.T) {
	t.Parallel()
	out, err := netrouteRun(t, netrouteToolRel, "dump")
	if err != nil {
		t.Fatalf("判据表导出失败: %v\n%s", err, out)
	}
	body := []byte(out)
	if i := bytes.IndexByte(body, '{'); i >= 0 {
		body = body[i:]
	}
	var d netrouteJudgeTables
	if uerr := json.Unmarshal(body, &d); uerr != nil {
		t.Fatalf("判据表输出不是合法 JSON (fail-closed): %v\n%s", uerr, out)
	}
	cmp := func(name string, goSide, pySide []string) {
		t.Helper()
		if len(goSide) == 0 || len(pySide) == 0 {
			t.Errorf("%s 表为空(一侧或两侧) —— 判据全部失效但用例照样绿", name)
			return
		}
		if len(goSide) != len(pySide) {
			t.Errorf("%s 表长度不一致: Go=%d Python=%d\nGo=%v\nPython=%v", name, len(goSide), len(pySide), goSide, pySide)
			return
		}
		for i := range goSide {
			if goSide[i] != pySide[i] {
				t.Errorf("%s 表第 %d 项分叉: Go=%q Python=%q —— 同一条红线两处两个答案", name, i, goSide[i], pySide[i])
			}
		}
	}
	cmp("镜像域名", mirrorHosts, d.Mirrors)
	cmp("凭据特征", credMarks, d.Cred)
	cmp("写操作特征", writeMarks, d.Write)
	cmp("路由常量", []string{netRouteMirror, netRouteDirect, netRouteSSH, netRouteProxy, netRouteDeny}, d.Routes)
}

// TestNetRouteExitToolSelfTest 出口工具自检通过(拒绝文本指的路必须真能走)。
func TestNetRouteExitToolSelfTest(t *testing.T) {
	t.Parallel()
	out, err := netrouteRun(t, netrouteToolRel, "selftest")
	if err != nil {
		t.Fatalf("出口工具自检未通过: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"fail": 0`) {
		t.Fatalf("出口工具自检有失败项: %s", out)
	}
}
