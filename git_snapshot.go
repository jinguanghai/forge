package main

// git_snapshot.go — self gate / upgrade 的 git 快照 (20260923 从 upgrade.go 拆出)
//
// 背景: 原实现用 `git add -A` 全量暂存 —— 无 scope 边界, 会把未被 .gitignore 覆盖的
// 临时日志/探针产物/敏感文件一并固化进 git 历史 (实测 .forge/_audit_*.log 被 auto 快照带入,
// 且 .gitignore 里 20260914 已为同类问题打过一次补丁 —— 打补丁治标, scope 收窄治本)。
// 现改为「已跟踪文件的改动/删除 + 根目录白名单内的新增」, scope 在代码里显式声明。

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// snapshotWhitelist 允许新增入库的根目录文件名模式 (filepath.Match 通配)
var snapshotWhitelist = []string{
	"*.go", "go.mod", "go.sum", ".gitignore", ".env.example", "LICENSE", "README.md",
}

// snapshotAllowed 判断未跟踪文件是否在白名单内 (仅仓库根目录, 子目录一律不放行)
func snapshotAllowed(rel string) bool {
	if strings.ContainsAny(rel, `/\`) {
		return false
	}
	for _, pat := range snapshotWhitelist {
		if ok, _ := filepath.Match(pat, rel); ok {
			return true
		}
	}
	return false
}

// snapshotAdd 暂存待提交内容: 已跟踪文件的改动/删除 + 白名单内的新增文件。
// 禁止 git add -A (见文件头背景)。逐个 add 白名单文件, 避免 pathspec 未命中致整体失败。
func snapshotAdd(workDir string, env []string) error {
	run := func(args ...string) ([]byte, error) {
		c := exec.Command("git", args...)
		c.Dir = workDir
		c.Env = env
		return c.CombinedOutput()
	}
	// 1) 已跟踪文件的修改/删除 (不添加任何未跟踪文件)
	if out, err := run("add", "-u"); err != nil {
		return fmt.Errorf("git add -u: %v %s", err, strings.TrimSpace(string(out)))
	}
	// 2) 未跟踪文件里挑白名单
	out, err := run("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return fmt.Errorf("git ls-files --others: %v %s", err, strings.TrimSpace(string(out)))
	}
	var add []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && snapshotAllowed(line) {
			add = append(add, line)
		}
	}
	if len(add) > 0 {
		args := append([]string{"add", "--"}, add...)
		if out, err := run(args...); err != nil {
			return fmt.Errorf("git add 白名单: %v %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// gitSnapshot 自改前 git 快照: 工作区是 git 仓库时
// `git commit` 形成不可逆历史点, 返回 commit hash。
// 失败静默返回空串(不阻塞自改 —— .forge\checkpoints 文件快照仍是兜底)。
// 运行时文件已被 .gitignore 排除(memory.json/events.jsonl/checkpoint 等), 不入库。
func gitSnapshot(workDir, reason string) string {
	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		return "" // 非 git 仓库, 跳过
	}
	// auto 快照用固定身份 (不依赖用户全局 git 配置; 仅用于 auto commit)
	autoEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=forge-auto", "GIT_AUTHOR_EMAIL=auto@forge.local",
		"GIT_COMMITTER_NAME=forge-auto", "GIT_COMMITTER_EMAIL=auto@forge.local",
		"GIT_TERMINAL_PROMPT=0")
	ts := time.Now().Format("20060102_150405")
	msg := "auto: pre-selfmod " + ts + " " + sanitizeReason(reason)
	if err := snapshotAdd(workDir, autoEnv); err != nil {
		fmt.Fprintf(os.Stderr, "%s git 暂存失败(静默跳过): %v\n", color(ansi.yellow, "⚠"), err)
		return ""
	}
	// 暂存区相对 HEAD 无差异 → 不建空提交, 直接返回当前 HEAD (回滚点等价)。
	// 回归 20260925: 原实现无条件 `commit --allow-empty`, 实测 208 个 commit 中
	// 13 个是零变更空提交(全为 auto: pre-selfmod), 纯历史噪音。
	if hasDiff, derr := stagedChanges(workDir, autoEnv); derr == nil && !hasDiff {
		return headShort(workDir, autoEnv, "unchanged")
	} else if derr != nil {
		// git diff 异常(非 0/1 退出码): fail-open 走原路径, 不因新闸门丢快照
		fmt.Fprintf(os.Stderr, "%s git diff --cached 判定异常(fail-open 照常提交): %v\n", color(ansi.yellow, "⚠"), derr)
	}
	commit := exec.Command("git", "commit", "-m", msg)
	commit.Dir = workDir
	commit.Env = autoEnv
	if out, err := commit.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%s git commit 失败(静默跳过): %v %s\n", color(ansi.yellow, "⚠"), err, strings.TrimSpace(string(out)))
		return ""
	}
	return headShort(workDir, autoEnv, "committed")
}

// stagedChanges 判定暂存区相对 HEAD 是否有差异。
// git diff --cached --quiet 的既定语义: 退出码 0=无差异, 1=有差异; 其余为异常。
// 用退出码而非文本匹配判定 (确定性)。异常返回 error, 交调用方 fail-open。
func stagedChanges(workDir string, env []string) (bool, error) {
	c := exec.Command("git", "diff", "--cached", "--quiet")
	c.Dir = workDir
	c.Env = env
	err := c.Run()
	if err == nil {
		return false, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return true, nil
	}
	return true, err
}

// headShort 返回当前 HEAD 短 hash; 取不到时返回 fallback (不返回空串——
// 调用方以空串表示"快照失败")。
func headShort(workDir string, env []string, fallback string) string {
	rev := exec.Command("git", "rev-parse", "--short", "HEAD")
	rev.Dir = workDir
	rev.Env = env
	if out, err := rev.Output(); err == nil {
		if h := strings.TrimSpace(string(out)); h != "" {
			return h
		}
	}
	return fallback
}

// buildLdflags 组装注入构建事实 (buildCommit/buildTime) 的 -ldflags 串。
//
// 单一数据源: self gate (forge_self.go) 与 upgrade.go 共用 —— 两处各写一份必然漂移。
// 为什么需要 (20261002 实测): exe mtime 13:34:10 早于最新提交 13:38:14,
// 即"跑的是哪一版"当场不可判 —— 版本号只能说明"源码是哪版", 说明不了"exe 是哪版"。
// 现在 --version 直接报出提交短哈希与构建日期。
//
// git 不可用/非仓库时返回空串 (不注入, 程序内缺省 "dev") —— 构建绝不因 git 失败而中断。
// 工作区有未提交改动时后缀 "-dirty": 让"跑的是改过的源码"也当场可见。
func buildLdflags(workDir string) string {
	run := func(args ...string) ([]byte, error) {
		c := exec.Command("git", args...)
		c.Dir = workDir
		return c.CombinedOutput()
	}
	out, err := run("rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return ""
	}
	if st, serr := run("status", "--porcelain"); serr == nil && strings.TrimSpace(string(st)) != "" {
		commit += "-dirty"
	}
	return fmt.Sprintf("-X main.buildCommit=%s -X main.buildTime=%s",
		commit, time.Now().Format("2006-01-02"))
}
