package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// smokeRunner 冒烟执行器 (可注入, 便于测试不依赖真实 exe)。
type smokeRunner func(exePath string) (exitCode int, stdout string, runErr error)

// deployOutcome 部署结果, 供报告与测试断言。
type deployOutcome struct {
	Deployed   bool
	BackupPath string
	SHA256     string
	SmokeMS    int64
	FailedPath string // 冒烟失败时产物被改名到的路径
}

// smokeVerdict 冒烟判定 (纯函数):
// 判据 = 正常退出(exitCode==0 且 runErr==nil) 且 输出含 "铸剑炉"。
// 只看退出码不够: 一个「启动即报错但 exit 0」的坏二进制会被放过,
// 所以额外要求版本标识串出现。
func smokeVerdict(exitCode int, stdout string, runErr error) (bool, string) {
	if runErr != nil {
		return false, fmt.Sprintf("无法启动: %v", runErr)
	}
	if exitCode != 0 {
		return false, fmt.Sprintf("退出码 %d != 0", exitCode)
	}
	if !strings.Contains(stdout, "铸剑炉") {
		return false, fmt.Sprintf("输出无版本标识(前 60 字: %s)", headRunes(stdout, 60))
	}
	return true, "ok"
}

// headRunes 截取前 n 个 rune (避免按字节切断中文)。
func headRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}

// realSmokeRunner 真实冒烟: 跑 <exe> --version, 合并捕获 stdout/stderr。
func realSmokeRunner(exePath string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), selfSmokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exePath, "--version")
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	buf := newLimitedWriter(gateCaptureLimit())
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := runWithTimeout(ctx, cmd)
	if err == nil {
		return 0, buf.String(), nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), buf.String(), nil
	}
	return -1, buf.String(), err
}

// deploySelfExe 编译成功后的就位: 冒烟 → 备份 → 原子就位 → 失败回滚。
//
// 三个设计要点:
//  1. 冒烟跑「中性名副本」而非产物本身 —— 直接运行 forge_new.exe 会触发
//     runSelfReplace 把产物 rename 走, 反而绕过验证(旧设计无法自证能跑)。
//  2. 备份用 rename(原子, 不复制 11MB), 就位失败立刻 rename 回来 ——
//     任何时刻 forge.exe 要么是旧的要么是新的, 不存在缺失窗口。
//  3. 冒烟失败 → 产物改名 .failed_<ts>, 杜绝陈旧产物之后被误用;
//     源码不回滚(编译已通过, 问题在运行期, 留给下一轮修)。
func deploySelfExe(workDir, builtExe string, keepBackups int, smoke smokeRunner) (deployOutcome, error) {
	var out deployOutcome
	st, statErr := os.Stat(builtExe)
	if statErr != nil {
		return out, fmt.Errorf("产物不存在: %v", statErr)
	}
	if st.IsDir() {
		return out, fmt.Errorf("产物是目录: %s", builtExe)
	}
	if h, herr := sha256File(builtExe); herr == nil {
		out.SHA256 = h
	}
	ts := time.Now().Format("20060102_150405")

	smokePath := filepath.Join(workDir, "forge_smoke_"+ts+".exe")
	if cerr := copyFile(builtExe, smokePath); cerr != nil {
		return out, fmt.Errorf("冒烟副本复制失败: %v", cerr)
	}
	defer os.Remove(smokePath)

	t0 := time.Now()
	code, stdout, runErr := smoke(smokePath)
	out.SmokeMS = time.Since(t0).Milliseconds()
	if ok, why := smokeVerdict(code, stdout, runErr); !ok {
		failed := builtExe + ".failed_" + ts
		if rerr := os.Rename(builtExe, failed); rerr == nil {
			out.FailedPath = failed
		}
		return out, fmt.Errorf("冒烟未通过(%s), 未部署, 旧 exe 保持原样; 产物: %s", why, out.FailedPath)
	}

	cur := filepath.Join(workDir, "forge.exe")
	if filepath.Clean(cur) == filepath.Clean(builtExe) {
		out.Deployed = true
		return out, nil
	}
	if _, serr := os.Stat(cur); serr == nil {
		backup := cur + ".bak_" + ts
		if rerr := os.Rename(cur, backup); rerr != nil {
			return out, fmt.Errorf("备份 forge.exe 失败, 拒绝替换(旧 exe 保持原样): %v", rerr)
		}
		out.BackupPath = backup
	}
	if rerr := os.Rename(builtExe, cur); rerr != nil {
		rb := "无备份可回滚"
		if out.BackupPath != "" {
			if rbErr := os.Rename(out.BackupPath, cur); rbErr == nil {
				rb = "已回滚旧 exe"
				out.BackupPath = ""
			} else {
				rb = fmt.Sprintf("回滚失败(%v), 备份在 %s", rbErr, out.BackupPath)
			}
		}
		return out, fmt.Errorf("就位失败: %v; %s", rerr, rb)
	}
	if _, serr := os.Stat(cur); serr != nil {
		if out.BackupPath != "" {
			os.Rename(out.BackupPath, cur)
			out.BackupPath = ""
		}
		return out, fmt.Errorf("就位后校验失败: %v", serr)
	}
	out.Deployed = true
	if keepBackups > 0 {
		pruneExeBackups(workDir, keepBackups)
	}
	return out, nil
}

// selfBackupDir 返回 self gate 源码备份目录 (.forge/backups)。
// 产生点(selfBackupSource)与轮转点(pruneSelfBackups)共用本函数 —— 两处各写一份
// 路径拼接必然漂移, 漂移的后果是「备份落 A、轮转扫 B」: 备份无界增长而轮转空转,
// 且表面上轮转还在跑(无报错), 属静默失效。
func selfBackupDir(workDir string) string {
	return filepath.Join(workDir, ".forge", "backups")
}

func pruneSelfBackups(backupDir, srcPath string, keep int) {
	base := filepath.Base(srcPath)
	pattern := base + ".bak_self_*"
	matches, err := filepath.Glob(filepath.Join(backupDir, pattern))
	if err != nil {
		return
	}
	if len(matches) <= keep {
		return
	}
	// Sort by name (timestamp suffix sorts lexicographically) and remove oldest.
	// matches[0] is the oldest because timestamps are zero-padded (20060102_150405.000000000).
	sort.Strings(matches)
	for _, m := range matches[:len(matches)-keep] {
		os.Remove(m)
	}
}
