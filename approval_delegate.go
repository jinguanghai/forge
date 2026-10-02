package main

// approval_delegate.go — 委托模式: 把「危险操作人工审批」从阻塞等待改为
// 「默认放行 + 全文留痕 + 后悔窗口」(20261002)。
//
// 动机 (实测, 非推测): .forge/gate_audit.jsonl 累计 382 次审批, approval_wait_ms
// 合计 59063 秒 = 16.41 小时, 单次最长 28608 秒 (7.9 小时); 而 events.jsonl 的
// approved 事件 382 条、审批拒绝 (guard_blocked) 仅 2 条 —— 这道闸门实测拦截率
// 约 0.5%, 代价是主人 16.4 小时空等。
//
// 根因 (反境): 闸门的真实价值不是「拦住」(实测几乎没拦过), 而是「记录谁批了
// 哪段代码」。主人不懂代码与英语, 无法对代码内容做安全裁决 —— 要求他在 10 秒内
// 裁决一段 300 行脚本, 是把机器该负的责任推给人。
//
// 处置 (合境): 保留「全文取证 + 索引 + 事件」三件套 (审计价值不降), 去掉
// 「阻塞等待」。档位由 FORGE_DELEGATE 决定:
//
//	未设/空/其它 → 关闭: 人工确认, 行为与引入前逐字节一致
//	"1"          → 窗口档: 倒计时 N 秒自动放行; 期间按任意键转人工确认
//	"all"        → 直通档: 不等窗口, 立即放行
//
// 窗口长度由 FORGE_DELEGATE_WINDOW (秒) 控制, 默认 10, 0 = 不等窗口, 上限 600。
// 自动放行的事件带 mode="auto", 与人工批准 (无 mode 字段) 可区分, 事后审计不降级。

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// approvalDelegateDefaultWindowSec 窗口档默认倒计时秒数 (后悔窗口)。
	approvalDelegateDefaultWindowSec = 10
	// approvalDelegateMaxWindowSec 窗口上限: 防误配成天文数字, 变回阻塞等待。
	approvalDelegateMaxWindowSec = 600
)

// approvalKeyProbeFunc 按键探针 (平台实现见 approval_delegate_windows.go 与
// approval_delegate_other.go)。声明为变量是为了让判据能注入 —— 真实控制台无法
// 在测试里敲键, 不注入则「按键转人工」这条分支永远无判据可钉。
var approvalKeyProbeFunc = approvalKeyProbe

// approvalDelegateMode 读取 FORGE_DELEGATE 环境变量并解析档位 (薄壳)。
func approvalDelegateMode() string {
	return parseDelegateMode(os.Getenv("FORGE_DELEGATE"))
}

// parseDelegateMode 解析委托档位: "" = 关闭, "1" = 窗口档, "all" = 直通档。
// 非法取值一律按关闭处理 (fail-closed: 不认识的配置不得静默放行)。
//
// 为什么抽成纯函数 (判据缺陷, 20261002): 原 TestApprovalDelegate_Modes 在循环里
// 传 mode 却调用无参的 approvalDelegateMode(), 读的永远是进程环境 —— 生产 User 级
// FORGE_DELEGATE=all 时该断言必然报红; 更糟的是, 即使下面 fail-closed 分支被整段
// 删除, 它也照样只反映环境, 判据等于不存在。纯函数才能钉住语义本身。
func parseDelegateMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return "1"
	case "all", "force", "direct":
		return "all"
	}
	return ""
}

// approvalDelegateWindow 倒计时窗口。未设/非法/负值 → 默认值; 超上限 → 上限。
func approvalDelegateWindow() time.Duration {
	raw := strings.TrimSpace(os.Getenv("FORGE_DELEGATE_WINDOW"))
	if raw == "" {
		return approvalDelegateDefaultWindowSec * time.Second
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return approvalDelegateDefaultWindowSec * time.Second
	}
	if n > approvalDelegateMaxWindowSec {
		n = approvalDelegateMaxWindowSec
	}
	return time.Duration(n) * time.Second
}

// approvalDelegateLabel 档位中文标签 (横幅与审批提示共用, 避免两处措辞漂移)。
func approvalDelegateLabel(mode string) string {
	switch mode {
	case "all":
		return "直通"
	case "1":
		return "窗口"
	}
	return "关闭"
}

// approvalDelegateWait 窗口档等待。返回 true = 期间检测到按键 (转人工确认)。
// 探针只窥视不消费, 故转人工后 ReadString 仍能读到主人真正敲的内容。
func approvalDelegateWait(window time.Duration) bool {
	if window <= 0 {
		return false
	}
	deadline := time.Now().Add(window)
	last := -1
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			fmt.Fprint(os.Stderr, "\r\033[K")
			return false
		}
		if sec := int(remain.Seconds()) + 1; sec != last {
			last = sec
			fmt.Fprintf(os.Stderr, "\r\033[K  ⏳ 委托模式: %d 秒后自动放行 (按任意键改为人工确认)", sec)
		}
		step := 200 * time.Millisecond
		if remain < step {
			step = remain
		}
		if approvalKeyProbeFunc(step) {
			fmt.Fprint(os.Stderr, "\r\033[K  ⏸ 检测到按键, 转为人工确认\n")
			return true
		}
	}
}

