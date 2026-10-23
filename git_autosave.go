package main

// git_autosave.go — 会话级自动存档 (20261009, 主人定案「存档自动化」)
//
// 双轨制 (为什么不直接 commit 到主线):
//   轨道一 主线 refs/heads/main —— 开发档案, 只增不改;
//   轨道二 refs/autosave/<ts>  —— 自动存档, 独立引用, 超期整条删掉。
// 自动存档若直接 commit 到主线, 「删 30 天前的」就变成改写整条提交链
// (parent 链断开 -> 全链 hash 重算, 不可逆); 分轨后删除只是一次
// update-ref -d, 主线一个字节不动。
//
// 实现: 临时 index (GIT_INDEX_FILE) 收集工作区 -> git write-tree ->
// git commit-tree -p HEAD -> git update-ref。全程不碰真实 index、不移动
// HEAD、不改工作区文件 —— 三者任一被碰都会干扰主人正在做的开发
// (最坏是把已 staged 的内容弄丢)。
//
// scope 与 self/upgrade 快照完全一致 (复用 snapshotAdd): 只收「已跟踪文件的
// 改动/删除 + 根目录白名单内的新增」, 临时日志/密钥/子目录新增一律不进。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// autosaveKeepDays 自动存档保留天数 (主人定案: 放一个月)。
	autosaveKeepDays = 30
	// autosaveRefPrefix 自动存档的独立引用命名空间。
	autosaveRefPrefix = "refs/autosave/"
)

// autosaveRefRe 从 ref 名提取时间戳; 允许同名冲突时的 "-N" 后缀。
var autosaveRefRe = regexp.MustCompile(`^refs/autosave/(\d{8}_\d{6})`)

// autosaveSuppressed 测试期抑制开关 (由 _test.go 的 init 置位)。
// 目的: 任何以生产根为 WorkDir 的用例调 finalize 时, 不得往真实仓库写 ref
// (教训「测试隔离必须覆盖生产根」)。gitAutosave 本身不做豁免, 以便用例
// 能端到端验证它。
var autosaveSuppressed bool

// autosaveEnabled 环境开关: FORGE_AUTOSAVE=0 关闭。
func autosaveEnabled() bool { return os.Getenv("FORGE_AUTOSAVE") != "0" }

// autosaveOnTurn 会话收尾的自动存档入口 (finalize 调用)。
// 抑制开关只拦这一个入口, 不拦 gitAutosave —— 用例需要能直接打核心函数。
func autosaveOnTurn(workDir string) string {
	if autosaveSuppressed {
		return ""
	}
	return gitAutosave(workDir, "turn")
}

