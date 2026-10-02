package main

// forge_self_diag.go — self gate 契约类失败的诊断文案 (P1, 20260930)。
//
// 靶心 (六西格玛实测): self gate 契约误用 5 次/14 天 (历史 7/29 = 24.1%) ——
// 旧报错只说「格式不对」不给样例, 模型只能盲改重试。与 aider「把真实证据回灌
// 给模型」同理, 证据的载体在本仓库就是「本次哪里不合契约 + 合法输入样例」。
//
// 独立成文件的原因: forge_self.go 加这三函数后 503 行 > F2 上限 500,
// 分形守卫当场报红 —— 拆出来并配同名测试 (F4), 而不是调高阈值。

import (
	"strings"
	"time"
)

// selfContractExample 是 self gate 合法输入的样例文本 (错误反馈用, 单一源)。
//
// 靶心 (20260930 P1, 六西格玛实测): self gate 契约误用 5 次/14 天 (历史 7/29=24.1%)
// —— 报错只说「格式不对」不给样例, 模型只能盲改重试; 与 aider「把真实证据回灌给
// 模型」同理, 证据的载体在本仓库就是「合法输入样例」。
func selfContractExample() string {
	return `合法输入样例 (动作写在 input 字段, code 只放要新增/替换的代码):
  1) 追加代码: input 留空或 "append", code 必须是 Go 顶层声明
     例 code: func myHelper() string { return "hi" }
  2) 原地替换: input = "replace:旧片段:新片段"  (两个冒号, 三段)
     例 input: replace:return nil:return fmt.Errorf("boom")
  3) 仅重新编译并部署(不改源码): input = "deploy"
  4) 仅重新编译(不部署): input = "build"
注意: replace 是精确子串匹配(非模糊匹配), 旧片段必须与源文件逐字符一致(含缩进与空格)。`
}

// selfInputDiag 在 self gate 契约类失败上附「本次哪里不合契约 + 合法样例」。
// 只在失败路径调用, 不改任何判定语义。
func selfInputDiag(action, code string) string {
	a := strings.TrimSpace(action)
	c := strings.TrimSpace(code)
	var sb strings.Builder
	sb.WriteString("self gate 契约诊断: 动作取自 forge 工具的 input 字段(不是 code); 取值只有 空 / deploy / build / replace:旧:新。\n")
	switch {
	case strings.HasPrefix(a, "{") && strings.Contains(a, "action"):
		sb.WriteString("本次 input 是一个 JSON 对象 —— 契约要求 input 直接填动作词: 写 deploy, 而不是 {\"action\":\"deploy\"}。\n")
	case strings.HasPrefix(a, "replace:"):
		if parts := strings.SplitN(a[8:], ":", 2); len(parts) != 2 {
			sb.WriteString("本次 input 以 replace: 开头, 但只有一段 —— replace 必须写成 replace:旧片段:新片段(两个冒号)。\n")
		} else {
			sb.WriteString("本次 replace 未命中: 旧片段必须与源文件逐字符一致(含缩进/空格/换行); 报错里已给出本次待匹配的前 40 字。\n")
		}
	case c == "":
		sb.WriteString("本次 code 为空 —— 追加动作必须有 code; 若只想重新编译部署, 请把 input 设为 deploy。\n")
	case strings.HasPrefix(c, "{"):
		sb.WriteString("本次 code 看起来是 JSON 而不是 Go 代码 —— 动作属于 input 字段, code 只放 Go 顶层声明。\n")
	default:
		sb.WriteString("本次 code 不像合法的 Go 顶层声明 —— 必须以 func / type / var / const / import / package 之一开头。\n")
	}
	sb.WriteString(selfContractExample())
	return sb.String()
}

// selfGateErrWithDiag 与 selfGateErr 同构, 额外附 Diagnostics。
// 诊断走 Diagnostics 而非 Error: 与 P0 同构 —— 渲染层给 Diagnostics 独立分节,
// 且在 Stderr 非空时会用 Stderr 覆盖返回给上层的错误文本, 混进 Error 会被吞。
func selfGateErrWithDiag(start time.Time, diag, format string, args ...any) ForgeGateResult {
	res := selfGateErr(start, format, args...)
	res.Diagnostics = diag
	return res
}
