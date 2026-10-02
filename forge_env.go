package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// truncateMsg caps a diagnostic message to a reasonable length.
func truncateMsg(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// summarizeOutput generates a brief summary of execution output.
// Long outputs get head+tail truncation with line count.
//
// 改进(20260930 P1-1/2): 原版固定取前 3 行, 恰巧碰上 Go test 样板
// (=== RUN / === PAUSE / === CONT), 头部全是噪音, 关键证据被吞。
// 改为: 过滤样板/空白行后取前 5 行作为 head; stderr 为空时不再输出 "[stderr]" 标签。
//
// 样板行集合 = Go test 标准流式前缀 (=== RUN / === PAUSE / === CONT /
//
//	=== NAME / === FAIL / === PASS / === SKIP / ok / FAIL )
//
//	+ Python pytest 同类 (PASSED/FAILED/SKIPPED/::test_)
//	+ 通用空行 + 仅含 ANSI reset 的"无信息行"。
func summarizeOutput(stdout, stderr string) string {
	var parts []string
	if stdout != "" {
		trimmed := strings.TrimSpace(stdout)
		lines := strings.Split(trimmed, "\n")
		n := len(lines)
		if n <= 8 {
			parts = append(parts, trimmed)
		} else {
			// 过滤样板行, 但保留原总数 n 用于统计
			contentLines := filterBoilerplateLines(lines)
			m := len(contentLines)
			if m <= 8 {
				// 过滤后已足够短, 全量展示
				parts = append(parts, strings.Join(contentLines, "\n"))
			} else {
				head := strings.Join(contentLines[:5], "\n")
				tail := strings.Join(contentLines[m-3:], "\n")
				parts = append(parts, fmt.Sprintf("%s\n... (%d 行, 过滤样板后 %d 行) ...\n%s",
					head, n, m, tail))
			}
		}
	}
	if stderr != "" {
		stderrTrimmed := strings.TrimSpace(stderr)
		if len(stderrTrimmed) > 200 {
			stderrTrimmed = stderrTrimmed[:200] + "..."
		}
		// 仅在有内容时附 stderr 段; 空 stderr 不打标签 (P1-2)
		parts = append(parts, stderrTrimmed)
	}
	return strings.Join(parts, " | ")
}

// filterBoilerplateLines 去除典型测试框架样板行 (Go test / pytest 流式前缀)。
// 空行与仅含 ANSI 的"无信息行"也一并剔除, 保证 head/tail 都是有效信息。
func filterBoilerplateLines(lines []string) []string {
	boilerplatePrefixes := []string{
		"=== RUN", "=== PAUSE", "=== CONT", "=== NAME",
		"=== FAIL", "=== PASS", "=== SKIP",
		"ok	", "FAIL	", "PASS	", "SKIP	",
		"PASSED ", "FAILED ", "SKIPPED ", "_test.",
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		// 仅含 ANSI reset (如 [0m) 也算空信息
		if isANSINoise(t) {
			continue
		}
		skip := false
		for _, p := range boilerplatePrefixes {
			if strings.HasPrefix(t, p) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, l)
		}
	}
	return out
}

// isANSINoise 判断 trim 后的字符串是否只剩 ANSI 控制序列 (无可见内容)。
// 例: "\x1b[0m\x1b[0m" → true。
func isANSINoise(s string) bool {
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := strings.IndexByte(s[i:], 'm')
			if j < 0 {
				return false // 残缺序列不视作"无信息"
			}
			i += j + 1
			continue
		}
		return false
	}
	return true
}

// forgeCleanupStaleTempDirs removes leftover forge_gate_* and forge_go_* temp directories
// from the system temp directory. These accumulate when forge processes are killed abruptly.
func forgeCleanupStaleTempDirs() {
	tmpDir := os.TempDir()
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return
	}
	now := time.Now()
	minAge := 5 * time.Minute
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "forge_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Skip recently created directories to avoid cleaning up active forge instances
		if now.Sub(info.ModTime()) < minAge {
			continue
		}
		path := filepath.Join(tmpDir, name)
		os.RemoveAll(path)
	}
}