// confirmByDelegate 委托模式的放行判定。返回 (allowed, decided):
//
//	decided=false → 未启用或已转人工, 调用方必须走原人工交互 (默认路径零改动)
//	decided=true  → 已定夺, allowed 即结论
//
// ─── 放行分级: 委托档不得代批的类别 (20261002 事故修复) ─────────────
//
// 事故: 15:17:02 模型写的清理代码含 os.system('taskkill /F /IM forge.exe'), 检测层
// 正确命中「自杀」并打印了提示 —— 但原 confirmByDelegate 只拿到档位与指纹, 拿不到
// 危险类别, 直通档遂无条件 return true → 炉体被自己杀死 (events.jsonl 在 approved
// 那一行硬截断, memory.json 落后 78 分钟, 无任何收尾)。
//
// 根因: 放行决策与检测结果解耦 —— 检测到了照样放行。修法: 把类别接进放行判定。
//
// 两档禁令, 力度不同:
//
//	irreversibleKinds 不可逆自毁类 → 直接拒绝执行, 连批准入口都不给 (力度 B,
//	  20261002 定案)。理由: 外部强杀跳过全部收尾(审计/记忆/自替换备份)且无正当
//	  用途 —— 炉子已有受控重启通道 (self gate / runSelfReplace)。拒绝可判定,
//	  猜测要烧一整轮 (与 sh gate 退役同一哲学)。
//	humanOnlyKinds 对象不明类 → 不走自动放行, 回落人工确认 (仍可人工批准)。理由:
//	  20261001 实战中 taskkill /F /PID 把主进程干掉过; 而 20261002 复核的 10 条同类
//	  告警其实在杀测试自己的子进程 —— 直接拒会误伤正常开发, 一律代批又会重演事故。
var irreversibleKinds = map[string]bool{
	"自杀": true, // 终止炉体本体: 不可逆, 无正当用途
	"磁盘": true, // 格式化磁盘: 不可逆, 无正当用途
	"炸弹": true, // fork 炸弹: 不可逆, 无正当用途
}

// humanOnlyKinds 必须人工确认的类别: 委托档不得代批, 但可人工批准。
var humanOnlyKinds = map[string]bool{
	"进程终止": true, // taskkill /PID: 无法辨别父子进程
}

// isIrreversibleKind 是否属不可逆自毁类 (直接拒绝, 无批准入口)。
func isIrreversibleKind(kind string) bool { return irreversibleKinds[kind] }

// delegateMayAutoApprove 委托档能否代批该类别 —— 放行决策必须看类别, 而非只看档位。
func delegateMayAutoApprove(kind string) bool {
	return !irreversibleKinds[kind] && !humanOnlyKinds[kind]
}

// 注意: mode=="" 时本函数不打印任何字节 —— 关闭委托的输出必须与引入前逐字节一致。
func confirmByDelegate(mode, kind, fp string) (bool, bool) {
	if mode == "" {
		return false, false
	}
	// 类别分级: 不可逆自毁类与对象不明类一律不代批 (20261002 事故修复点)。
	// 调用方对不可逆类已做过硬拒绝, 此处仍独立复核 —— 放行闸门不得依赖调用方自觉。
	if !delegateMayAutoApprove(kind) {
		fmt.Fprintf(os.Stderr, "  %s 委托模式[%s] 不适用于「%s」类 — 转人工确认 (留痕: %s)\n",
			color(ansi.yellow, "🛑"), approvalDelegateLabel(mode), kind, fp)
		return false, false
	}
	fmt.Fprintf(os.Stderr, "  %s 委托模式[%s] — 不再阻塞等键盘, 自动放行并留痕 (撤销: setx FORGE_DELEGATE \"\")\n",
		color(ansi.yellow, "⚡"), approvalDelegateLabel(mode))
	if mode == "all" {
		fmt.Fprintf(os.Stderr, "  ✅ 已自动放行 (直通档)  留痕: %s\n", fp)
		return true, true
	}
	if !approvalDelegateWait(approvalDelegateWindow()) {
		fmt.Fprintf(os.Stderr, "  ✅ 已自动放行 (窗口到期)  留痕: %s\n", fp)
		return true, true
	}
	return false, false
}

// irreversibleRefusalText 不可逆自毁类的拒绝回执。
// 措辞三要求: ① 说清「不是等你批准, 是已经拒绝」; ② 给模型可执行的替代路径 ——
// 否则它只会换一种写法再撞一次; ③ 让不懂代码的主人也能看懂刚才发生了什么。
func irreversibleRefusalText(kind, hit string) string {
	return fmt.Sprintf("  🛑 已直接拒绝 [%s]: 该操作会终止/毁坏铸剑炉本体, 不提供批准入口。\n"+
		"     命中: %s\n"+
		"     原因: 外部强杀会跳过全部收尾(审计落盘/记忆归档/自替换备份), 实测可让炉体静默消失。\n"+
		"     替代: 需要重启/升级铸剑炉 → 走 self gate (runSelfReplace), 那是有序收尾的正规通道;\n"+
		"           需要停掉测试起的子进程 → 用 taskkill /PID (该类转人工确认)。\n", kind, hit)
}
