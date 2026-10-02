package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (f *Forge) forgeGateInline(code, lang string, compiler CompilerDef, input string, start time.Time) (gateRes ForgeGateResult) {
	timeout := compiler.ExecTimeout
	if timeout == 0 {
		timeout = defaultGateTimeout
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	execArgs := make([]string, len(compiler.Exec))
	copy(execArgs, compiler.Exec)
	hasCodePlaceholder := false
	needsStdin := false
	for i, a := range execArgs {
		if a == "{code}" {
			execArgs[i] = code
			hasCodePlaceholder = true
		}
		if strings.Contains(a, "{forge}") {
			execArgs[i] = strings.ReplaceAll(a, "{forge}", f.workDir)
			if runtime.GOOS != "windows" {
				execArgs[i] = strings.ReplaceAll(execArgs[i], ".exe", "")
			}
		}
		if a == "-" {
			needsStdin = true
		}
	}
	if !hasCodePlaceholder && !needsStdin {
		execArgs = append(execArgs, code)
	}
	if len(execArgs) > 0 {
		exe := execArgs[0]
		args := execArgs[1:]
		cmd = f.newCmd(ctx, exe, args...)
	} else {
		return ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error:    "no exec command configured",
			Duration: time.Since(start).Milliseconds(),
		}
	}
	cmd.Dir = f.workDir
	if input != "" {
		cmd.Env = append(cmd.Env, ForgeInputEnv+"="+input)
		cmd.Stdin = strings.NewReader(input)
	}

	stdout := newLimitedWriter(gateCaptureLimit())
	stderr := newLimitedWriter(gateCaptureLimit())
	defer markCaptureLimit(&gateRes, stdout, stderr)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if needsStdin {
		cmd.Stdin = strings.NewReader(code)
	} else if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	err := runWithTimeout(ctx, cmd)
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = stdout.String()
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		timedOut := isTimeoutErr(err)
		diag := ""
		if timedOut {
			diag = timeoutDiag(lang, timeout, stdout.String(), stderr.String())
		}
		return ForgeGateResult{
			OK:          false,
			Lang:        lang,
			Stage:       "execute",
			Error:       fmt.Sprintf("%s execution failed: %s", lang, errMsg),
			Stderr:      errMsg,
			Diagnostics: diag,
			Timeout:     timedOut,
			ExitCode:    safeExitCode(cmd),
			Duration:    time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK:       true,
		Lang:     lang,
		Stage:    "done",
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(start).Milliseconds(),
	}
}