// cleanupWorkTempDir 清空工作区的一次性临时目录 .forge-temp/ (5S 的出口约定)。
//
// 动机: .forge-temp 是白纸黑字的一次性区(纪律: 禁放任何资产), 但此前无任何机制清空它 ——
// cleanup_backups 只扫根目录普通文件、卫生哨兵只判根目录白名单, 于是它只进不出。
// 实测(20260928): 该目录长期躺着上一轮的 arch_scan.json / browser_test.png, 而四套守卫全绿。
//
// 位置纪律(实测约束, 勿挪): 只能在启动序调用, 严禁放进 NewForge —— NewForge 有 17 个
// 测试调用点, 其中多个以 D:\forge 为 workDir, 放进去会让测试互踩(browser_gate_test.go
// 的截图正是写到 .forge-temp), 且每跑一次单测就清空生产临时目录。
//
// 行为三条:
//  1. 清空内容但保留目录本身(不存在则建出), 使「目录恒存在」成为不变量;
//  2. 占用中的条目跳过并留痕, 不阻塞启动(同类问题见 hygiene_redcard.py 的 is_locked);
//  3. 体量超熔断阈值 = 资产被错放的信号, 此时只留痕不删除(红牌精神: 宁可漏清不可误删)。
func cleanupWorkTempDir(workDir string) {
	dir := filepath.Join(workDir, ForgeTempDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		_ = os.MkdirAll(dir, 0755)
		return
	}
	total := dirTreeSize(dir)
	if len(entries) > tempDirMaxEntries || total > tempDirMaxBytes {
		logEvent("hygiene", fmt.Sprintf(".forge-temp 体量异常(%d 项/%.1f MB), 已跳过清理",
			len(entries), float64(total)/(1<<20)),
			map[string]interface{}{"dir": dir, "entries": len(entries), "bytes": total})
		return
	}
	removed, locked, freed := 0, 0, int64(0)
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		size := dirTreeSize(p)
		if rerr := os.RemoveAll(p); rerr != nil {
			// 只读文件在 Windows 上删不掉: 清属性后重试一次
			_ = os.Chmod(p, 0666)
			if rerr = os.RemoveAll(p); rerr != nil {
				locked++ // 占用中(如正在运行的 verify.exe): 跳过, 不阻塞启动
				continue
			}
		}
		removed++
		freed += size
	}
	if removed > 0 || locked > 0 {
		// 只报实际释放量: 把 locked 未删的字节算进「已清空」会让证据失真
		// (实测 20260928: 报告 11605 KB 里含 11.3MB 运行中的 verify.exe)
		logEvent("hygiene", fmt.Sprintf(".forge-temp 已清空: %d 项 / %.1f KB",
			removed, float64(freed)/1024),
			map[string]interface{}{"dir": dir, "removed": removed, "bytes": freed, "locked": locked})
	}
}

// dirTreeSize 递归累计目录树内普通文件的字节数(目录项本身不计)。
func dirTreeSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		n += info.Size()
		return nil
	})
	return n
}

// 熔断阈值: 正常临时产物是 KB~MB 级, 越过两个数量级即判为「资产被错放」。
// 声明为 var 而非 const, 唯一目的是让测试能注入小阈值验证熔断分支。
var (
	tempDirMaxEntries = 2000
	tempDirMaxBytes   = int64(200 << 20)
)

