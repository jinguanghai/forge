package main

import (
	"time"
	"unicode/utf8"
)

// ---- readLine ----

// readLine reads a line with up/down history navigation and Tab completion.
// Falls back to plain line mode when console raw mode is unavailable or
// FORGE_NO_READLINE=1 is set.
//
// 多行粘贴支持：检测到突发字节流（一次 Read >=2 字节）即进入粘贴模式，
// 粘贴内容中的换行不触发提交；流空闲（无新字节）或换行后无
// 后续字节（newlineProbe）才提交。多行粘贴作为一条完整输入返回。
// inputSession 封装一次行输入的编辑会话，含粘贴突发收集状态机。
// 终端 IO 依赖通过字段注入，便于单元测试（真实 readLine 注入控制台实现）。
type inputSession struct {
	ed         *lineEditor
	history    []string
	histIdx    int
	pending    []byte
	burst      bool
	sawNewline bool

	// 注入的终端依赖
	probe       func(time.Duration) bool // 探测输入缓冲是否有后续字节（waitInput）
	readNext    func() (byte, error)     // 读取一个后续字节
	echoNewline func()                   // 提交时打印换行
	redrawFn    func()                   // 整块重绘
}

func newInputSession(prompt string, history []string) *inputSession {
	return &inputSession{
		ed:      newLineEditor(prompt),
		history: history,
		histIdx: len(history), // len(history) == "new input" position
	}
}

func consumePending(ed *lineEditor, pending *[]byte, silent bool) {
	for len(*pending) > 0 {
		r, sz := utf8.DecodeRune(*pending)
		if r != utf8.RuneError || sz > 1 {
			if silent {
				insertSilent(ed, r)
			} else {
				insertRune(ed, r)
			}
			*pending = (*pending)[sz:]
			continue
		}
		// Invalid or incomplete sequence.
		if utf8.RuneStart((*pending)[0]) {
			if len(*pending) < utf8.UTFMax {
				break
			}
			if rs, ok := gbkDecode(*pending); ok && len(rs) > 0 {
				for _, rr := range rs {
					if silent {
						insertSilent(ed, rr)
					} else {
						insertRune(ed, rr)
					}
				}
				*pending = (*pending)[:0]
				break
			}
			*pending = (*pending)[1:]
			continue
		}
		if rs, ok := gbkDecode(*pending); ok && len(rs) > 0 {
			for _, rr := range rs {
				if silent {
					insertSilent(ed, rr)
				} else {
					insertRune(ed, rr)
				}
			}
			*pending = (*pending)[:0]
			break
		}
		*pending = (*pending)[1:]
	}
}

// flushPending decodes any leftover bytes (e.g. GBK bytes pending at the
// moment Enter was pressed) so they are not silently lost.
func flushPending(ed *lineEditor, pending *[]byte) {
	consumePending(ed, pending, true)
	if len(*pending) > 0 {
		if rs, ok := gbkDecode(*pending); ok && len(rs) > 0 {
			for _, rr := range rs {
				insertSilent(ed, rr)
			}
		}
		*pending = (*pending)[:0]
	}
}

// handleByte 处理一个非换行字节。返回 committed=true 表示输入已提交。
// applySeq 执行一个 ESC 序列(不含 ESC 前缀, 如 "[A"/"[1;5C"/"[200~")对应的动作。
// 批内完整序列与流式序列共用同一动作表; 未知序列静默忽略(色码/功能键)。
func (s *inputSession) applySeq(seq string) {
	switch seq {
	case "[A", "[5~": // Up / PgUp: history back
		if s.histIdx > 0 {
			s.histIdx--
			s.ed.buf = []rune(s.history[s.histIdx])
			s.ed.cursor = len(s.ed.buf)
			s.redrawFn()
		}
	case "[B", "[6~": // Down / PgDn: history forward
		if s.histIdx < len(s.history) {
			s.histIdx++
			if s.histIdx == len(s.history) {
				s.ed.buf = nil
			} else {
				s.ed.buf = []rune(s.history[s.histIdx])
			}
			s.ed.cursor = len(s.ed.buf)
			s.redrawFn()
		}
	case "[C": // Right
		if s.ed.cursor < len(s.ed.buf) {
			s.ed.cursor++
			s.redrawFn()
		}
	case "[D": // Left
		if s.ed.cursor > 0 {
			s.ed.cursor--
			s.redrawFn()
		}
	case "[H", "[1~": // Home
		if s.ed.cursor != 0 {
			s.ed.cursor = 0
			s.redrawFn()
		}
	case "[F", "[4~": // End
		if s.ed.cursor != len(s.ed.buf) {
			s.ed.cursor = len(s.ed.buf)
			s.redrawFn()
		}
	case "[3~": // Delete
		if s.ed.cursor < len(s.ed.buf) {
			s.ed.buf = append(s.ed.buf[:s.ed.cursor], s.ed.buf[s.ed.cursor+1:]...)
			s.redrawFn()
		}
	case "[1;5C": // Ctrl+Right
		s.ed.cursor = nextWord(s.ed.buf, s.ed.cursor)
		s.redrawFn()
	case "[1;5D": // Ctrl+Left
		s.ed.cursor = prevWord(s.ed.buf, s.ed.cursor)
		s.redrawFn()
	}
	// "[2~" (Insert) and F1-F12 sequences are intentionally ignored.
}

