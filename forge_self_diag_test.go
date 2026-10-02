package main

// forge_self_diag_test.go — P1(20260930) self gate 契约反馈的哨兵。
//
// 靶心(六西格玛实测): self gate 契约误用 5 次/14 天 (历史 7/29 = 24.1%) ——
// 报错不给样例, 模型只能盲改。本文件钉住: 契约类失败必带「本次定位 + 合法样例」,
// 且样例走 Diagnostics 能被渲染层打给模型; 非契约类失败(IO 错)不挂样例(防噪音)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func assertHasExample(t *testing.T, diag, extraWant string) {
	t.Helper()
	for _, want := range []string{"replace:旧片段:新片段", "deploy", "input"} {
		if !strings.Contains(diag, want) {
			t.Errorf("诊断缺合法样例片段 %q:\n%s", want, diag)
		}
	}
	if extraWant != "" && !strings.Contains(diag, extraWant) {
		t.Errorf("诊断缺本次定位 %q:\n%s", extraWant, diag)
	}
}

// ── P1: self gate 契约报错附合法输入样例 ──────────────────────────────

func TestP1_ContractExampleContent(t *testing.T) {
	ex := selfContractExample()
	for _, want := range []string{"input", "replace:", "deploy", "build", "func", "逐字符"} {
		if !strings.Contains(ex, want) {
			t.Errorf("合法样例缺 %q:\n%s", want, ex)
		}
	}
}

// 靶心形态 1: replace 只写了一段(漏第二个冒号)
func TestP1_ReplaceFormatErrHasExample(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "forge.go")
	if err := os.WriteFile(srcPath, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, failed := selfApplyReplace(srcPath, "replace:onlyone", time.Now())
	if !failed {
		t.Fatalf("格式错必须失败: %+v", res)
	}
	assertHasExample(t, res.Diagnostics, "两个冒号")
	out := (&Forge{}).formatResult(res)
	if !strings.Contains(out, "replace:旧片段:新片段") {
		t.Errorf("渲染层丢失样例:\n%s", out)
	}
}

// 靶心形态 2: replace 未命中(旧片段与源文件不逐字符一致)
func TestP1_ReplaceMissHasExample(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "forge.go")
	if err := os.WriteFile(srcPath, []byte("package main\n\nfunc a() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	res, failed := selfApplyReplace(srcPath, "replace:func b():func c()", time.Now())
	if !failed {
		t.Fatalf("未命中必须失败: %+v", res)
	}
	assertHasExample(t, res.Diagnostics, "逐字符一致")
}

// 靶心形态 3: code 不是 Go 顶层声明(模型写了语句)
func TestP1_AppendBadCodeHasExample(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "forge.go")
	body := "package main\n\n" + selfAppendMarker + "\n}\n"
	if err := os.WriteFile(srcPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	res, failed := selfApplyAppend(srcPath, "x := 1", time.Now())
	if !failed {
		t.Fatalf("非顶层声明必须失败: %+v", res)
	}
	assertHasExample(t, res.Diagnostics, "顶层声明")
}

// 靶心形态 4: code 为空(模型只给了 input, 忘了 code)
func TestP1_AppendEmptyCodeHasExample(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "forge.go")
	body := "package main\n\n" + selfAppendMarker + "\n}\n"
	if err := os.WriteFile(srcPath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	res, failed := selfApplyAppend(srcPath, "   ", time.Now())
	if !failed {
		t.Fatalf("空 code 必须失败: %+v", res)
	}
	assertHasExample(t, res.Diagnostics, "code 为空")
}

// 契约误用的真实形态: 模型把 action 包成 JSON 塞进 input
func TestP1_JSONActionMisuseDiag(t *testing.T) {
	d := selfInputDiag(`{"action":"deploy"}`, "")
	if !strings.Contains(d, "JSON 对象") || !strings.Contains(d, "写 deploy") {
		t.Errorf("JSON 契约误用未被识别:\n%s", d)
	}
}

// ③ 防噪音: 非契约类失败(读文件失败)不得挂样例
func TestP1_NonContractErrNoExample(t *testing.T) {
	res, failed := selfApplyAppend(filepath.Join(t.TempDir(), "nope.go"), "func x() {}", time.Now())
	if !failed {
		t.Fatalf("文件不存在必须失败: %+v", res)
	}
	if res.Diagnostics != "" {
		t.Errorf("IO 失败不该挂契约样例(噪音淹没真因): %s", res.Diagnostics)
	}
}
