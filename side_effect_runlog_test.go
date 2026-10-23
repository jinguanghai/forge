package main

// side_effect_runlog_test.go — 佐制方 --run 留痕的接线判据 (T12 分母, 20261023)
//
// 病根 (T12 正文 · 10-11 复检结论): side_effect_guard.py 已通电 (10 条规则 + 扫描/执行/
// 校验/自检 + 变异证明), 但运行时机由修复者掌握 —— 没人强制调用。要升级成强制点, 前提是
// 「先有分布」; 而分布需要两个数: --run 的调用频率 与 漏跑导致副作用的次数。
// 实测复检: 脚本没有任何调用留痕数据源 -> 频率与漏跑都无分母 -> 统计不可得 -> 升级条件
// 永远满足不了。这是「判据存在而分布不可得」的死角, 与 lessons 里「备而未用」同型。
//
// 本文件钉住留痕必须在岗 (公理三: 架构即测试):
//
//	R1 接线   脚本必须有 .forge/logs/side_effect_runs.jsonl 落盘 + --run 分支调用留痕函数
//	          (剥离注释后判定 —— 注释里提到不算接线, 与 recover_log_sentinel 同教训)
//	R2 登记   manifest 的 subdirs[logs].patterns 必须登记同一文件名 (位置清单与代码同源,
//	          改一边不改另一边即报红)
//	R3 行为   --stats --json 必须跑通并给出 path/exists/total 字段 (端到端, 只读不写)
//	R4 反向   注释里写 / 只定义函数不调用 -> R1 必须报红 (判据自身要有判据)
//
// 隔离: R3 只读留痕文件, 不落盘、不动生产数据。

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	seRunLogScript   = "defense_system/side_effect_guard.py"
	seRunLogManifest = "defense_system/hygiene_forge_manifest.json"
	seRunLogName     = "side_effect_runs.jsonl"
	seRunLogRel      = ".forge/logs/side_effect_runs.jsonl"
)

