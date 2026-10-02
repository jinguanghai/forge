package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (f *Forge) forgeGateSelfHosted(code, lang string, compiler CompilerDef, input string, start time.Time) ForgeGateResult {

	// gate 启用拦截 (FORGE_GATES_ENABLED 配置; 空 = 全部启用)
	var enabled []string
	if f.cfg != nil {
		enabled = f.cfg.GatesEnabled
	}
	if !gateEnabled(lang, enabled) {
		return ForgeGateResult{
			OK: false, Lang: lang, Stage: "compile",
			Error:    fmt.Sprintf("gate 未启用: %s (FORGE_GATES_ENABLED 配置), 可用的自托管 gate: %s", lang, strings.Join(gateNames(), ", ")),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	switch lang {
	case "go":
		return f.selfHostedGo(code, compiler, start)
	case "math":
		// 内联快速路径 (P0, 20261001): 纯整数四则/幂/取模在进程内用 big.Rat 精确求值,
		// 省掉 spawn math_gate.exe + python + import sympy (实测 464ms → <1ms)。
		// 不在内联子集时 ok=false, 原路回退 spawn —— sympy 仍是唯一语义权威。
		if res, ok := f.mathInlineGate(code, start); ok {
			return res
		}
		// Auto-wrap plain expressions as JSON for math_gate
		// simplify handles arithmetic, sqrt, trig, symbolics in one pass
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"expr": code, "action": "simplify"})
			code = string(b)
		}
		return f.selfHostedGate("math_gate", code, start)
	case "logic":
		// Auto-wrap plain logic expressions as JSON for logic_gate (Z3 SAT)
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "sat", "code": code})
			code = string(b)
		}
		return f.selfHostedGate("logic_gate", code, start)
	case "knowledge":
		// Auto-wrap plain query as JSON for knowledge_gate (SPARQL)
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "query", "query": code})
			code = string(b)
		}
		return f.selfHostedGate("knowledge_gate", code, start)
	case "regex":
		// Auto-wrap plain pattern as JSON for regex_gate
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "match", "pattern": code})
			code = string(b)
		}
		return f.selfHostedGate("regex_gate", code, start)
	case "relation":
		// 接线/关系断言校验 (骨架=符号表+引用图); 裸文本按 scan 处理
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"type": "scan"})
			code = string(b)
		}
		return f.selfHostedGate("relation_gate", code, start)
	case "chain":
		return f.selfHostedChain(code, input, start)
	case "tcm":
		// Auto-wrap plain text as JSON for tcm_gate (中医认知诊断)
		if !validGateJSON(code) {
			// 药对检索: "查药对 附子 干姜" / "药对：白芍、枳实" / "配伍 桃仁 红花"
			if m := herbPairInputRE.FindStringSubmatch(code); m != nil {
				b, _ := json.Marshal(map[string]string{"type": "herb_pair", "herb1": m[1], "herb2": m[2]})
				code = string(b)
			} else {
				b, _ := json.Marshal(map[string]string{"type": "diagnose", "text": code})
				code = string(b)
			}
		}
		return f.selfHostedGate("tcm_gate", code, start)
	case "browser":
		// Auto-wrap plain text as JSON for browser_gate (浏览器自动化)
		if !validGateJSON(code) {
			b, _ := json.Marshal(map[string]string{"action": "navigate", "url": code})
			code = string(b)
		}
		return f.selfHostedGate("browser_gate", code, start)
	case "media":
		// 媒体生成 (图片/视频): MiniMax 云端 API, 产物落 .forge/media/
		return f.selfHostedGate("media_gate", code, start)
	case "self":
		return f.selfHostedSelf(code, input, start)
	default:
		// 不可达兜底: 编译器表中 SelfHosted=true 的语言集合与下方 switch 的 case 集合
		// 必须一致, 由 TestSelfHostedSwitchCoversCompilerTable 前置拦截。本条只在
		// 「表中新增 SelfHosted 条目却漏加 case」的配置漂移时可达 —— 属环境/能力缺失,
		// 不是代码文本错误, 故打标 EnvFailure: 不写缓存(补上 case 后即刻生效, 不会
		// 命中旧错误结果), 且允许换语言重跑。
		return ForgeGateResult{
			OK:         false,
			Lang:       lang,
			Stage:      "compile",
			Error:      fmt.Sprintf("self-hosted compiler not implemented: %s", lang),
			ExitCode:   -1,
			EnvFailure: true,
			Duration:   time.Since(start).Milliseconds(),
		}
	}
}

