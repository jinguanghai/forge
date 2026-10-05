package main

// memory_guard.go — 记忆写入前置拒绝权 (20261003)
//
// 背景: memoryWriteSentinel 早已把「memory.json 内容变化必经 SaveMemory」变成死程序判定,
// 但它只在 /memhealth 里只读显示 —— 旁路写入可以持续发生, 直到有人主动去看。
// 实测 20261003: 当日 10 次锚点写入全部走手工路径, memory_writes.jsonl 留痕链断在 14:24,
// 哨兵判红而 gate 照跑 (只报不拦 = 软约束, 与 GitHub 红线/sh 退役前的形态同构)。
//
// 处置: 升级为 gate 层前置拒绝权 —— 命中即拒绝本次执行, 附修复路径。
// 设计对齐 sh 退役 / 重活拒绝 / GitHub 红线: 拒绝可判定且当场截断, 猜测要烧一整轮重试。
//
// 逃生通道 (防死锁, 必须有): 本次代码含闭合留痕链的动作 → 放行, 但记审计
// (否则一次旁路即把自己永久锁死: 修留痕本身要跑 gate, 而 gate 正被这条判据拒)。
//
// 公理四: 本文件是「安全带」(前置拦截) 的执行臂, 与 git 留痕(安全网/事后可还原)
// 互为两层 —— 拦住发生在写之前, 回滚发生在写之后, 不可互相替代。
//
// 误伤边界: 留痕集合是累积的 (从不删除), 故 rollback / .bak 自愈 / 快照还原到任一
// 历史版本 → 该版本 sha 仍在集合中 → 放行。只有「从未留痕过的新内容」才被拦。
//
// 开关: FORGE_MEMORY_GUARD=0 关闭。

import (
	"fmt"
	"os"
	"strings"
)

// memoryGuardText 拒绝时的修复指引 (拒绝 + 给替代写法, 与 sh 退役文案同构)。
const memoryGuardText = `记忆写入护栏: memory.json 当前版本无写入留痕 → 疑似旁路写入。

判据: 当前 memory.json 的 sha256 前16位 必须出现在 memory_writes.jsonl 留痕集合中。
旁路写入 = 跳过 memHealthLint 体检 + 不写 anchor_audit 审计 + 使写入路径哨兵长期判红。

修复 (任选其一, 在 gate 里跑, 会自动放行):
  ① 出口工具: python .forge/forge-tools/memory_write.py exempt --reason "说明"
  ② 手工补留痕: 向 memory_writes.jsonl 追加一行
     {"time","sha","size","anchor","src":"manual-exempt","reason"}
  ③ Go 侧 SaveMemory 保存 (自带留痕)

关闭本护栏: set FORGE_MEMORY_GUARD=0 (不推荐 —— 旁路写入会重新静默)`

// memoryGuardEnabled 护栏开关: FORGE_MEMORY_GUARD=0 关闭, 其余默认开启。
func memoryGuardEnabled() bool {
	return os.Getenv("FORGE_MEMORY_GUARD") != "0"
}

// memoryGuardFixHints 闭合留痕链的动作特征。
//
// 拼接构造 (而非字面量): 避免「扫代码找模式」的元工具被自身判据命中
// (实测教训 —— 写重活判据分析脚本时被重活判据当场拦下)。
func memoryGuardFixHints() []string {
	return []string{
		"memory_" + "writes",   // 直接写留痕文件
		"Save" + "Memory",      // Go 侧保存路径
		"memory_" + "write.py", // 出口工具
	}
}

// memoryGuardFixIntent 判断本次代码是否含闭合留痕链的动作。返回命中特征 (无则空串)。
func memoryGuardFixIntent(code string) string {
	for _, h := range memoryGuardFixHints() {
		if strings.Contains(code, h) {
			return h
		}
	}
	return ""
}

// checkMemoryGuard 记忆写入前置检查。
//
// 返回 (放行?, 命中名, 说明):
//   - 放行=true, 命中名="" → 正常放行 (无留痕基线 / 当前版本有留痕 / 主文件不可读)
//   - 放行=true, 命中名!="" → 逃生通道放行 (本次代码含闭合留痕链动作, 调用方须记审计)
//   - 放行=false → 拒绝执行
func checkMemoryGuard(workDir, code string) (bool, string, string) {
	data, err := os.ReadFile(memoryFilePath(workDir))
	if err != nil {
		return true, "", "主文件不可读, 不判定"
	}
	set, n, err := loadMemoryWriteSHAs(workDir)
	if err != nil || n == 0 {
		return true, "", "无留痕基线, 不判定"
	}
	cur := sysHashPrefix(data)
	if set[cur] {
		return true, "", ""
	}
	if h := memoryGuardFixIntent(code); h != "" {
		return true, h, "本次代码含闭合留痕链动作"
	}
	return false, fmt.Sprintf("sha=%s", cur),
		fmt.Sprintf("当前 memory.json (sha=%s, %d 字节) 无写入留痕 → 旁路写入", cur, len(data))
}

// memoryGuardDeny Build 前置拒绝权的执行臂。
//
// 抽成独立函数而非内联在 Build 里: 主执行路径有行数预算(分形守卫 F2/F3),
// 内联会让 forge.go 涨到 512 行 / Build 167 行 —— 抬水位是掩盖, 不是解决。
//
// 返回 (回执, 结果, 是否拒绝): 拒绝时不产生 error(避免触发失败重试链)。
func (f *Forge) memoryGuardDeny(code, lang string) (string, *ForgeGateResult, bool) {
	if !memoryGuardEnabled() {
		return "", nil, false
	}
	ok, hit, ev := checkMemoryGuard(f.workDir, code)
	if ok {
		if hit != "" {
			logGuardEvent(f.workDir, "info", "记忆写入", hit, "exempt", "逃生通道放行: "+ev, code)
		}
		return "", nil, false
	}
	res := &ForgeGateResult{
		OK: false, Lang: lang, Stage: "rejected",
		Error: fmt.Sprintf("记忆写入护栏被拒 [%s]: %s", hit, ev),
	}
	logGuardEvent(f.workDir, "high", "记忆写入", hit, "deny", "记忆旁路写入: "+ev, code)
	return fmt.Sprintf("--- ⛔ 记忆写入护栏被拒 [rejected] ---\n命中「%s」: %s\n\n%s\n--- 结束 ---",
		hit, ev, memoryGuardText), res, true
}