// seStripPyComments 去掉 Python 注释 (行内 # 之后, 引号内的 # 保留)。
// 为什么不用正则一把梭: 字符串里的 "#" (如色值、格式串) 会被误剥, 导致接线判定假红。
func seStripPyComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		inS, inD, esc := false, false, false
		cut := -1
		for i := 0; i < len(line); i++ {
			c := line[i]
			if esc {
				esc = false
				continue
			}
			if c == '\\' && (inS || inD) {
				esc = true
				continue
			}
			if c == '\'' && !inD {
				inS = !inS
				continue
			}
			if c == '"' && !inS {
				inD = !inD
				continue
			}
			if c == '#' && !inS && !inD {
				cut = i
				break
			}
		}
		if cut >= 0 {
			line = line[:cut]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// seRunLogProblems 检查接线是否成立 (纯函数: 便于喂反向向量做自证)。
func seRunLogProblems(src string) []string {
	code := seStripPyComments(src)
	var bad []string
	if !strings.Contains(code, seRunLogName) {
		bad = append(bad, "未接线: 代码里没有 "+seRunLogName+" 落盘路径 (只打屏 = 分母仍不可得)")
	}
	if !regexp.MustCompile(`append_run_log\(\s*rec\s*\)`).MatchString(code) {
		// 锚到右括号: 定义行 `def append_run_log(rec, path=...)` 也含 "append_run_log(rec" ——
		// 变异实测 (20261023) 摘掉调用后判据仍绿, 正是被函数定义冒充了接线。
		bad = append(bad, "未接线: --run 的结果没有交给 append_run_log (留痕函数在, 但没被调用)")
	}
	if !strings.Contains(code, "if args.run:") {
		bad = append(bad, "未接线: 找不到 `if args.run:` 分支 (留痕必须挂在真跑分支上, dry-run 不计分母)")
	}
	return bad
}

// TestSideEffectRunLogWiring R1: 真实脚本必须接线。
func TestSideEffectRunLogWiring(t *testing.T) {
	raw, err := os.ReadFile(seRunLogScript)
	if err != nil {
		t.Skipf("佐制方不在位 (开源仓库无 defense_system): %v", err)
	}
	for _, p := range seRunLogProblems(string(raw)) {
		t.Errorf("%s", p)
	}
}

// TestSideEffectRunLogCommentNotWiring R4: 反向向量 —— 注释/空壳不算接线。
func TestSideEffectRunLogCommentNotWiring(t *testing.T) {
	vectors := []struct {
		name string
		src  string
		want int // 期望报出的问题条数 (>0 即"未接线")
	}{
		{"注释里提到", "# append_run_log(rec)  " + seRunLogName + "\n# if args.run:\n", 3},
		{"只定义函数不调用", "def append_run_log(rec, path='x'):\n    pass\n", 3},
		{"字符串里的#不误剥", "RUN_LOG = '.forge/logs/" + seRunLogName + "'  # 分母\nif args.run:\n    append_run_log(rec)\n", 0},
		{"dry-run 分支冒充", "if args.dry_run:\n    append_run_log(rec)\n", 3},
	}
	for _, v := range vectors {
		got := seRunLogProblems(v.src)
		if (len(got) > 0) != (v.want > 0) {
			t.Errorf("%s: 期望未接线=%v, 实得问题 %v", v.name, v.want > 0, got)
		}
	}
}

// TestSideEffectRunLogManifest R2: 留痕位置必须登记进 .forge 清单 (否则 hourly 卫生巡检判未登记)。
func TestSideEffectRunLogManifest(t *testing.T) {
	raw, err := os.ReadFile(seRunLogManifest)
	if err != nil {
		t.Skipf("清单不在位: %v", err)
	}
	var m struct {
		Subdirs []struct {
			Path     string `json:"path"`
			Patterns []struct {
				Glob     string `json:"glob"`
				Kind     string `json:"kind"`
				Category string `json:"category"`
				Keep     int    `json:"keep"`
			} `json:"patterns"`
		} `json:"subdirs"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("清单不可解析: %v", err)
	}
	found := false
	for _, sd := range m.Subdirs {
		if sd.Path != "logs" {
			continue
		}
		for _, p := range sd.Patterns {
			if p.Glob != seRunLogName {
				continue
			}
			found = true
			if p.Kind != "file" {
				t.Errorf("manifest %s kind=%q, 期望 file", seRunLogName, p.Kind)
			}
			if p.Category == "" {
				t.Errorf("manifest %s 缺 category (无分类 = 无判据)", seRunLogName)
			}
			if p.Keep != 1 {
				t.Errorf("manifest %s keep=%d, 期望 1 (单文件 append-only, 轮转在脚本内)", seRunLogName, p.Keep)
			}
		}
	}
	if !found {
		t.Errorf("manifest subdirs[logs].patterns 未登记 %s —— 留痕会落成未登记项", seRunLogName)
	}
	// 双向: 脚本常量与 manifest 文件名必须同一 (改一边不改另一边 = 判据与实现脱节)。
	rawPy, err := os.ReadFile(seRunLogScript)
	if err != nil {
		t.Skipf("佐制方不在位: %v", err)
	}
	code := seStripPyComments(string(rawPy))
	if !strings.Contains(code, seRunLogName) || !strings.Contains(code, filepath.Base(seRunLogRel)) {
		t.Errorf("脚本里的留痕文件名与 manifest 不一致 (期望 %s)", seRunLogName)
	}
}

// TestSideEffectRunLogStatsRuns R3: 统计臂端到端可跑 (只读)。
func TestSideEffectRunLogStatsRuns(t *testing.T) {
	if _, err := os.Stat(seRunLogScript); err != nil {
		t.Skip("佐制方不在位 (开源仓库无 defense_system): skip")
	}
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	// 必须强制 UTF-8: 剥离 PYTHONUTF8 的环境(计划任务/裸 go test)下中文输出按 GBK 落地,
	// 判据会假红 —— 由 python_utf8_sentinel_test.go 钉住 (首版漏写, 全量测试当场抓到)。
	cmd := exec.Command(py, seRunLogScript, "--stats", "--json")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--stats 失败: %v\n%s", err, out)
	}
	var st struct {
		Path   string `json:"path"`
		Exists bool   `json:"exists"`
		Total  int    `json:"total"`
	}
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("--stats --json 输出不可解析 (契约漂移?): %v\n%s", err, out)
	}
	if !strings.HasSuffix(filepath.ToSlash(st.Path), seRunLogRel) {
		t.Errorf("统计路径 %q 不是留痕位置 %q (分母算到别处去了)", st.Path, seRunLogRel)
	}
	if st.Total < 0 {
		t.Errorf("total 非法: %d", st.Total)
	}
}