func (s *inputSession) handleByte(ch byte) (bool, error) {
	switch {
	case ch == 0x03: // Ctrl+C
		s.echoNewline()
		flushPending(s.ed, &s.pending)
		return true, errInterrupt

	case ch == 0x1b: // ESC sequence (arrows, Delete, Home/End, Fn keys...)
		if s.burst {
			// 粘贴流中的 ESC：丢弃（防 ANSI 色码花屏），不阻塞收集
			return false, nil
		}
		seq := readEscapeSeq(s.readNext)
		s.applySeq(seq)
		// "[2~" (Insert) and F1-F12 sequences are intentionally ignored.

	case ch == 0x08 || ch == 0x7f: // Backspace
		if s.burst {
			consumePending(s.ed, &s.pending, false) // 先刷 pending，保持删除顺序正确
		}
		if s.ed.cursor > 0 {
			s.ed.buf = append(s.ed.buf[:s.ed.cursor-1], s.ed.buf[s.ed.cursor:]...)
			s.ed.cursor--
			if !s.burst {
				s.redrawFn()
			}
		}

	case ch == 0x09: // Tab
		completed := tabComplete(string(s.ed.buf))
		if completed != "" {
			s.ed.buf = []rune(completed)
			s.ed.cursor = len(s.ed.buf)
			if !s.burst {
				s.redrawFn()
			}
		} else if s.burst {
			consumePending(s.ed, &s.pending, false) // 先刷 pending 再插入，保持顺序
			insertRune(s.ed, '\t')
		} else {
			insertRune(s.ed, '\t')
		}

	default:
		// UTF-8 multi-byte handling with a GBK fallback.
		s.pending = append(s.pending, ch)
		if !s.burst {
			consumePending(s.ed, &s.pending, false)
		}
		// burst 模式：字节先累积，流结束时统一解码+重绘
	}
	return false, nil
}

// commit 提交当前缓冲：重绘确保显示正确，打印换行，清空 pending。
func (s *inputSession) commit() (bool, error) {
	s.redrawFn()
	// 多行输入时编辑光标可能在块中间：先把光标移到块底再换行,
	// 否则后续输出从中间行开始会覆盖块下方内容("看不到自己写了什么")。
	if s.ed.lastLines > 0 {
		commitCursorPlacement(s.ed)
	}
	s.echoNewline()
	flushPending(s.ed, &s.pending)
	return true, nil
}

// processBatch 处理一批从 stdin 读到的字节（批量读取优化）。
// 返回 committed=true 表示输入已提交（调用方应立即返回 string(ed.buf)）。
// processBatch 处理一批输入字节。返回 committed=true 表示输入已提交。
// burst 唯一入口 = 批内/批尾换行(粘贴特征); 中文IME上屏(纯文字)永不触发。
func (s *inputSession) processBatch(data []byte) (bool, error) {
	for i := 0; i < len(data); {
		consumed, committed, err := s.stepBatch(data, i)
		if err != nil {
			return committed, err
		}
		if committed {
			return true, nil
		}
		i += consumed
	}
	return s.finishBatch()
}

// stepBatch 处理 data[i] 起的一个逻辑单元(换行 / ESC 序列 / 普通字节)。
// 返回消费的字节数(>=1)与是否已提交。
func (s *inputSession) stepBatch(data []byte, i int) (int, bool, error) {
	switch ch := data[i]; {
	case ch == '\r' || ch == '\n':
		return s.stepNewline(data, i, ch)
	case ch == 0x1b:
		return s.stepEscape(data, i)
	default:
		return s.stepByte(ch)
	}
}

// stepByte 处理单个普通字节(非换行非 ESC)。
func (s *inputSession) stepByte(ch byte) (int, bool, error) {
	committed, err := s.handleByte(ch)
	if err != nil {
		return 1, committed, err
	}
	return 1, committed, nil
}

