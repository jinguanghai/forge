package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ────────────────────────────────────────────────────────────────
// self gate 部署阶段: 编译成功之后的「最后一公里」
//
// 背景(20260913): 此前 self gate 只做到 go build -o forge_new.exe,
// 编译成功即返回 OK —— 「编译过了」被当成「能用了」。实际替换/验证/
// 留痕全靠人手写临时脚本, events.jsonl 里 5 天零条部署记录。
// 现补上: 冒烟 → 原子就位 → 失败回滚 → 留痕。
// ────────────────────────────────────────────────────────────────

// selfSmokeTimeout 冒烟超时: 只跑 --version(正常 10-50ms), 15s 是宽松上限。
const selfSmokeTimeout = 15 * time.Second

// selfGateErr 构造 self gate 的编译/写入类失败结果 (统一 Stage/ExitCode/Duration)。
// 本函数内的 11 处失败出口原先各写 6 行字面量 —— 收敛后改一处即全体生效。
func selfGateErr(start time.Time, format string, args ...any) ForgeGateResult {
	return ForgeGateResult{
		OK: false, Lang: "self", Stage: "compile",
		Error:    fmt.Sprintf(format, args...),
		ExitCode: -1,
		Duration: time.Since(start).Milliseconds(),
	}
}

// selfApplyReplace 执行 replace:old:new 动作 —— 原地改写 forge.go 源码。
// 返回的 failed=true 表示失败(格式错/未命中/读写错), 调用方应直接返回 res。
func selfApplyReplace(srcPath, action string, start time.Time) (res ForgeGateResult, failed bool) {
	parts := strings.SplitN(action[8:], ":", 2)
	if len(parts) != 2 {
		return selfGateErrWithDiag(start, selfInputDiag(action, ""),
			"replace action requires format: replace:old_text:new_text"), true
	}
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return selfGateErr(start, "读取铸剑炉.go: %v", err), true
	}
	if !strings.Contains(string(src), parts[0]) {
		prev := []rune(parts[0])
		if len(prev) > 40 {
			prev = prev[:40]
		}
		return selfGateErrWithDiag(start, selfInputDiag(action, ""),
			"replace 未命中: old_text 不存在于 forge.go (前 40 字: %s)", string(prev)), true
	}
	newSrc := strings.Replace(string(src), parts[0], parts[1], 1)
	if err := atomicWrite(srcPath, []byte(newSrc)); err != nil {
		return selfGateErr(start, "写入铸剑炉.go: %v", err), true
	}
	logEvent(EvSelfModified, "replace", map[string]string{"file": "forge.go"})
	return ForgeGateResult{}, false
}

// selfApplyAppend 执行默认的 append 动作: 把 code 插到 ForgeToolSchema 声明之前。
// 返回的 failed=true 表示失败(标记缺失/代码为空/非顶层声明/读写错), 调用方应直接返回 res。
func selfApplyAppend(srcPath, code string, start time.Time) (res ForgeGateResult, failed bool) {
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return selfGateErr(start, "读取铸剑炉.go: %v", err), true
	}
	srcStr := strings.ReplaceAll(string(src), "\r\n", "\n")
	marker := selfAppendMarker
	idx := strings.LastIndex(srcStr, marker)
	if idx < 0 {
		return selfGateErr(start, "标记 'func ForgeToolSchema()' 未在forge.go中找到"), true
	}
	if strings.TrimSpace(code) == "" {
		return selfGateErrWithDiag(start, selfInputDiag("", code),
			"self gate: code is empty, cannot append to forge.go"), true
	}
	if !looksLikeValidGoTopLevel(code) {
		return selfGateErrWithDiag(start, selfInputDiag("", code),
			"self gate: code does not look like a valid Go top-level declaration: %s", code), true
	}
	newSrc := srcStr[:idx] + code + "\n" + srcStr[idx:]
	if err := atomicWrite(srcPath, []byte(newSrc)); err != nil {
		return selfGateErr(start, "写入铸剑炉.go: %v", err), true
	}
	logEvent(EvSelfModified, "append", map[string]string{"file": "forge.go"})
	return ForgeGateResult{}, false
}

func compilerTimeout(lang string) time.Duration {
	if compiler, ok := 铸剑炉_COMPILERS[lang]; ok {
		if compiler.ExecTimeout > 0 {
			return compiler.ExecTimeout
		}
	}
	return defaultGateTimeout
}

