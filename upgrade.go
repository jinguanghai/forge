// upgrade.go: 自升级: 拉取新版本 / 校验 / 原子替换 / 快照回滚

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SelfUpgrade 一键升级 (排空式重启 + 审计 + 快照):
//  1. 自动自检: go vet → go test → forge_guard check → go build (forge_new.exe)
//  2. 全部通过后: ①自改前统一快照(.forge\checkpoints) → ②写审计(.forge\updates)
//     → ③生成排空式重启脚本并启动
//
// 重启脚本不再强杀主进程: 等主进程自行退出(主循环已置 exitRequested, defer 保存缓存),
// 最多等30秒, 超时才强杀。新进程启动时读取审计并向主人报告上次自改。
// 任一步失败立即返回错误, 不碰任何文件/进程。
func SelfUpgrade(cfg *Config) error {
	workDir := cfg.WorkDir
	ts := time.Now().Format("20060102_150405")

	fmt.Println()
	fmt.Println(bold("═══ 自动升级 ═══"))
	fmt.Println(dim("  自检(logic_guard → vet → test → 防御基线) → 编译 → 快照 → 审计 → 排空重启"))
	fmt.Println()

	// 逻辑闭环门控 (I-3 永续防线): .forge/tools/logic_guard 存在则先跑, P0 破绽即拦截编译.
	// 工具缺失时静默降级 (不阻塞主流程), 防新环境未初始化导致升级卡死.
	lgPath := filepath.Join(workDir, ".forge", "tools", "logic_guard", "logic_guard"+exeSuffix())
	steps := []struct {
		name string
		cmd  *exec.Cmd
	}{}
	if _, err := os.Stat(lgPath); err == nil {
		steps = append(steps, struct {
			name string
			cmd  *exec.Cmd
		}{"logic_guard (逻辑闭环门控)", exec.Command(lgPath, workDir)})
	}
	steps = append(steps,
		struct {
			name string
			cmd  *exec.Cmd
		}{"go vet (静态检查)", exec.Command("go", "vet", ".")},
		struct {
			name string
			cmd  *exec.Cmd
		}{"go test (单元测试)", exec.Command("go", "test", ".")},
		struct {
			name string
			cmd  *exec.Cmd
		}{"forge_guard check (防御基线)", exec.Command("python", filepath.Join(workDir, "defense_system", "forge_guard.py"), "check")},
	)
	for i, st := range steps {
		fmt.Printf("  [%d/%d] %s ... ", i+1, len(steps)+2, st.name)
		st.cmd.Dir = workDir
		st.cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
		out, err := st.cmd.CombinedOutput()
		if err != nil {
			fmt.Println(color(ansi.red, "FAIL"))
			for _, l := range upgradeTailLines(string(out), 6) {
				fmt.Println(dim("    " + l))
			}
			return fmt.Errorf("自检未通过: %s", st.name)
		}
		fmt.Println(color(ansi.green, "OK"))
	}

	// 编译新版本 (最后一步: 刚生成的 forge_new.exe 不经过防御基线比对)
	fmt.Printf("  [%d/%d] go build → forge_new.exe ... ", len(steps)+2, len(steps)+2)
	build := exec.Command("go", "build", "-o", "forge_new.exe", ".")
	build.Dir = workDir
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Println(color(ansi.red, "FAIL"))
		for _, l := range upgradeTailLines(string(out), 6) {
			fmt.Println(dim("    " + l))
		}
		return fmt.Errorf("编译失败")
	}
	newExe := filepath.Join(workDir, "forge_new.exe")
	if _, err := os.Stat(newExe); err != nil {
		fmt.Println(color(ansi.red, "FAIL"))
		return fmt.Errorf("forge_new.exe 未生成")
	}
	// 升级产物哈希留痕(证据先于声称): 没有哈希, 「新 exe 已就位」只是一句话。
	newHash, herr := sha256File(newExe)
	if herr != nil {
		fmt.Println(color(ansi.red, "FAIL"))
		return fmt.Errorf("新 exe 哈希计算失败: %v", herr)
	}
	logEvent(EvSelfModified, "upgrade-new-exe-hash", map[string]string{"sha256": newHash})
	fmt.Println(color(ansi.green, "OK"))
	fmt.Printf("  %s\n", dim("forge_new.exe sha256="+newHash[:16]))

	// ① 自改前统一快照 (I-3): 顶层源码 + memory + gate 源码 + 当前二进制
	fmt.Printf("  [%d/%d] 自改前快照 (.forge\\checkpoints) ... ", len(steps)+3, len(steps)+2)
	if err := createCheckpoint(workDir, ts, "upgrade"); err != nil {
		fmt.Println(color(ansi.red, "FAIL"))
		return fmt.Errorf("创建快照失败: %v", err)
	}
	fmt.Println(color(ansi.green, "OK"))

	// ③ git 快照: .git 存在时 add+commit, 失败静默(文件快照仍是兜底)
	if h := gitSnapshot(workDir, "upgrade"); h != "" {
		fmt.Printf("  git 快照: %s\n", dim("commit "+h))
		logEvent(EvSelfModified, "upgrade-git-snapshot", map[string]string{"git": h})
	}

	// ② 审计: 记录本次自改 (I-1)
	fmt.Printf("  [%d/%d] 写审计记录 (.forge\\updates) ... ", len(steps)+4, len(steps)+2)
	if err := writeUpgradeAudit(workDir, ts, "upgrade 自我修改安全三件套"); err != nil {
		fmt.Println(color(ansi.red, "FAIL"))
		return fmt.Errorf("写审计失败: %v", err)
	}
	fmt.Println(color(ansi.green, "OK"))

	// 备份轮转: forge.exe.bak_* 最多留 10 个
	pruneExeBackups(workDir, backupKeepCount)

	// 写重启脚本 (排空式: 等主进程自行退出, 超时才强杀)
	script := buildRestartScript(workDir, os.Getpid(), ts)
	scriptPath := filepath.Join(workDir, "restart_self.ps1")
	// 带 BOM 写: PowerShell 5.1 对无 BOM 的 UTF-8 按 ANSI 解释 → 中文日志乱码。
	if err := os.WriteFile(scriptPath, append([]byte("\ufeff"), script...), 0644); err != nil {
		return fmt.Errorf("写重启脚本失败: %v", err)
	}

	// 启动脚本 (隐藏窗口, 主程序退出后继续执行)
	cmd := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.Dir = workDir
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动重启脚本失败: %v", err)
	}
	return nil
}