// gitAutosave 把当前工作区状态固化为一个独立 ref (不移动主线)。
// 返回 commit hash; 无改动 / 失败 / 关闭 / 非 git 仓库时返回空串。
func gitAutosave(workDir, reason string) string {
	if !autosaveEnabled() {
		return ""
	}
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return "" // 非 git 仓库, 静默跳过
	}
	// 临时 index: 绝不碰真实 index (工作区可能已有 staged 内容)
	tmp, err := os.CreateTemp("", "forge_autosave_idx_")
	if err != nil {
		return ""
	}
	idxPath := tmp.Name()
	tmp.Close()
	os.Remove(idxPath) // 交给 git 自行创建; 留着空文件同样可用
	defer os.Remove(idxPath)

	env := append(os.Environ(),
		"GIT_INDEX_FILE="+idxPath,
		"GIT_AUTHOR_NAME=forge-auto", "GIT_AUTHOR_EMAIL=auto@forge.local",
		"GIT_COMMITTER_NAME=forge-auto", "GIT_COMMITTER_EMAIL=auto@forge.local",
		"GIT_TERMINAL_PROMPT=0")
	run := func(args ...string) ([]byte, error) {
		c := exec.Command("git", args...)
		c.Dir = workDir
		c.Env = env
		return c.CombinedOutput()
	}
	// 1) 临时 index 以 HEAD 为基线 (空仓库退化为空 index)
	headTree := ""
	if out, err := run("rev-parse", "HEAD^{tree}"); err == nil {
		headTree = strings.TrimSpace(string(out))
		if _, err := run("read-tree", "HEAD"); err != nil {
			return ""
		}
	} else if _, err := run("read-tree", "--empty"); err != nil {
		return ""
	}
	// 2) 与 self/upgrade 快照同一 scope (复用 snapshotAdd, 不另写一套)
	if err := snapshotAdd(workDir, env); err != nil {
		return ""
	}
	// 3) 写 tree 并与 HEAD 比较: 无差异不建 ref (无改动 = 不产生噪音存档)
	out, err := run("write-tree")
	if err != nil {
		return ""
	}
	tree := strings.TrimSpace(string(out))
	if tree == "" || tree == headTree {
		return ""
	}
	// 4) 建 commit (父 = HEAD, 形成独立链; 不移动任何分支指针)
	ts := time.Now().Format("20060102_150405")
	args := []string{"commit-tree", tree, "-m", "autosave: " + ts + " " + sanitizeReason(reason)}
	if headTree != "" {
		args = append(args, "-p", "HEAD")
	}
	cout, err := run(args...)
	if err != nil {
		return ""
	}
	commit := strings.TrimSpace(string(cout))
	if commit == "" {
		return ""
	}
	// 5) 挂到独立命名空间 (同一秒重复存档时加 -N 后缀, 不覆盖前一次)
	ref := autosaveRefPrefix + ts
	for i := 2; ; i++ {
		if _, err := run("show-ref", "--verify", "--quiet", ref); err != nil {
			break
		}
		ref = fmt.Sprintf("%s%s-%d", autosaveRefPrefix, ts, i)
	}
	if _, err := run("update-ref", ref, commit); err != nil {
		return ""
	}
	// 6) 顺手清理超期存档 (失败不影响本次存档)
	gitAutosavePrune(workDir, autosaveKeepDays)
	return commit
}

// gitAutosavePrune 删除超过 keepDays 天的自动存档 ref, 返回删除条数。
// 只动 refs/autosave/* —— 主线分支/tag 永不在删除范围内 (双轨制的全部意义)。
func gitAutosavePrune(workDir string, keepDays int) int {
	if !autosaveEnabled() || keepDays <= 0 {
		return 0
	}
	run := func(args ...string) ([]byte, error) {
		c := exec.Command("git", args...)
		c.Dir = workDir
		c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		return c.CombinedOutput()
	}
	out, err := run("for-each-ref", "--format=%(refname)", autosaveRefPrefix)
	if err != nil {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -keepDays)
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		ref := strings.TrimSpace(line)
		if ref == "" {
			continue
		}
		t, ok := autosaveRefTime(ref)
		if !ok || !t.Before(cutoff) {
			continue
		}
		if _, err := run("update-ref", "-d", ref); err == nil {
			n++
		}
	}
	return n
}

// autosaveRefTime 从 ref 名解析存档时间戳 (仅认 autosave 命名空间)。
func autosaveRefTime(ref string) (time.Time, bool) {
	m := autosaveRefRe.FindStringSubmatch(ref)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("20060102_150405", m[1], time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// autosaveWorkDir 定位仓库根: 优先可执行文件所在目录, 回退当前目录;
// 都不含 .git 时返回首选候选 (由 gitAutosave 自行判空跳过)。
func autosaveWorkDir() string {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		cands = append(cands, wd)
	}
	for _, c := range cands {
		if _, err := os.Stat(filepath.Join(c, ".git")); err == nil {
			return c
		}
	}
	if len(cands) > 0 {
		return cands[0]
	}
	return ""
}

// gitAutosaveCLI 是 --git-autosave 的实现 (hourly 兜底调度臂调用)。
// 只依赖 git, 不加载配置、不初始化 agent —— 秒级返回, 供计划任务使用。
func gitAutosaveCLI() {
	wd := autosaveWorkDir()
	if wd == "" {
		fmt.Println("autosave: 无法定位工作目录, 跳过")
		return
	}
	if h := gitAutosave(wd, "hourly"); h == "" {
		fmt.Println("autosave: 无改动或非 git 仓库, 跳过")
		return
	}
	fmt.Printf("autosave: 已存档 %s (保留 %d 天)\n", wd, autosaveKeepDays)
}