func truncateOutput(s string) string {
	runes := []rune(s)
	if len(runes) <= MaxForgeOutputLength {
		return s
	}
	if len(runes) <= ForgeHeadKeep+ForgeTailKeep {
		return string(runes[:ForgeHeadKeep]) + "\n...[truncated]...\n" + string(runes[len(runes)-ForgeTailKeep:])
	}
	head := string(runes[:ForgeHeadKeep])
	tail := string(runes[len(runes)-ForgeTailKeep:])
	return head + "\n...[truncated]...\n" + tail
}

func (f *Forge) selfHostedChain(code, input string, start time.Time) ForgeGateResult {
	// Parse chain specification: {"stages":[{"gate":"...","input":{...},"if_verdict":"..."}],"stop_on":"error|first_success|never"}
	var req struct {
		Stages []struct {
			Gate      string          `json:"gate"`
			Input     json.RawMessage `json:"input"`
			IfVerdict string          `json:"if_verdict,omitempty"`
		} `json:"stages"`
		StopOn string `json:"stop_on"`
	}
	if err := json.Unmarshal([]byte(code), &req); err != nil {
		return ForgeGateResult{
			OK: false, Lang: "chain", Stage: "parse",
			Error:    fmt.Sprintf("invalid chain JSON: %v", err),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	if req.StopOn == "" {
		req.StopOn = "error"
	}

	type stageResult struct {
		Index     int    `json:"index"`
		Gate      string `json:"gate"`
		OK        bool   `json:"ok"`
		Stdout    string `json:"stdout,omitempty"`
		Error     string `json:"error,omitempty"`
		LatencyMs int64  `json:"latency_ms"`
	}

	var stages []stageResult
	var prevOutput string // for if_verdict substring matching
	for i, stage := range req.Stages {
		// if_verdict: skip stage if previous output doesn't contain verdict string
		if stage.IfVerdict != "" && i > 0 && !strings.Contains(prevOutput, stage.IfVerdict) {
			continue
		}

		// Extract actual code and optional input from stage's input field
		stageCode := string(stage.Input)
		stageInput := ""

		// If input is a plain JSON string literal (e.g. "fof(...)"), unwrap the quotes
		var plainStr string
		if json.Unmarshal(stage.Input, &plainStr) == nil {
			stageCode = plainStr
		}

		// If input has a "code" field (for external gates like python/node/sh),
		// extract it as the main code and pass remaining fields as the input arg.
		var rawInput map[string]json.RawMessage
		if json.Unmarshal(stage.Input, &rawInput) == nil {
			if codeField, ok := rawInput["code"]; ok {
				var codeStr string
				if json.Unmarshal(codeField, &codeStr) == nil {
					stageCode = codeStr
					delete(rawInput, "code")
					if len(rawInput) > 0 {
						if remaining, err := json.Marshal(rawInput); err == nil {
							stageInput = string(remaining)
						}
					}
				}
			}
		}
		stageStart := time.Now()
		result := f.forgeGateSkipCache(stageCode, stage.Gate, stageInput, false)
		sr := stageResult{
			Index:     i,
			Gate:      stage.Gate,
			OK:        result.OK,
			Stdout:    truncateOutput(result.Stdout),
			Error:     result.Error,
			LatencyMs: time.Since(stageStart).Milliseconds(),
		}
		stages = append(stages, sr)
		prevOutput = result.Stdout
		if !result.OK {
			prevOutput += "\n" + result.Error
		}
		if req.StopOn == "error" && !result.OK {
			break
		}
		if req.StopOn == "first_success" && result.OK {
			break
		}
	}
	allOK := true
	for _, s := range stages {
		if !s.OK {
			allOK = false
			break
		}
	}
	output, _ := json.Marshal(map[string]interface{}{
		"ok":     allOK,
		"stages": stages,
	})
	return ForgeGateResult{
		OK:       allOK,
		Lang:     "chain",
		Stage:    "done",
		Stdout:   string(output),
		Duration: time.Since(start).Milliseconds(),
	}
}

// selfBackupSource 改动前备份源文件, 并落统一快照(源码+记忆+gate+exe) + git 历史点。
// 返回的 failed=true 表示失败, 调用方应直接返回 res (不改源码)。
// 备份数上限由 pruneSelfBackups 控制, 目录不会无界增长。
//
// 备份落 .forge/backups/ (2S 归位, 20260928): 此前写 srcPath+".bak_self_*", srcPath 在
// 根目录, 于是 self gate 每改一次源码就在根目录丢一个 .bak —— 根目录垃圾的固定产地。
// 归位依据: 备份是「持久运行状态」(回滚要用), 不是「临时产物」, 与 checkpoints/updates
// 同级放 .forge/ 下; 放 .forge-temp/ 是错的 —— 那里启动即清空, 会把回滚路径一起清掉。
// 建目录失败按「无备份即拒绝改动」处理(failed=true), 不降级为「无备份也改」。
func (f *Forge) selfBackupSource(srcPath string, start time.Time) (backupPath string, res ForgeGateResult, failed bool) {
	backupDir := selfBackupDir(f.workDir)
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", selfGateErr(start, "self gate: 建备份目录失败, 拒绝无备份改动: %v", err), true
	}
	backupPath = filepath.Join(backupDir, filepath.Base(srcPath)+".bak_self_"+time.Now().Format("20060102_150405.000000000"))
	src0, err0 := os.ReadFile(srcPath)
	if err0 != nil {
		return "", selfGateErr(start, "self gate: 读取 forge.go 失败, 拒绝无备份改动: %v", err0), true
	}
	if err := os.WriteFile(backupPath, src0, 0644); err != nil {
		return "", selfGateErr(start, "self gate: 备份失败, 拒绝改动: %v", err), true
	}
	pruneSelfBackups(backupDir, srcPath, backupKeepCount)
	// self gate 强制主人审批, 批准后执行到此处才落快照。
	ts := time.Now().Format("20060102_150405")
	if err := createCheckpoint(f.workDir, ts, "self"); err == nil {
		if h := gitSnapshot(f.workDir, "self"); h != "" {
			logEvent(EvSelfModified, "self-git-snapshot", map[string]string{"git": h})
		}
	}
	return backupPath, ForgeGateResult{}, false
}

// locateSelfSource 定位 self gate 要改写的源文件。
//
// 铸剑炉源码已按职责拆成多个 .go 文件, 因此不能写死 forge.go —— 函数一旦搬出
// forge.go, 写死路径的 self gate 就再也改不到它(等于拆自己的手术刀)。
//
// 定位键 = 动作自身的匹配键, 与后续实际改写用的是同一个键, 因此"定位到哪"与
// "改到哪"必然一致:
//
//	replace: old_text (selfApplyReplace 用它判命中)
//	append : selfAppendMarker (selfApplyAppend 用它找插入点)
//
// 匹配数 != 1 时【拒绝】而非"取第一个": 取错文件 = 改错代码 + 备份错文件 = 双错,
// 且事后无法从备份恢复。零命中回退 forge.go, 让 selfApplyXxx 报出原有错误文案
// (保持旧行为与错误信息不变)。
func (f *Forge) locateSelfSource(action string) (string, error) {
	def := filepath.Join(f.workDir, "forge.go")
	needle := selfAppendMarker
	if strings.HasPrefix(action, "replace:") {
		parts := strings.SplitN(action[8:], ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return def, nil // 格式错留给 selfApplyReplace 报原有错误
		}
		needle = parts[0]
	}
	entries, err := os.ReadDir(f.workDir)
	if err != nil {
		return def, nil // 目录读不到 -> 回退旧路径, 由后续读写报错
	}
	var hits []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(f.workDir, name))
		if rerr != nil {
			continue
		}
		if strings.Contains(strings.ReplaceAll(string(b), "\r\n", "\n"), needle) {
			hits = append(hits, name)
		}
	}
	switch len(hits) {
	case 0:
		return def, nil
	case 1:
		return filepath.Join(f.workDir, hits[0]), nil
	default:
		sort.Strings(hits)
		return "", fmt.Errorf("self gate 定位歧义: 匹配键同时出现在 %d 个源文件中 %v -- 拒绝改写(请加长 old_text 使其唯一)", len(hits), hits)
	}
}