// buildRestartScript 生成排空式重启脚本内容 (纯函数, 可测试):
//
//	主进程已置 exitRequested 应自行退出(走 defer 保存缓存/记忆)
//	→ 脚本轮询等待(最多30秒, 进程一退就走) → 超时才强杀
//	→ 备份旧exe(失败即拒绝替换) → 替换新exe → 冒烟(--version, 失败自动回滚)
//	→ 防御基线重建 → 打开新窗口
//
// 脚本正文保持纯 ASCII: PowerShell 5.1 读无 BOM 的 UTF-8 会按 ANSI 解释,
// 中文日志必然乱码(旧版升级日志乱码的根因)。文件写入侧另加 UTF-8 BOM。
func buildRestartScript(workDir string, pid int, ts string) string {
	// 不再用 SilentlyContinue: 升级失败曾表现为「程序退出且没回来, 用户看不到原因」。
	// 现在每步写 .forge/upgrade_restart.log, 致命失败 exit 1 且留痕。
	return fmt.Sprintf(`# auto-generated by /upgrade - do not edit
$ErrorActionPreference = 'Continue'
$wd = '%s'
$log = Join-Path $wd '.forge\upgrade_restart.log'
function Log($m) { "$((Get-Date).ToString('s')) $m" | Out-File -Append -Encoding utf8 $log }
Log "=== restart begin (pid %d) ==="
Set-Location $wd
# 排空等待: 主进程收到 exitRequested 后自行退出(走 defer 保存缓存), 最多等30秒
$deadline = (Get-Date).AddSeconds(30)
while ((Get-Process -Id %d -ErrorAction SilentlyContinue) -and ((Get-Date) -lt $deadline)) {
    Start-Sleep -Milliseconds 500
}
# 超时仍未退出才强杀(兜底)
if (Get-Process -Id %d -ErrorAction SilentlyContinue) {
    Log "排空超时, 强杀 pid %d"
    Stop-Process -Id %d -Force
    Start-Sleep -Milliseconds 500
}
# backup BEFORE replace: no backup = no rollback, so refuse to touch forge.exe
Copy-Item forge.exe forge.exe.bak_%s -Force
if (-not $?) { Log "FATAL: backup forge.exe failed, refuse to replace (old exe intact)"; exit 1 }
Move-Item forge_new.exe forge.exe -Force
if (-not $?) { Log "FATAL: deploy forge_new.exe failed, forge.exe unchanged"; exit 1 }
# smoke test: compiled != runnable. roll back if the new exe cannot report its version
$smoke = (& .\forge.exe --version 2>&1 | Out-String)
$smokeCode = $LASTEXITCODE
if ($smokeCode -ne 0 -or $smoke.Trim().Length -eq 0) {
    Log "FATAL: smoke test failed (exit=$smokeCode), rolling back"
    Move-Item forge.exe forge_new.exe.failed_%s -Force
    Move-Item forge.exe.bak_%s forge.exe -Force
    if (-not $?) { Log "FATAL: rollback failed, backup kept at forge.exe.bak_%s"; exit 1 }
    Log "rolled back to previous forge.exe"
    Start-Process -FilePath 'forge.exe' -WorkingDirectory $wd
    exit 1
}
Log "smoke test ok: $($smoke.Trim())"
python defense_system\forge_guard.py init *>> $log
Start-Process -FilePath 'forge.exe' -WorkingDirectory $wd
if (-not $?) { Log "FATAL: start forge.exe failed"; exit 1 }
Log "=== restart ok ==="
`, workDir, pid, pid, pid, pid, pid, ts, ts, ts, ts)
}

