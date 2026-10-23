//go:build linux

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

// ---- Linux readLine: raw 模式 + 跨平台状态机 (T22 第 3 层) ----

// makeRaw 把终端切到 raw 模式(关 ICANON/ECHO/ISIG 等), 返回原设置供恢复。
//
// 逐位取舍(与 Windows rawConsoleMode 同构):
//   - 关 ICANON/ECHO: 进入行编辑(逐字节读、自己回显)
//   - 关 ISIG: Ctrl+C 作为 0x03 字节进入状态机(与 Windows handleByte 的
//     0x03 -> errInterrupt 路径同构), 而不是直接杀进程 —— 否则 defer 不跑,
//     终端残留 raw
//   - 关 IXON: 防 Ctrl+S 冻结终端
//   - 保留 OPOST: 输出侧 \n -> \r\n 转换留着, 让 fmt.Println 等既有输出语义不变
//   - VMIN=1/VTIME=0: 读操作阻塞到至少 1 字节(等价 Windows 的阻塞 Read)
func makeRaw(fd int) (*syscall.Termios, error) {
	var old syscall.Termios
	if err := ioctlTermios(fd, syscall.TCGETS, unsafe.Pointer(&old)); err != nil {
		return nil, err
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := ioctlTermios(fd, syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	return &old, nil
}

// restoreTermios 恢复终端原设置(幂等; nil 安全)。
func restoreTermios(fd int, t *syscall.Termios) {
	if t == nil {
		return
	}
	ioctlTermios(fd, syscall.TCSETS, unsafe.Pointer(t))
}

// readLine 读入一行: 上下历史 / 左右与词移动 / Home-End / Delete / Tab 补全 /
// 多行折行重绘。与 Windows 侧共用同一套状态机(readline_session.go), 只有平台层不同。
//
// 回退链(三条, 与 Windows 侧同构): FORGE_NO_READLINE=1 -> 非 TTY(管道/重定向)
// -> termios 读取失败, 三者都退到 readLineFallback(bufio 逐行读)。
func readLine(prompt string, history []string) (string, error) {
	if os.Getenv("FORGE_NO_READLINE") == "1" {
		return readLineFallback(prompt)
	}
	fd := int(os.Stdin.Fd())
	if !isTerminal(fd) {
		return readLineFallback(prompt)
	}
	old, err := makeRaw(fd)
	if err != nil {
		return readLineFallback(prompt)
	}
	// 终端恢复三重兜底(对应 Windows 20261002 控制台卡死事故的 Linux 版):
	//   ① defer —— 正常/错误返回
	//   ② 信号 handler —— 被 SIGTERM/SIGHUP/SIGQUIT 打断(关窗口/强杀)时先恢复再退
	//   ③ ensureCookedForApproval(term_linux.go) —— 万一仍残留 raw, 审批前自检补回
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	stopSig := make(chan struct{})
	defer func() {
		close(stopSig)
		signal.Stop(sigCh)
		restoreTermios(fd, old)
	}()
	go func() {
		select {
		case <-sigCh:
			restoreTermios(fd, old)
			os.Exit(130)
		case <-stopSig:
		}
	}()

	fmt.Print(prompt)

	s := newInputSession(prompt, history)
	readByte := func() (byte, error) {
		var one [1]byte
		for {
			n, err := os.Stdin.Read(one[:])
			if n > 0 {
				return one[0], nil
			}
			if err != nil {
				return 0, err
			}
			// n==0 && err==nil: 伪空读, 让步防忙转(与 Windows 侧同处理)
			time.Sleep(time.Millisecond)
		}
	}
	s.probe = waitInput
	s.readNext = readByte
	s.echoNewline = func() { fmt.Println() }
	s.redrawFn = s.ed.redraw

	var rbuf [readBatchSize]byte
	for {
		n, err := os.Stdin.Read(rbuf[:])
		if n == 0 {
			if err != nil {
				return "", err
			}
			time.Sleep(time.Millisecond)
			continue
		}
		committed, herr := s.processBatch(rbuf[:n])
		if herr != nil {
			return "", herr
		}
		if committed {
			return filterControl(string(s.ed.buf)), nil
		}
	}
}