func (f *Forge) selfHostedSelf(code, input string, start time.Time) (gateRes ForgeGateResult) {
	// 产物名恒为 forge_new.exe: 就位统一由 deploySelfExe 负责。
	// 旧版按自身进程名切换目标(build -o forge.exe 直接覆盖生产 exe) =
	// 「没冒烟就先替换」, 正是本轮回补的缺口。
	targetExe := "forge_new.exe"
	action := input
	if action == "" {
		action = "append"
	}

	// restart 动作已废弃：不再触发任何行为，直接返回。
	if action == "restart" {
		return ForgeGateResult{
			OK: true, Lang: "self", Stage: "restart",
			Summary:  "restart 动作已废弃，不再生效。",
			ExitCode: 0,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	srcPath, lerr := f.locateSelfSource(action)
	if lerr != nil {
		return selfGateErr(start, "%v", lerr)
	}
	var backupPath string // self gate 改动前备份路径; go build 失败时自动回滚用

	if action != "build" {
		bp, bres, bfailed := f.selfBackupSource(srcPath, start)
		if bfailed {
			return bres
		}
		backupPath = bp
	}
	switch {
	case action == "build":
		// just rebuild, no source modification
	case action == "deploy":
		// 仅重新编译并部署, 不改源码 (20260913): 供"改了非 forge.go 源文件后需热替换"的场景
		// —— build 动作只编译不部署, append/replace 又必改源码, 此前无"只部署"通道。
	case strings.HasPrefix(action, "replace:"):
		if res, failed := selfApplyReplace(srcPath, action, start); failed {
			return res
		}
	default: // "append"
		if res, failed := selfApplyAppend(srcPath, code, start); failed {
			return res
		}
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), 60*time.Second)
	defer cancel()
	goCmd, goErr := f.findGoCommand()
	if goErr != nil {
		return ForgeGateResult{
			OK: false, Lang: "self", Stage: "compile",
			Error:    goErr.Error(),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	targetPath := filepath.Join(f.workDir, targetExe)
	// 清理陈旧产物: 上一轮编译成功但未就位的 forge_new.exe 若留着,
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
	stdout := newLimitedWriter(gateCaptureLimit())
	stderr := newLimitedWriter(gateCaptureLimit())
	defer markCaptureLimit(&gateRes, stdout, stderr)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := runWithTimeout(ctx, cmd); err != nil {
		// 编译失败 → 自动回滚 forge.go 到本次改动前的备份(不留半改状态)。
		note := ""
		if backupPath != "" {
			if b, rerr := os.ReadFile(backupPath); rerr == nil {
				if werr := os.WriteFile(srcPath, b, 0644); werr == nil {
					note = "\n[self gate 已自动回滚 forge.go 到改动前版本]"
				} else {
					note = fmt.Sprintf("\n[回滚失败: %v, 备份: %s]", werr, backupPath)
				}
			} else {
				note = fmt.Sprintf("\n[备份不可读: %v, 备份: %s]", rerr, backupPath)
			}
		}
		return ForgeGateResult{
			OK: false, Lang: "self", Stage: "compile",
			Error:    fmt.Sprintf("go build failed: %s%s", stderr.String(), note),
			Stderr:   stderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	// ── 部署阶段: 编译成功 != 能跑 ──
	// action=build 保持原语义(只编译); FORGE_SELF_NODEPLOY=1 是应急刹车。
	if action == "build" {
		return ForgeGateResult{
			OK: true, Lang: "self", Stage: "done",
			Stdout:   fmt.Sprintf("built %s successfully (build 动作: 不部署)\n%s", targetExe, stdout.String()),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	if os.Getenv("FORGE_SELF_NODEPLOY") == "1" {
		return ForgeGateResult{
			OK: true, Lang: "self", Stage: "done",
			Stdout:   fmt.Sprintf("built %s successfully (FORGE_SELF_NODEPLOY=1: 跳过部署)\n%s", targetExe, stdout.String()),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	outcome, derr := deploySelfExe(f.workDir, targetPath, backupKeepCount, realSmokeRunner)
	if derr != nil {
		logEvent(EvSelfModified, "self-deploy-failed", map[string]string{
			"err": derr.Error(), "sha256": outcome.SHA256,
		})
		return ForgeGateResult{
			OK: false, Lang: "self", Stage: "deploy",
			Error:    fmt.Sprintf("编译成功但部署失败: %v\n[旧 exe 未被破坏, 源码保留待修]", derr),
			Stdout:   stdout.String(),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	logEvent(EvSelfRestart, "self-deploy", map[string]string{
		"sha256":   outcome.SHA256,
		"backup":   outcome.BackupPath,
		"smoke_ms": strconv.FormatInt(outcome.SmokeMS, 10),
		"exe":      "forge.exe",
	})
	report := fmt.Sprintf("built %s successfully\n冒烟通过(%dms) → 已就位 forge.exe\nsha256=%s\n备份=%s",
		targetExe, outcome.SmokeMS, outcome.SHA256, outcome.BackupPath)
	if stdout.Len() > 0 {
		report += "\n" + stdout.String()
	}
	return ForgeGateResult{
		OK: true, Lang: "self", Stage: "done",
		Stdout:   report,
		Duration: time.Since(start).Milliseconds(),
	}
}
