package main

// forge_heavy_test.go — 重模式拒绝权哨兵 (P0-3, 20261002)
//
// 动机: agent_memory.go 的建议类约束对 LLM 无效(实测 214 次超时, 白耗 6486s),
// 升级为 gate 层前置拒绝。本文件钉住三条:
//
//	① 判据向量: 每条判据有正例, 且相邻的"看着像但不是"有反例 ——
//	   只有正例的判据会在收窄后静默失效, 只有反例的判据会静默放宽。
//	② 拒绝必须自带出路: 拒绝文本缺 lang="task" 就是断路(同 sh 退役教训)。
//	③ 接线在 Build: 判据函数正确但没接进 Build = 形同不存在
//	   (判据与放行必须耦合 —— 检测到 ≠ 拦得住)。
//
// 隔离: 走 temp workDir + FORGE_ROOT, 生产 .forge/tasks 与审计零污染。
import (
	"strings"
	"testing"
)

func heavyTestForge(t *testing.T) *Forge {
	t.Helper()
	root := t.TempDir()
	t.Setenv("FORGE_ROOT", root)
	_, cfg, _ := newHandleCmdAgent(t)
	f := NewForge(root, cfg)
	t.Cleanup(f.Shutdown)
	return f
}

// TestHeavyGuard_RuleVectors 正例/反例双向量表。
func TestHeavyGuard_RuleVectors(t *testing.T) {
	cases := []struct {
		name string
		code string
		hit  bool
	}{
		{"os.walk 点根", "import os\nfor r, d, f in os.walk(\".\"):\n    print(r)", true},
		{"os.walk 斜杠根", "os.walk('./')", true},
		{"os.walk getcwd", "os.walk(os.getcwd())", true},
		{"go test 同步", "subprocess.run([\"go\", \"test\", \"./...\"], capture_output=True)", true},
		{"go test 字符串形式", "subprocess.run(\"go test ./...\", shell=True)", true},
		{"go build 同步", "os.system(\"go build -o x.exe .\")", true},
		{"sleep 30", "time.sleep(30)", true},
		{"sleep 20.5", "time.sleep(20.5)", true},
		{"pip install 列表", "subprocess.run([\"pip\", \"install\", \"requests\"])", true},
		{"pip install 裸文本", "pip install requests", true},

		{"sleep 3 不拦", "time.sleep(3)", false},
		{"sleep 19 不拦", "time.sleep(19)", false},
		{"Popen 后台写法放行", "p = subprocess.Popen([\"go\", \"test\", \".\"], stdout=open('l','w'))", false},
		{"go version 不拦", "subprocess.run([\"go\", \"version\"])", false},
		{"子目录 walk 不拦", "os.walk(\"knowledge\")", false},
		{"普通代码", "print(1 + 1)", false},
	}
	for _, c := range cases {
		name, ev, hit := checkHeavyTask(c.code)
		if hit != c.hit {
			t.Errorf("%s: hit=%v want %v (判据=%q 证据=%q)\n  code=%s", c.name, hit, c.hit, name, ev, c.code)
		}
		if hit && (name == "" || ev == "") {
			t.Errorf("%s: 命中但判据名/证据为空 (审计无从归因)", c.name)
		}
	}
}

// TestHeavyGuard_RulesWired 判据表与拒绝文本的结构断言。
// 防"表被清空/判据名重复/文本被削成断路"而哨兵恒真。
func TestHeavyGuard_RulesWired(t *testing.T) {
	if len(heavyRules) < 4 {
		t.Fatalf("判据表被削: %d 条 (P0-3 要求 H1-H4 四条)", len(heavyRules))
	}
	seen := map[string]bool{}
	for _, r := range heavyRules {
		if r.Name == "" || r.Hit == nil {
			t.Fatalf("判据条目残缺: %+v", r)
		}
		if seen[r.Name] {
			t.Errorf("判据名重复(审计归因会混淆): %s", r.Name)
		}
		seen[r.Name] = true
	}
	for _, want := range []string{`lang="task"`, `"action"`, "task_id", ".forge/tasks"} {
		if !strings.Contains(heavyTaskText, want) {
			t.Errorf("拒绝文本缺 %q —— 拒绝不带出路就是断路", want)
		}
	}
}

// TestHeavyGuard_BuildRejects 接线在 Build: 检测到必须拦得住。
func TestHeavyGuard_BuildRejects(t *testing.T) {
	f := heavyTestForge(t)
	out, res, err := f.Build("import time\ntime.sleep(30)", "python", "")
	if err != nil {
		t.Fatalf("拒绝不应产生 error (否则触发失败重试链): %v", err)
	}
	if res == nil || res.OK {
		t.Fatalf("重活必须被拒: res=%+v out=%s", res, covTrunc(out, 200))
	}
	if res.Stage != "rejected" {
		t.Errorf("Stage=%q, want rejected (审计按 stage 分类, 语义必须准确)", res.Stage)
	}
	if !strings.Contains(out, `lang="task"`) {
		t.Errorf("拒绝必须自带出路: %s", covTrunc(out, 300))
	}
	if !strings.Contains(out, "time.sleep") {
		t.Errorf("拒绝应回显命中证据: %s", covTrunc(out, 300))
	}
}

// TestHeavyGuard_TaskLangExempt lang=task 是通道本身, 不得被自己拦下。
func TestHeavyGuard_TaskLangExempt(t *testing.T) {
	f := heavyTestForge(t)
	out, res, _ := f.Build("time.sleep(30)", "task", "")
	if res != nil && res.Stage == "rejected" {
		t.Fatalf("lang=task 被重活拒绝权拦下(自相矛盾): %s", covTrunc(out, 300))
	}
}

// TestHeavyGuard_Switch 开关语义 (关闭只跳过拦截, 不改判据本身)。
func TestHeavyGuard_Switch(t *testing.T) {
	t.Setenv("FORGE_HEAVY_GUARD", "")
	if !heavyGuardEnabled() {
		t.Error("默认必须开启")
	}
	for _, off := range []string{"0", "off", "false", "NO"} {
		t.Setenv("FORGE_HEAVY_GUARD", off)
		if heavyGuardEnabled() {
			t.Errorf("FORGE_HEAVY_GUARD=%q 必须关闭", off)
		}
	}
	for _, on := range []string{"1", "on", "yes"} {
		t.Setenv("FORGE_HEAVY_GUARD", on)
		if !heavyGuardEnabled() {
			t.Errorf("FORGE_HEAVY_GUARD=%q 必须开启", on)
		}
	}
	// 关闭开关不改判据本身: 判据仍命中(只是 Build 不拦)
	t.Setenv("FORGE_HEAVY_GUARD", "0")
	if _, _, hit := checkHeavyTask("time.sleep(30)"); !hit {
		t.Error("关闭开关后判据不应失效 —— 开关只控拦截, 不控判定")
	}
}
