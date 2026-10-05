package main

// preflight.go — 前置拒绝权统一执行臂 (20261003)
//
// 为什么抽出来: Build 是主执行路径, 有行数预算(分形守卫 F2/F3 —— 实测内联后
// Build 152 > 150, forge.go 511 > 500)。每加一条前置拒绝就吃一次预算,
// 而抬水位是掩盖不是解决(实测教训: 水位必须跟随真实债务下降)。
//
// 本文件收拢两类拒绝, 共同点: 判据确定性(命中即拒) + 拒绝自带修复路径。
//   · GitHub 出口红线 —— 第三方镜像绝不可接触登录态/私有内容/凭据
//   · 记忆写入完整性 —— memory.json 变化必须留痕(否则跳过体检与审计)
// 降级臂保留: GitHub 跨行共现只记审计不拦 —— 静态不可判定数据流, 误伤代价高于收益。
//
// 公理四: git=安全网(事后可还原) 与 gate=安全带(前置拦截) 是两层, 不可互相替代 ——
// 本文件是「安全带」侧的统一入口: 命中即拒, 不指望事后回滚兜底。

import "fmt"

// preflightDeny 前置拒绝权统一入口。返回 (回执, 结果, 是否拒绝)。
func (f *Forge) preflightDeny(code, lang string) (string, *ForgeGateResult, bool) {
	if out, res, denied := f.netRedlineDeny(code, lang); denied {
		return out, res, true
	}
	return f.memoryGuardDeny(code, lang)
}

// netRedlineDeny GitHub 红线执行臂。
//
// agent_memory.go 早已写「第三方镜像只可用于公开内容, 登录态/私有仓库/凭据绝不可经它」,
// 实测(20261003)全仓代码 0 处判据 —— 红线 100% 靠自觉, 物理上零拦截。
// 处置: 升级为 gate 层前置拒绝(命中即拒 + 附出口工具 netroute.py)。
func (f *Forge) netRedlineDeny(code, lang string) (string, *ForgeGateResult, bool) {
	if !netRedlineEnabled() {
		return "", nil, false
	}
	if hitName, ev, bad := checkNetRedline(code); bad {
		res := &ForgeGateResult{
			OK: false, Lang: lang, Stage: "rejected",
			Error: fmt.Sprintf("GitHub 红线被拒 [%s]: %s", hitName, ev),
		}
		logGuardEvent(f.workDir, "high", "红线", hitName, "deny", "GitHub 红线: "+ev, code)
		return fmt.Sprintf("--- ⛔ GitHub 红线被拒 [rejected] ---\n命中「%s」: %s\n\n%s\n--- 结束 ---",
			hitName, ev, netRedlineText), res, true
	}
	if w := netWarnCrossLine(code); w != "" {
		logGuardEvent(f.workDir, "info", "红线", w, "allow", "跨行共现(降级告警, 未拦)", code)
	}
	return "", nil, false
}