func (f *Forge) forgeGateFile(code, lang string, compiler CompilerDef, input string, start time.Time) (gateRes ForgeGateResult) {
	ext := compiler.Ext
	if ext == "" {
		ext = ".code"
	}
	tmpDir, err := os.MkdirTemp("", "forge_gate_")
	if err != nil {
		return ForgeGateResult{OK: false, Lang: lang, Stage: "write",
			Error: fmt.Sprintf("failed to create temp dir: %v", err), Duration: time.Since(start).Milliseconds()}
	}
	defer os.RemoveAll(tmpDir)
	srcPath := filepath.Join(tmpDir, "code"+ext)

	// Ensure UTF-8 BOM-free
	if writeErr := os.WriteFile(srcPath, []byte(code), 0644); writeErr != nil {
		return ForgeGateResult{OK: false, Lang: lang, Stage: "write",
			Error: fmt.Sprintf("failed to write source: %v", writeErr), Duration: time.Since(start).Milliseconds()}
	}

	// Check/lint phase
	if len(compiler.Check) > 0 {
		checkStart := time.Now()
		checkArgs, hasFilePlaceholder := expandArgs(compiler.Check, srcPath, f.workDir, tmpDir)
		if !hasFilePlaceholder {
			checkArgs = append(checkArgs, srcPath)
		}
		checkCtx, checkCancel := context.WithTimeout(f.effCtx(), compiler.CompileTimeout)
		defer checkCancel()
		checkCmd := f.newCmd(checkCtx, checkArgs[0], checkArgs[1:]...)
		checkCmd.Dir = f.workDir
		var checkStderr strings.Builder
		checkCmd.Stderr = &checkStderr
		if checkErr := runWithTimeout(checkCtx, checkCmd); checkErr != nil {
			return ForgeGateResult{OK: false, Lang: lang, Stage: "compile",
				Error:  fmt.Sprintf("%s syntax check failed (%v): %s", lang, checkErr, checkStderr.String()),
				Stderr: checkStderr.String(), Timeout: isTimeoutErr(checkErr), Duration: time.Since(start).Milliseconds()}
		}
		slog.Debug("forge check", "lang", lang, "duration_ms", time.Since(checkStart).Milliseconds())
	}

	// Lint phase
	var lintOutput string
	if len(compiler.Lint) > 0 {
		lintStart := time.Now()
		lintArgs, hasFilePlaceholder := expandArgs(compiler.Lint, srcPath, f.workDir, tmpDir)
		if !hasFilePlaceholder {
			lintArgs = append(lintArgs, srcPath)
		}
		lintCtx, lintCancel := context.WithTimeout(f.effCtx(), compiler.CompileTimeout)
		defer lintCancel()
		lintCmd := f.newCmd(lintCtx, lintArgs[0], lintArgs[1:]...)
		lintCmd.Dir = f.workDir
		var lintStdout, lintStderr strings.Builder
		lintCmd.Stdout = &lintStdout
		lintCmd.Stderr = &lintStderr
		if lintErr := runWithTimeout(lintCtx, lintCmd); lintErr != nil {
			slog.Warn("forge lint warning", "lang", lang, "stderr", lintStderr.String())
		}
		lintOutput = lintStdout.String()
		if lintStderr.Len() > 0 {
			if lintOutput != "" {
				lintOutput += "\n"
			}
			lintOutput += lintStderr.String()
		}
		slog.Debug("forge lint", "lang", lang, "duration_ms", time.Since(lintStart).Milliseconds())
	}

	// Execute phase
	execArgs, hasFilePlaceholder := expandArgs(compiler.Exec, srcPath, f.workDir, tmpDir)
	if !hasFilePlaceholder {
		if len(execArgs) == 0 {
			return ForgeGateResult{OK: false, Lang: lang, Stage: "execute",
				Error: fmt.Sprintf("%s: no exec command configured", lang), Duration: time.Since(start).Milliseconds()}
		}
		execArgs = append(execArgs, srcPath)
	}
	execCtx, execCancel := context.WithTimeout(f.effCtx(), compiler.ExecTimeout)
	defer execCancel()
	cmd := f.newCmd(execCtx, execArgs[0], execArgs[1:]...)
	cmd.Dir = f.workDir
	if input != "" {
		cmd.Env = append(cmd.Env, ForgeInputEnv+"="+input)
		cmd.Stdin = strings.NewReader(input)
	}
	stdout := newLimitedWriter(gateCaptureLimit())
	stderr := newLimitedWriter(gateCaptureLimit())
	defer markCaptureLimit(&gateRes, stdout, stderr)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	execErr := runWithTimeout(execCtx, cmd)
	if execErr != nil {
		stdOut := stdout.String()
		stdErr := stderr.String()
		// 超时 = 预算烧完(代码没跑完), 不是「结果」: 旧实现把「超时+有部分输出」
		// 判成 OK=true —— 加 -u 让部分输出可见后, 这个口子会把超时上报成成功,
		// 模型据此以为任务已跑完 (P0 20260930 修正)。超时一律 OK=false,
		// 部分输出照常回灌(仅作定位), 诊断单独走 Diagnostics(不被「Stderr 非空
		// 则丢弃 Error」的渲染规则吞掉, 见 formatResult)。
		if timedOut := isTimeoutErr(execErr); timedOut {
			return ForgeGateResult{OK: false, Lang: lang, Stage: "execute",
				Error:       fmt.Sprintf("%s execution failed: %s", lang, execErr.Error()),
				Diagnostics: timeoutDiag(lang, compiler.ExecTimeout, stdOut, stdErr),
				Timeout:     true,
				Stdout:      stdOut, Stderr: stdErr, Lint: lintOutput,
				ExitCode: safeExitCode(cmd), Duration: time.Since(start).Milliseconds()}
		}
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = execErr.Error()
		}
		// Runtime errors with output are results, not gate failures.
		ok := len(stdOut) > 0 || len(stdErr) > 0
		return ForgeGateResult{OK: ok, Lang: lang, Stage: "execute",
			Error:   fmt.Sprintf("%s execution failed: %s", lang, errMsg),
			Timeout: false,
			Stdout:  stdOut, Stderr: stdErr, Lint: lintOutput,
			ExitCode: safeExitCode(cmd), Duration: time.Since(start).Milliseconds()}
	}
	return ForgeGateResult{OK: true, Lang: lang, Stage: "done",
		Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0,
		Duration: time.Since(start).Milliseconds()}
}

// timeoutDiag 构造超时诊断文本 —— 超时是「预算烧完」而非「代码有错」, 单凭
// "context deadline exceeded" 模型无法定位卡点, 只能盲猜重写(实测 14 天内 python
// 超时占 gate 缺陷 75%)。部分输出已由 Stdout/Stderr 渲染在上文, 这里只补「它是什么
// 性质 + 下一步怎么做」, 不重复贴正文。
func timeoutDiag(lang string, budget time.Duration, stdout, stderr string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s 执行超时: %s 预算烧完, 代码未跑完 —— 这不是语法/编译错误。\n", lang, budget)
	if n := len(stdout) + len(stderr); n == 0 {
		sb.WriteString("超时前无任何输出: 卡死在首次输出之前, 常见原因 死循环 / 等待输入 input() / 阻塞式网络或文件锁。\n")
	} else {
		fmt.Fprintf(&sb, "上方共 %d 字节输出是超时前的部分结果, 不代表任务完成。\n", n)
	}
	sb.WriteString("建议: 把任务拆小分批执行; 关键步骤后 print(..., flush=True) 以定位卡点。")
	return sb.String()
}
