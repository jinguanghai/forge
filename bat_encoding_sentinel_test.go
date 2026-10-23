package main

// bat_encoding_sentinel_test.go — .cmd/.bat 入口「代码页与文件编码」判据 (T59-②, 20261017)
//
// 为什么需要 (20261017 实测, 真控制台对照实验):
//
//	带中文的 .cmd 若声明 chcp 65001 而文件本身是 UTF-8, cmd.exe 读取批处理时会出现
//	字节偏移错位 —— 实测 rollback.cmd 输出 `'\' is not recognized`、`'从' is not
//	recognized`, 连 call :rlog 这样的后续行都被吃掉 (留痕失效)。用 git HEAD 的原版做
//	对照, 同样错乱 (只错位量不同) —— 属既有缺陷, 不是本次改动引入。
//	同一份文件改为 chcp 936 + GBK 后: 解析错误 0 行, 中文正常显示, 留痕完整。
//	机制: cmd.exe 逐行读取批处理时按字节偏移定位, chcp 切换叠加多字节字符使偏移漂移;
//	GBK 是中文 Windows 的 ANSI 代码页, 不切换即不错位 (本仓库 重启铸剑炉.bat 一直如此)。
//
// 判据: 含非 ASCII 的 .cmd/.bat 必须 ①显式声明 chcp 936 ②文件不是有效 UTF-8 (= GBK)。
// 纯 ASCII 脚本不受限 —— 没有多字节字符就没有错位风险 (quick_test.cmd 等仍可 65001)。
//
// 判据自身也要有判据: 反向向量 (纯 ASCII 判绿 / GBK+936 判绿 / 两种不匹配形态判红) 同跑。

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// batAsciiOnly 判定字节流是否纯 ASCII (无多字节字符 = 无错位风险)。
func batAsciiOnly(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

// batCodePageProblem 判定单个 .cmd/.bat 的代码页/编码一致性 (空串 = 合规)。
func batCodePageProblem(b []byte) string {
	if batAsciiOnly(b) {
		return ""
	}
	low := strings.ToLower(string(b))
	has65001 := strings.Contains(low, "chcp 65001")
	has936 := strings.Contains(low, "chcp 936")
	isUTF8 := utf8.Valid(b)
	switch {
	case isUTF8 && has65001:
		return "UTF-8 文件 + chcp 65001: 实测 cmd.exe 逐行读取会字节错位 (中文行被当命令, 后续 call 被吃掉) —— 请改 chcp 936 + GBK 编码"
	case isUTF8:
		return "含非 ASCII 但文件是 UTF-8 且未声明 chcp 936: 中文 Windows 下必然错位"
	case !has936:
		return "GBK 文件未显式声明 chcp 936: 依赖系统默认代码页, 换环境即乱码"
	}
	return ""
}

// TestBatEntriesCodePage 哨兵: 根目录全部 .cmd/.bat 的代码页声明必须与文件编码一致。
func TestBatEntriesCodePage(t *testing.T) {
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取仓库根失败 (fail-closed, 不放行): %v", err)
	}
	scanned, nonASCII := 0, 0
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		if !strings.HasSuffix(low, ".cmd") && !strings.HasSuffix(low, ".bat") {
			continue
		}
		b, err := os.ReadFile(e.Name())
		if err != nil {
			t.Errorf("读取 %s 失败: %v", e.Name(), err)
			continue
		}
		scanned++
		if !batAsciiOnly(b) {
			nonASCII++
		}
		if prob := batCodePageProblem(b); prob != "" {
			t.Errorf("%s: %s", e.Name(), prob)
		}
	}
	if scanned == 0 {
		t.Fatal("根目录未扫到任何 .cmd/.bat —— 判据失效 (扫描范围写错?)")
	}
	if nonASCII == 0 {
		t.Fatal("扫到的 .cmd/.bat 全是纯 ASCII —— 判据没覆盖到任何多字节脚本, 等于没测")
	}
	t.Logf("已扫 %d 个 .cmd/.bat, 其中含非 ASCII 的 %d 个 (须 GBK+chcp936)", scanned, nonASCII)
}

// TestBatCodePageProblemSelfCheck 元判据: 编码判据必须有鉴别力 (双向)。
func TestBatCodePageProblemSelfCheck(t *testing.T) {
	gbk936 := []byte("chcp 936 >nul\r\necho \xd6\xfd\xbd\xa3\xc2\xaf\r\n") // GBK「铸剑炉」
	utf8_65001 := []byte("chcp 65001 >nul\r\necho 铸剑炉\r\n")                // UTF-8 + 65001
	utf8_noChcp := []byte("echo 铸剑炉\r\n")                                  // UTF-8 无声明
	gbk_noChcp := []byte("echo \xd6\xfd\xbd\xa3\xc2\xaf\r\n")              // GBK 无声明
	ascii65001 := []byte("chcp 65001 >nul\r\ngo test -c -o b .\r\n")       // 纯 ASCII
	cases := []struct {
		name    string
		body    []byte
		wantRed bool
	}{
		{"gbk+936", gbk936, false},
		{"utf8+65001", utf8_65001, true},
		{"utf8-nocchp", utf8_noChcp, true},
		{"gbk-nocchp", gbk_noChcp, true},
		{"ascii+65001", ascii65001, false},
	}
	for _, c := range cases {
		prob := batCodePageProblem(c.body)
		if c.wantRed && prob == "" {
			t.Errorf("%s: 应报红却判绿 —— 判据无鉴别力", c.name)
		}
		if !c.wantRed && prob != "" {
			t.Errorf("%s: 应判绿却报红: %s", c.name, prob)
		}
	}
	// 纯 ASCII 判定自身也要双向: 单字节 0x80 以上必须被判为非 ASCII。
	if batAsciiOnly([]byte("abc")) != true {
		t.Error("纯 ASCII 被判成多字节")
	}
	if batAsciiOnly([]byte{0x61, 0x80}) != false {
		t.Error("含高位字节被判成纯 ASCII (判据会漏掉真多字节脚本)")
	}
}
