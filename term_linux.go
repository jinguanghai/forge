//go:build linux

package main

import (
	"os"
	"syscall"
	"time"
	"unsafe"
)

// ---- Linux 平台层: termios / ioctl (T22 第 3 层) ----
//
// 零依赖说明: 标准库 syscall 在 linux 上已提供 Termios/Winsize 类型与
// TCGETS/TCSETS/TIOCGWINSZ/TIOCINQ 常量, 故无需引入 golang.org/x/sys ——
// 保住「零第三方依赖」硬约束 (补平行编辑器本需 readline/liner 库, 这里自实现)。

// ioctlTermios 是 ioctl 的薄封装(裸 syscall, 不引第三方)。
func ioctlTermios(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// isTerminal 判断 fd 是否指向 tty (TCGETS 成功即 tty)。
// 非 TTY(管道/重定向) 时行编辑无意义, readLine 退到 readLineFallback。
func isTerminal(fd int) bool {
	var t syscall.Termios
	return ioctlTermios(fd, syscall.TCGETS, unsafe.Pointer(&t)) == nil
}

// winsize 是 TIOCGWINSZ 的返回结构(与内核 struct winsize 同内存布局)。
// 本地定义而非用 syscall.Winsize —— 标准库该类型并非所有 linux 版本/架构都导出
// (实测 Go 1.26 linux/amd64: undefined: syscall.Winsize), 零依赖约束下不能引 x/sys。
type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

// getTermWidth 返回终端可见列数 (ioctl TIOCGWINSZ); 失败返回 0 由调用方回落 80。
func getTermWidth() int {
	var ws winsize
	if err := ioctlTermios(int(os.Stdout.Fd()), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil {
		return 0
	}
	return int(ws.Col)
}

// consoleWindowRect Linux 恒返回 false: 终端没有「缓冲区绝对行号」概念,
// 状态栏(需绝对定位到窗口底行)在 Linux 侧不绘制 —— 与 Windows 非 TTY 路径同构。
func consoleWindowRect() (top, bottom int, ok bool) {
	return 0, 0, false
}

// coord 占位类型: 与 term_windows.go 的 coord 同构, 仅为让 status_bar.go 跨平台编译。
type coord struct{ X, Y int16 }

// getCursorPos Linux 恒返回不可用: ANSI 没有「同步查询光标绝对位置」的原语
// (DSR \033[6n 需异步读响应, 在 raw 读循环里会与输入字节竞争)。
// 重绘因此改走相对移动模型(见 readline_editor_linux.go), 不需要绝对定位。
func getCursorPos() (coord, bool) { return coord{}, false }

// setCursorPos Linux 为空实现 —— 保留符号仅为满足 status_bar.go 跨平台编译;
// consoleWindowRect 恒 false 使 drawStatusBar 提前返回, 此函数不会被调用。
func setCursorPos(x, y int) {}

// ensureConsoleProbe 空实现(无控制台 API 需探针)。
func ensureConsoleProbe() {}

// stdinHasInput 用 TIOCINQ 查输入缓冲可读字节数(不消费字节)。
func stdinHasInput() bool {
	var n int32
	if err := ioctlTermios(int(os.Stdin.Fd()), syscall.TIOCINQ, unsafe.Pointer(&n)); err != nil {
		return false
	}
	return n > 0
}

// waitInput 等待最多 d 时间; 一旦检测到输入立即返回 true (不消费字节、不泄漏 goroutine)。
// 与 Windows 侧同名同语义 —— 粘贴突发探测(readline_session.go)靠它。
func waitInput(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if stdinHasInput() {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return stdinHasInput()
		}
		sleep := 5 * time.Millisecond
		if remaining < sleep {
			sleep = remaining
		}
		time.Sleep(sleep)
	}
}

// gbkDecode 在 Linux 恒返回 false: 终端输入为 UTF-8, 无需 GBK 回退。
// 保留同名符号以满足跨平台状态机(readline_session.go)编译。
func gbkDecode(b []byte) ([]rune, bool) { return nil, false }

// ensureCookedForApproval 审批读取前自检终端处于 cooked 模式 (Linux 对应实现)。
//
// 背景(与 Windows 20261002 事故同构): readLine 进 raw 模式(关 ICANON/ECHO),
// 若 defer 未跑(强杀/超时退)终端会残留 raw —— 主人的 shell 从此无回显、无行缓冲。
// 处置: 每次审批读取前自检, 发现 ICANON 或 ECHO 被关就补回并留痕(事件类型 console_cookguard)。
// 这是「死程序兜底」一则 —— 不依赖 readLine 的 defer 是否正确执行。
func ensureCookedForApproval() {
	fd := int(os.Stdin.Fd())
	var t syscall.Termios
	if err := ioctlTermios(fd, syscall.TCGETS, unsafe.Pointer(&t)); err != nil {
		return
	}
	want := t.Lflag | syscall.ICANON | syscall.ECHO
	if want == t.Lflag {
		return
	}
	fixed := t
	fixed.Lflag = want
	if err := ioctlTermios(fd, syscall.TCSETS, unsafe.Pointer(&fixed)); err != nil {
		return
	}
	logEvent("console_cookguard", "审批读取前补回 cooked (ICANON|ECHO)",
		map[string]uint32{"before": t.Lflag, "after": want})
}
