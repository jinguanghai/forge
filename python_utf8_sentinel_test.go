package main

// python_utf8_sentinel_test.go — 结构哨兵 (20261002)
//
// 由来: 「自杀事故」修复中发现 4 个假红同源 —— Go 读 python 子进程的中文输出,
// 而 Windows 下 python 默认按 ANSI(GBK) 写 stdout, 剥离 PYTHONUTF8 的环境
// (计划任务 / 裸 go test / 用户双击) 读到的是 GBK 字节 -> 判据匹配不到 -> 假红。
// 逐点补 pythonUTF8Env 只是半修: 新写的调用点仍会重犯。
// 本哨兵钉住模式本身: 凡 exec.Command 调 python 且读取其输出的函数, 必须显式强制 UTF-8。
// 生产侧由 Forge.newCmd 单点注入(forge_env.go), 但判据仍扫全仓, 防止有人绕过 newCmd。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pyUTF8Exempt 显式豁免登记: 键 = "文件名:函数名"。
// 登记本身会刷引用数(观察者效应), 故只登记不得不豁免的, 且必须写明前置条件。
// 用「文件:函数名」而非行号: 行号随编辑漂移, 会让豁免静默失效或误放行。
var pyUTF8Exempt = map[string]string{
	"output_limit_test.go:TestLimitedWriter_RealSubprocessNotKilled": "输出 2MB 'y' 纯 ASCII, 不做中文解码; 被测对象是限流器不是编码",
}

// stripComment 去掉行内注释。
// 必须剥注释: 判据若扫到注释里写的关键字就当合规, 等于给违规开了后门
// (20261002 实测踩过: 修复注释里提到 PYTHONUTF8, 变异删除真正的 cmd.Env 行后哨兵仍绿)。
// 副作用: 字符串里的 "//"(如 URL) 会被截断 —— 本判据关心的关键字
// (exec.Command/python/PYTHONUTF8/CombinedOutput) 不会出现在 URL 中, 故可接受。
func stripComment(l string) string {
	if i := strings.Index(l, "//"); i >= 0 {
		return l[:i]
	}
	return l
}

// funcNameOf 从 "func Foo(" / "func (r T) Foo(" 行取函数名。
func funcNameOf(line string) string {
	s := strings.TrimPrefix(line, "func ")
	if i := strings.IndexByte(s, '('); i >= 0 {
		head := s[:i]
		if j := strings.LastIndexByte(head, ' '); j >= 0 { // 方法: 取最后一个空格后的部分
			return strings.TrimSpace(head[j+1:])
		}
		return strings.TrimSpace(head)
	}
	return strings.TrimSpace(s)
}

func TestPythonSubprocessForcesUTF8(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("扫描 .go 失败: %v (命中 %d)", err, len(files))
	}
	scanned, offenders := 0, 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		type blk struct {
			name  string
			start int
			body  []string
		}
		var cur *blk
		var blocks []blk
		flush := func() {
			if cur != nil {
				blocks = append(blocks, *cur)
				cur = nil
			}
		}
		for i, l := range lines {
			if strings.HasPrefix(l, "func ") {
				flush()
				cur = &blk{name: funcNameOf(l), start: i + 1}
				continue
			}
			if cur != nil {
				cur.body = append(cur.body, stripComment(l))
			}
		}
		flush()
		for _, b := range blocks {
			joined := strings.Join(b.body, "\n")
			if !strings.Contains(joined, "exec.Command(") {
				continue
			}
			lower := strings.ToLower(joined)
			if !strings.Contains(lower, "python") {
				continue // 非 python 子进程, 编码由各自工具负责
			}
			reads := strings.Contains(joined, "CombinedOutput") ||
				strings.Contains(joined, ".Output()") ||
				strings.Contains(joined, "StdoutPipe") ||
				strings.Contains(joined, "cmd.Stdout")
			if !reads {
				continue // 不读输出 -> 无解码风险
			}
			forced := strings.Contains(joined, "PYTHONUTF8") ||
				strings.Contains(joined, "PYTHONIOENCODING") ||
				strings.Contains(joined, "pythonUTF8Env")
			if forced {
				scanned++
				continue
			}
			key := f + ":" + b.name
			if _, ok := pyUTF8Exempt[key]; ok {
				scanned++
				continue
			}
			offenders++
			t.Errorf("%s:%d 函数 %s 调 python 并读取输出, 却未强制 UTF-8 —— "+
				"剥离 PYTHONUTF8 的环境(计划任务/裸 go test)下中文输出按 GBK 落地, 判据假红。\n"+
				"  修法: cmd.Env = pythonUTF8Env() (或 append(pythonUTF8Env(), ...))", f, b.start, b.name)
		}
	}
	if scanned == 0 {
		t.Fatal("扫描到 0 个合规的 python 调用点 —— 判据自身失效(扫描口径写错?)")
	}
	t.Logf("扫描 %d 个文件, 合规 python 调用点 %d 个, 违规 %d 个", len(files), scanned, offenders)
}