func (f *Forge) selfHostedGate(gateName, code string, start time.Time) (gateRes ForgeGateResult) {
	baseLang := strings.TrimSuffix(gateName, "_gate")
	gatePath := filepath.Join(f.toolsDir, gateName+".exe")
	if runtime.GOOS != "windows" {
		gatePath = filepath.Join(f.toolsDir, gateName)
	}
	isScript := false
	if _, statErr := os.Stat(gatePath); statErr != nil {
		scriptPath := filepath.Join(f.toolsDir, gateName+".py")
		if _, serr := os.Stat(scriptPath); serr == nil {
			gatePath = scriptPath
			isScript = true
		}
	}
	absPath, err := filepath.Abs(gatePath)
	if err != nil {
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "compile",
			Error:    fmt.Sprintf("%s path resolution failed: %v", gateName, err),
			ExitCode: -1,
			Duration: time.Since(start).Milliseconds(),
		}
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "compile",
			Error: func() string {
				suffix := ".exe"
				if runtime.GOOS != "windows" {
					suffix = ""
				}
				return fmt.Sprintf("%s%s not found -- dead boundary unavailable", gateName, suffix)
			}(),
			ExitCode:   -1,
			Duration:   time.Since(start).Milliseconds(),
			EnvFailure: true, // 死边界二进制缺失属环境问题, 非输入问题
		}
	}
	timeout := deadBoundaryTimeout
	if comp, ok := 铸剑炉_COMPILERS[baseLang]; ok && comp.ExecTimeout > 0 {
		timeout = comp.ExecTimeout
	}
	ctx, cancel := context.WithTimeout(f.effCtx(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	if isScript {
		// -u 强制无缓冲: 超时前已打印的内容必须可见(同 python gate, P0 20260930)。
		cmd = f.newCmd(ctx, "python", "-u", absPath, code)
	} else {
		cmd = f.newCmd(ctx, absPath, code)
	}

	stdout := newLimitedWriter(gateCaptureLimit())
	stderr := newLimitedWriter(gateCaptureLimit())
	defer markCaptureLimit(&gateRes, stdout, stderr)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = runWithTimeout(ctx, cmd)
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = stdout.String()
		}
		if errMsg == "" {
			errMsg = err.Error()
		}
		// 主动拒绝优先于「执行失败」: gate 以非零退出表达拒绝, 但 stdout 里有
		// 结构化 rejected:true。旧版把它当执行失败, 错误文本里塞一坨 JSON,
		// 调用方无从区分「输入不合法」与「gate 坏了」。
		if rejected, reason := parseGateReject(stdout.String()); rejected {
			return ForgeGateResult{
				OK: false, Lang: baseLang, Stage: "execute",
				Error:        fmt.Sprintf("%s 拒绝该输入: %s", gateName, reason),
				Stderr:       errMsg,
				GateRejected: true, RejectReason: reason,
				ExitCode: safeExitCode(cmd),
				Duration: time.Since(start).Milliseconds(),
			}
		}
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "execute",
			Error:    fmt.Sprintf("%s execution failed: %s", gateName, errMsg),
			Stderr:   errMsg,
			Timeout:  isTimeoutErr(err),
			ExitCode: safeExitCode(cmd),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	output := stdout.String()

	// 判定型 gate 必须有判定输出: 空 stdout = 判定缺席, 不可当成功。
	// (旧实现 if/return 两分支逐字相同, 空输出与有输出同样 OK:true —— 判定被静默吞掉)
	if output == "" && mustHaveOutputGates[baseLang] {
		return ForgeGateResult{
			OK: false, Lang: baseLang, Stage: "execute",
			Error:    fmt.Sprintf("%s gate 无输出: 判定缺失(疑似 gate 二进制异常)", gateName),
			Stderr:   stderr.String(),
			ExitCode: safeExitCode(cmd),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK: true, Lang: baseLang, Stage: "done",
		Stdout:   output,
		Duration: time.Since(start).Milliseconds(),
	}
}

func (f *Forge) selfHostedGo(code string, compiler CompilerDef, start time.Time) ForgeGateResult {
	runCode := code
	if !hasPackageClause(code) {
		if looksLikeFullGoProgram(code) {
			// 完整程序(含顶层 import/func/type 声明)但缺 package 声明:
			// 补声明头, 不可嵌套包装 —— 否则 import 会落进 func main 体内,
			// 报 "syntax error" 而非本来的 "expected 'package'"。
			runCode = "package main\n\n" + code
		} else {
			runCode = fmt.Sprintf(`package main
import "fmt"
func main() {
%s
}`, code)
		}
	}
	tmpDir, err := os.MkdirTemp("", "forge_go_")
	if err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "write",
			Error:    fmt.Sprintf("failed to create temp dir: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	defer os.RemoveAll(tmpDir)
	srcPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(srcPath, []byte(runCode), 0644); err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "write",
			Error:    fmt.Sprintf("failed to write source: %v", err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	exePath := filepath.Join(tmpDir, "main"+exeSuffix())
	compileCtx, compileCancel := context.WithTimeout(f.effCtx(), compiler.CompileTimeout)
	defer compileCancel()
	goCmd, goErr := f.findGoCommand()
	if goErr != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "compile",
			Error:    goErr.Error(),
			Duration: time.Since(start).Milliseconds(),
			// 工具链缺失属环境问题(且 downloadGo 失败通常是网络), 换语言可绕开。
			EnvFailure: true,
		}
	}
	compileCmd := f.newCmd(compileCtx, goCmd, "build", "-o", exePath, srcPath)
	var compileStderr strings.Builder
	compileCmd.Stderr = &compileStderr
	if err := runWithTimeout(compileCtx, compileCmd); err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "compile",
			Error:    fmt.Sprintf("go build failed: %s", compileStderr.String()),
			Stderr:   compileStderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	execCtx, execCancel := context.WithTimeout(f.effCtx(), compilerTimeout("go"))
	defer execCancel()
	execCmd := f.newCmd(execCtx, exePath)
	var stdout, execStderr strings.Builder
	execCmd.Stdout = &stdout
	execCmd.Stderr = &execStderr
	err = runWithTimeout(execCtx, execCmd)
	if err != nil {
		return ForgeGateResult{
			OK: false, Lang: "go", Stage: "execute",
			Error:    fmt.Sprintf("go run failed: %v — %s", err, execStderr.String()),
			Stderr:   execStderr.String(),
			Timeout:  isTimeoutErr(err),
			Duration: time.Since(start).Milliseconds(),
		}
	}
	return ForgeGateResult{
		OK: true, Lang: "go", Stage: "done",
		Stdout:   stdout.String(),
		Duration: time.Since(start).Milliseconds(),
	}
}