// stepNewline 处理换行字节(20260808 重写):
//   - 批中换行(后面还有内容) -> 必然是粘贴: 动态进入收集模式(手动输入不可能批中带\n)
//   - 批尾换行(非burst)      -> 手动回车: 立即提交, 0延迟, 不做跨批探测
//   - 批尾换行(burst)        -> 粘贴流的换行: 先刷积累字节再保留\n(顺序保真)
func (s *inputSession) stepNewline(data []byte, i int, ch byte) (int, bool, error) {
	if i+1 < len(data) {
		// 批中换行: 粘贴特征, 进入收集模式
		s.burst = true
		s.sawNewline = true
		consumePending(s.ed, &s.pending, false)
		consumed := 1
		if ch == '\r' && data[i+1] == '\n' {
			consumed = 2 // CRLF 同批
		}
		insertRune(s.ed, '\n')
		return consumed, false, nil
	}
	if s.burst {
		// 粘贴流中的批尾换行: 先刷积累字节再插换行(保序)
		s.sawNewline = true
		consumePending(s.ed, &s.pending, false)
		insertRune(s.ed, '\n')
		return 1, false, nil
	}
	return s.stepTailNewline(ch)
}

// stepTailNewline 处理「批尾换行且非 burst」: 30ms 短探测区分手动回车与跨批粘贴。
// (20260808: 原 300ms 会把回车后立刻打的字误判为粘贴并吞进 pending;
// 30ms 覆盖粘贴批间间隔(<1ms); 手动回车最多延迟 30ms 提交, 人无感知)
func (s *inputSession) stepTailNewline(ch byte) (int, bool, error) {
	if !s.probe(30 * time.Millisecond) {
		// 手动回车: 立即提交(30ms 内无后续 -> 提交, 不吞键)
		committed, err := s.commit()
		return 1, committed, err
	}
	next, err := s.readNext()
	if err != nil {
		return 1, false, err
	}
	s.burst = true
	s.sawNewline = true
	consumePending(s.ed, &s.pending, false)
	if ch == '\r' && next == '\n' {
		insertRune(s.ed, '\n') // CRLF: 批尾\r + 开头\n = 一个换行
		return 1, false, nil
	}
	insertRune(s.ed, '\n') // 保留跨批换行
	s.pending = append(s.pending, next)
	return 1, false, nil
}

// stepEscape 处理 ESC 起始字节(20260808): 批内完整序列解析(导航键执行/色码静默忽略);
// 批尾不完整序列丢弃到批尾(防色码残片); 孤立/批尾 ESC 交给 handleByte 流式兜底。
func (s *inputSession) stepEscape(data []byte, i int) (int, bool, error) {
	if i+1 >= len(data) || data[i+1] != '[' {
		return s.stepByte(0x1b)
	}
	j := i + 2
	for j < len(data) && !isSeqFinalByte(data[j]) {
		j++
	}
	if j < len(data) {
		j++ // 含终止字节
		// 批内完整序列: 导航执行, 色码/未知序列静默忽略
		s.applySeq(string(data[i+1 : j]))
		return j - i, false, nil
	}
	// 序列不完整(截断在批尾): 丢弃到批尾, 防色码残片花屏
	return len(data) - i, false, nil
}

// isSeqFinalByte 判断字节是否为 CSI 序列的终止字节(字母或 ~)。
func isSeqFinalByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '~'
}

// finishBatch 处理批尾: burst 短探测判断粘贴流是否还在继续。
// (20260808: 原粘贴空闲判定=300ms 会吞掉手动回车后 300ms 内键入的字符 -> "突然无法输入")
func (s *inputSession) finishBatch() (bool, error) {
	if !s.burst {
		return false, nil
	}
	if s.probe(30 * time.Millisecond) {
		// 还有后续字节：读一个并处理，然后由调用方继续 Read
		return s.consumeTrailing()
	}
	// 粘贴流结束
	s.burst = false
	consumePending(s.ed, &s.pending, false)
	s.redrawFn()
	if s.sawNewline {
		return s.commit() // 多行粘贴：直接提交
	}
	return false, nil // 单行粘贴：留在编辑区，等待用户继续编辑/回车
}

// consumeTrailing 读取批尾探测到的后续字节并处理(跨批粘贴的续片)。
func (s *inputSession) consumeTrailing() (bool, error) {
	next, err := s.readNext()
	if err != nil {
		return false, err
	}
	if next != '\r' && next != '\n' {
		s.pending = append(s.pending, next)
		return false, nil
	}
	// 跨批换行：一律作为粘贴内容
	s.sawNewline = true
	consumePending(s.ed, &s.pending, false)
	if next == '\r' && s.consumeCRLFContinuation() {
		return false, nil
	}
	insertRune(s.ed, '\n')
	return false, nil
}

// consumeCRLFContinuation 处理跨批 CRLF 的 \n 部分。
// 返回 true 表示 \n 已作为同一换行插入, 调用方不应再插换行。
func (s *inputSession) consumeCRLFContinuation() bool {
	if !s.probe(newlineProbe) {
		return false
	}
	n2, err := s.readNext()
	if err != nil {
		return false
	}
	if n2 == '\n' {
		insertRune(s.ed, '\n')
		return true
	}
	s.pending = append(s.pending, n2)
	return false
}