// newCmd creates a subprocess command with UTF-8 output forced for all
// child processes. Windows Python otherwise inherits the ANSI code page
// (GBK on zh-CN systems) and emits GBK bytes that the UTF-8 console
// renders as mojibake. Setting these env vars makes every gate's output
// consistently UTF-8.
func (f *Forge) newCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	// 故意不用 exec.CommandContext：Go 在 ctx 到期时会起内部 watcher 抢先 Kill
	// 直接子进程，等 runWithTimeout 再执行 taskkill /T 时父进程已死、枚举不到
	// 孙进程 → 孙进程成孤儿（2026-09-23 受控实验：CommandContext 下即便把
	// taskkill 提到 Kill 之前仍是 3/3 漏网；换 exec.Command 后 0/3）。
	// 超时杀树职责因此完全交给 runWithTimeout（它已在 ctx.Done 分支调用
	// killProcessTree）；ctx 参数保留以维持所有调用点签名不变。
	_ = ctx
	cmd := exec.Command(name, args...)
	// 工作目录绑定: 自托管 gate (relation/tcm 等) 靠相对路径读源码与数据文件。
	// 若子进程继承"启动目录", 从别处启动 forge.exe 就会扫错根、找不到数据
	// (20260925 实测: 从 C:\Users\jin 启动 -> relation 报"生产符号 0"、tcm 报"方剂库未找到";
	//  而全量测试全绿, 因测试里 WorkDir 恰好 == 进程 cwd, 错位场景构造不出来)。
	// 全部 gate 子进程都经 newCmd 这一个入口创建, 故在此单点绑定。
	if f.workDir != "" {
		cmd.Dir = f.workDir
	}
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"PYTHONUTF8=1",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	)
	// 脚本型 gate 的兜底根目录/数据目录: 仅在用户未显式设置时注入, 不覆盖自定义值。
	if f.workDir != "" {
		if os.Getenv("FORGE_WORKDIR") == "" {
			cmd.Env = append(cmd.Env, "FORGE_WORKDIR="+f.workDir)
		}
		if os.Getenv("FORGE_DATA") == "" {
			cmd.Env = append(cmd.Env, "FORGE_DATA="+f.workDir)
		}
	}
	return cmd
}

// findGoCommand 查找 Go 编译器路径。
// 优先级：1) 系统 PATH  2) .forge/go/bin/go  3) 自动下载
func (f *Forge) findGoCommand() (string, error) {
	if goPath, err := exec.LookPath("go"); err == nil {
		return goPath, nil
	}
	localGo := filepath.Join(f.workDir, ".forge", "go", "bin", "go"+exeSuffix())
	if _, err := os.Stat(localGo); err == nil {
		return localGo, nil
	}
	if err := f.downloadGo(); err != nil {
		return "", fmt.Errorf("go toolchain not found and auto-download failed: %w\nInstall Go manually: https://go.dev/dl/", err)
	}
	if _, err := os.Stat(localGo); err == nil {
		return localGo, nil
	}
	return "", errors.New("go toolchain not found. Install Go: https://go.dev/dl/")
}

// downloadGo 从 Go 官网自动下载工具链到 .forge/go/
func (f *Forge) downloadGo() error {
	goVersion := runtime.Version()
	goOS := runtime.GOOS
	goArch := runtime.GOARCH

	// 映射 Go 下载包中的架构名
	archMap := map[string]string{"arm": "armv6l"}
	if mapped, ok := archMap[goArch]; ok {
		goArch = mapped
	}
	ext := "tar.gz"
	if goOS == "windows" {
		ext = "zip"
	}
	filename := fmt.Sprintf("%s.%s-%s.%s", goVersion, goOS, goArch, ext)
	url := "https://go.dev/dl/" + filename
	tmpFile := filepath.Join(os.TempDir(), filename)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	fh, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer fh.Close()
	defer os.Remove(tmpFile)
	if _, err := io.Copy(fh, resp.Body); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	fh.Close()
	goDir := filepath.Join(f.workDir, ".forge")
	os.RemoveAll(filepath.Join(goDir, "go"))
	if goOS == "windows" {
		if err := forgeUnzip(tmpFile, goDir); err != nil {
			return fmt.Errorf("unzip Go: %w", err)
		}
	} else {
		if err := forgeUntarGz(tmpFile, goDir); err != nil {
			return fmt.Errorf("untar Go: %w", err)
		}
	}
	return nil
}