// upgradeTailLines 返回输出最后 n 行(去空白行)
func upgradeTailLines(s string, n int) []string {
	all := strings.Split(strings.TrimSpace(s), "\n")
	out := []string{}
	for _, l := range all {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// backupKeepCount 备份/快照轮转保留份数。
// IFR-2 理想解: 机制本已存在, 病根是一个数字 10 散落在 4 处 —— 收归一处并收窄。
// 依据: 回滚只需 1 份, 保留 3 份即双保险; git 已提供完整历史, 快照仅兜底"未提交的工作区状态"。
// 旧值 10 的成本: checkpoints 10x12.15MB=121MB + exe 10x11.4MB=114MB + bak_self 10x92KB。
const backupKeepCount = 3

// pruneExeBackups 轮转 forge.exe.bak_* 备份, 最多保留 n 个
func pruneExeBackups(dir string, n int) {
	matches, globErr := filepath.Glob(filepath.Join(dir, "forge.exe.bak_*"))
	if globErr != nil {
		return // Glob 失败: 无法枚举备份, 不动
	}
	if len(matches) <= n {
		return
	}
	sort.Strings(matches) // 时间戳命名 → 字典序即时间序
	for _, m := range matches[:len(matches)-n] {
		os.Remove(m)
	}
}

// ────────────────────────────────────────────────────────────────
// I-3 快照: 自改前统一备份 (.forge\checkpoints\{ts}_{reason}\)
// ────────────────────────────────────────────────────────────────

// gitSnapshot 自改前 git 快照: 工作区是 git 仓库时
// `git add -A` + `git commit` 形成不可逆历史点, 返回 commit hash。
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
	add := exec.Command("git", "add", "-A")
	add.Dir = workDir
	add.Env = autoEnv
	if out, err := add.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%s git add 失败(静默跳过): %v %s\n", color(ansi.yellow, "⚠"), err, strings.TrimSpace(string(out)))
		return ""
	}
	commit := exec.Command("git", "commit", "-m", msg, "--allow-empty")
	commit.Dir = workDir
	commit.Env = autoEnv
	if out, err := commit.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "%s git commit 失败(静默跳过): %v %s\n", color(ansi.yellow, "⚠"), err, strings.TrimSpace(string(out)))
		return ""
	}
	rev := exec.Command("git", "rev-parse", "--short", "HEAD")
	rev.Dir = workDir
	rev.Env = autoEnv
	if out, err := rev.Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return "committed"
}

