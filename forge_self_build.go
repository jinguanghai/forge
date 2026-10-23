package main

// forge_self_build.go — self gate 的编译阶段 (从 selfHostedSelf 拆出, 20261011 F3)
//
// 拆分动机: selfHostedSelf 148/150 行贴边 (被 doStream 的 150 遮蔽后才暴露)。
// 拆法 = 按阶段切三段: ① 改源码+备份 (selfApplyAction, 在 forge_self.go)
// ② 编译+失败回滚 (本文件) ③ 部署收尾 (仍在 selfHostedSelf)。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// selfCompileSelf 编译 self gate 的产物 exe; 编译失败自动把源码回滚到改动前备份。
//
// 返回 stdout/stderr 捕获器供调用方统一 markCaptureLimit —— 超限信息必须挂在**最终**
// 结果上 (编译失败与部署成功两条路径都要), 故不在此处 defer。
func (f *Forge) selfCompileSelf(targetExe, srcPath, backupPath string, start time.Time) (
	targetPath string, stdout, stderr *limitedWriter, res ForgeGateResult, failed bool) {
	ctx, cancel := context.WithTimeout(f.effCtx(), 60*time.Second)
	defer cancel()
	goCmd, goErr := f.findGoCommand()
	if goErr != nil {
		return "", nil, nil, ForgeGateResult{
			OK: false, Lang: "self", Stage: "compile",
			Error:    goErr.Error(),
			Duration: time.Since(start).Milliseconds(),
		}, true
	}
	targetPath = filepath.Join(f.workDir, targetExe)
	// 清理陈旧产物: 上一轮编译成功但未就位的产物若留着,
	// 之后被运行会把 exe 换成更旧的版本。清不掉(被占用)则交给 go build 覆盖。
	if _, serr := os.Stat(targetPath); serr == nil {
		stale := targetPath + ".stale_" + time.Now().Format("20060102_150405")
		if rerr := os.Rename(targetPath, stale); rerr == nil {
			os.Remove(stale)
		}
	}
	// 构建事实注入 (20261002): 版本号在源码里, 但「这一版从哪个提交编出来」源码里没有。
	buildArgs := []string{"build"}
	if ld := buildLdflags(f.workDir); ld != "" {
		buildArgs = append(buildArgs, "-ldflags", ld)
	}
	buildArgs = append(buildArgs, "-o", targetPath, ".")
	cmd := f.newCmd(ctx, goCmd, buildArgs...)
	cmd.Dir = f.workDir
	stdout = newLimitedWriter(gateCaptureLimit())
	stderr = newLimitedWriter(gateCaptureLimit())
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := runWithTimeout(ctx, cmd); err != nil {
		return targetPath, stdout, stderr, ForgeGateResult{
			OK: false, Lang: "self", Stage: "compile",
			Error:    fmt.Sprintf("go build failed: %s%s", stderr.String(), selfRollbackNote(srcPath, backupPath)),
			Stderr:   stderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}, true
	}
	return targetPath, stdout, stderr, ForgeGateResult{
		OK: true, Lang: "self", Stage: "compile", Stdout: stdout.String(),
	}, false
}

// selfRollbackNote 把源码回滚到改动前备份, 返回附加在错误信息后的说明。
// 编译失败不留半改状态 —— 无备份返回空串 (action=build 不备份), 备份不可读/回滚失败
// 都必须显式报出, 不静默。
func selfRollbackNote(srcPath, backupPath string) string {
	if backupPath == "" {
		return ""
	}
	b, rerr := os.ReadFile(backupPath)
	if rerr != nil {
		return fmt.Sprintf("\n[备份不可读: %v, 备份: %s]", rerr, backupPath)
	}
	if werr := os.WriteFile(srcPath, b, 0644); werr != nil {
		return fmt.Sprintf("\n[回滚失败: %v, 备份: %s]", werr, backupPath)
	}
	return "\n[self gate 已自动回滚 forge.go 到改动前版本]"
}