// createCheckpoint 拷贝顶层 *.go + memory.json + gate 源码 + 当前二进制
// 到 .forge\checkpoints\{ts}_{reason}\, 轮转最多留 n 个目录。
func createCheckpoint(workDir, ts, reason string) error {
	dir := filepath.Join(workDir, ".forge", "checkpoints")
	safe := sanitizeReason(reason)
	dst := filepath.Join(dir, ts+"_"+safe)
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	copied := 0

	// 顶层 *.go 源码 (含测试)
	gos, _ := filepath.Glob(filepath.Join(workDir, "*.go"))
	for _, f := range gos {
		if copyFile(f, filepath.Join(dst, filepath.Base(f))) == nil {
			copied++
		}
	}
	// memory.json
	mem := memoryFilePath(workDir)
	if _, err := os.Stat(mem); err == nil {
		if copyFile(mem, filepath.Join(dst, "memory.json")) == nil {
			copied++
		}
	}
	// .forge\forge-tools\*.go (gate 源码)
	toolsDir := filepath.Join(workDir, ".forge", "forge-tools")
	tgos, _ := filepath.Glob(filepath.Join(toolsDir, "*.go"))
	for _, f := range tgos {
		if copyFile(f, filepath.Join(dst, "gate_"+filepath.Base(f))) == nil {
			copied++
		}
	}
	// 当前二进制 (回滚用)
	exe := filepath.Join(workDir, "forge.exe")
	if _, err := os.Stat(exe); err == nil {
		if copyFile(exe, filepath.Join(dst, "forge.exe")) == nil {
			copied++
		}
	}
	if copied == 0 {
		os.RemoveAll(dst)
		return fmt.Errorf("快照未拷贝任何文件")
	}
	pruneCheckpoints(workDir, backupKeepCount)
	return nil
}

// pruneCheckpoints 轮转快照目录, 最多保留 n 个 (按修改时间删最旧)
func pruneCheckpoints(workDir string, n int) {
	dir := filepath.Join(workDir, ".forge", "checkpoints")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type ce struct {
		name string
		mod  time.Time
	}
	var list []ce
	for _, e := range entries {
		if e.IsDir() {
			if info, err := e.Info(); err == nil {
				list = append(list, ce{e.Name(), info.ModTime()})
			}
		}
	}
	if len(list) <= n {
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.Before(list[j].mod) })
	for _, c := range list[:len(list)-n] {
		os.RemoveAll(filepath.Join(dir, c.name))
	}
}

// copyFile 拷贝单个文件
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// sanitizeReason 清洗 reason 为安全文件名 (去路径/去非法字符)
func sanitizeReason(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "x"
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out
}

// ────────────────────────────────────────────────────────────────
// I-1 审计: .forge\updates\{ts}.json 记录每次自改
// ────────────────────────────────────────────────────────────────

// upgradeAudit 一次自改的审计记录 (status: ready=待生效, done=已消费)
type upgradeAudit struct {
	ID     string `json:"id"`
	TS     string `json:"ts"`
	Reason string `json:"reason"`
	Status string `json:"status"`
	Source string `json:"source"`
}

func updatesDir(workDir string) string {
	return filepath.Join(workDir, ".forge", "updates")
}

// writeUpgradeAudit 写审计记录 (status=ready, 待重启生效)
func writeUpgradeAudit(workDir, ts, reason string) error {
	dir := updatesDir(workDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	a := upgradeAudit{ID: ts, TS: time.Now().Format(time.RFC3339), Reason: reason, Status: "ready", Source: "upgrade"}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ts+".json"), b, 0644)
}

// latestAudit 返回最新一条审计记录及其路径
func latestAudit(workDir string) (*upgradeAudit, string) {
	dir := updatesDir(workDir)
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(matches) == 0 {
		return nil, ""
	}
	sort.Strings(matches)
	path := matches[len(matches)-1]
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	var a upgradeAudit
	if json.Unmarshal(b, &a) != nil {
		return nil, ""
	}
	return &a, path
}

// markAuditDone 把审计状态标记为 done (tmp+rename 原子替换)
func markAuditDone(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var a upgradeAudit
	if json.Unmarshal(b, &a) != nil {
		return
	}
	a.Status = "done"
	if nb, err := json.MarshalIndent(a, "", "  "); err == nil {
		atomicWrite(path, nb)
	}
}

// reportLastUpgrade 启动时调用: 检测未消费的 ready 审计 → 打印报告 → 标记 done → 记事件
func reportLastUpgrade(workDir string) {
	a, path := latestAudit(workDir)
	if a == nil || a.Status != "ready" {
		return
	}
	fmt.Println()
	fmt.Println(bold("♻ 上次自改报告 (排空式重启完成):"))
	fmt.Printf("  %s %s\n", bold("时间:"), a.TS)
	if a.Reason != "" {
		fmt.Printf("  %s %s\n", bold("原因:"), a.Reason)
	}
	fmt.Println(dim("  详情见 .forge\\updates\\ 审计记录, 源码快照见 .forge\\checkpoints\\"))
	fmt.Println()
	markAuditDone(path)
	logEvent(EvSelfRestart, a.Reason, nil)
}

// sha256File 计算文件 SHA256 (用于升级前后比对)
func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}
