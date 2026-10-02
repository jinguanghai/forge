package main

// self_apply_locate_test.go — self gate 的 apply(replace/append) 与 locateSelfSource 定位。
// 20260927 自 self_deploy_test.go 拆出 (该文件 629 行超 F2 上限 500)。

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSelfGateErr_Shape(t *testing.T) {
	res := selfGateErr(time.Now(), "boom: %d", 7)
	if res.OK || res.Lang != "self" || res.Stage != "compile" || res.ExitCode != -1 {
		t.Fatalf("形状不符: %+v", res)
	}
	if res.Error != "boom: 7" {
		t.Fatalf("Error 应为格式化后文本, 实得 %q", res.Error)
	}
}

// TestSelfApplyReplace_FormatError 缺 new_text 段 → 失败, 且源码零改动。
func TestSelfApplyReplace_FormatError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	const orig = "package main\n\nconst x = 1\n"
	writeFake(t, src, orig)

	res, failed := selfApplyReplace(src, "replace:only-one-part", time.Now())
	if !failed {
		t.Fatal("格式错应失败")
	}
	if res.Stage != "compile" || res.ExitCode != -1 || res.Error == "" {
		t.Fatalf("失败结果形状不符: %+v", res)
	}
	if got := readFake(t, src); got != orig {
		t.Fatal("失败时源码不得被改动")
	}
}

// TestSelfApplyReplace_Miss 未命中 → 失败(报明原因)且源码不变。
func TestSelfApplyReplace_Miss(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	const orig = "package main\n"
	writeFake(t, src, orig)

	res, failed := selfApplyReplace(src, "replace:NOT_THERE:NEW", time.Now())
	if !failed || !strings.Contains(res.Error, "未命中") {
		t.Fatalf("未命中应失败并报明原因: %+v", res)
	}
	if got := readFake(t, src); got != orig {
		t.Fatal("未命中时源码不得被改动")
	}
}

// TestSelfApplyReplace_Hit 命中 → 只替换第一处, 其余原样。
func TestSelfApplyReplace_Hit(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	writeFake(t, src, "package main\n// A\n// A\n")

	res, failed := selfApplyReplace(src, "replace:// A:// B", time.Now())
	if failed {
		t.Fatalf("命中应成功: %+v", res)
	}
	if got := readFake(t, src); got != "package main\n// B\n// A\n" {
		t.Fatalf("应只替换第一处, 实得 %q", got)
	}
}

// TestSelfApplyAppend_NoMarker 无插入标记 → 失败且源码不变。
func TestSelfApplyAppend_NoMarker(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	const orig = "package main\n"
	writeFake(t, src, orig)

	res, failed := selfApplyAppend(src, "func extra() {}", time.Now())
	if !failed || !strings.Contains(res.Error, "未在forge.go中找到") {
		t.Fatalf("无标记应失败: %+v", res)
	}
	if got := readFake(t, src); got != orig {
		t.Fatal("失败时源码不得被改动")
	}
}

// TestSelfApplyAppend_EmptyCode 标记存在但 code 为空 → 失败, 源码不变。
func TestSelfApplyAppend_EmptyCode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	orig := "package main\n\nfunc ForgeToolSchema() json.RawMessage { return nil }\n"
	writeFake(t, src, orig)

	res, failed := selfApplyAppend(src, "   ", time.Now())
	if !failed || !strings.Contains(res.Error, "empty") {
		t.Fatalf("空 code 应失败: %+v", res)
	}
	if got := readFake(t, src); got != orig {
		t.Fatal("失败时源码不得被改动")
	}
}

// TestSelfApplyAppend_InsertsBeforeMarker 合法 code → 插到标记之前(顺序是契约)。
func TestSelfApplyAppend_InsertsBeforeMarker(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	writeFake(t, src, "package main\n\nfunc ForgeToolSchema() json.RawMessage { return nil }\n")

	const code = "func injected() int { return 42 }"
	if _, failed := selfApplyAppend(src, code, time.Now()); failed {
		t.Fatal("合法顶层声明应成功")
	}
	got := readFake(t, src)
	i, j := strings.Index(got, code), strings.Index(got, "func ForgeToolSchema()")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("新代码应插在标记之前, 实得:\n%s", got)
	}
}

// TestSelfApplyAppend_NotGoTopLevel 非顶层声明 → 失败且源码不变。
func TestSelfApplyAppend_NotGoTopLevel(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "forge.go")
	orig := "package main\n\nfunc ForgeToolSchema() json.RawMessage { return nil }\n"
	writeFake(t, src, orig)

	res, failed := selfApplyAppend(src, "这不是 Go 代码", time.Now())
	if !failed || !strings.Contains(res.Error, "valid Go top-level") {
		t.Fatalf("非顶层声明应失败: %+v", res)
	}
	if got := readFake(t, src); got != orig {
		t.Fatal("失败时源码不得被改动")
	}
}

// ────────────────────────────────────────────────────────────────
// locateSelfSource: self gate 的多文件定位
//
// 背景: 源码按职责拆分后, self gate 原先写死的 srcPath=forge.go 就再也
// 改不到搬走的函数 —— 等于拆自己的手术刀。定位键必须与后续实际改写用的
// 是同一个键, 且匹配不唯一时【拒绝】而非"取第一个"(取错文件 = 改错代码
// + 备份错文件 = 双错, 事后无法从备份恢复)。
// ────────────────────────────────────────────────────────────────

// mkLocateFixture 造一个最小源码目录(只含定位逻辑关心的 .go 文件)。
func mkLocateFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
	}
	return dir
}

// TestLocateSelfSource_TargetsSplitFile 钉住本改造的核心价值:
// 片段已搬到非 forge.go 的文件时, 定位必须跟随过去 ——
// 改造前这里会定位到 forge.go 然后报"replace 未命中"。
func TestLocateSelfSource_TargetsSplitFile(t *testing.T) {
	dir := mkLocateFixture(t, map[string]string{
		"forge.go": "package main\n\nfunc core() {}\n",
		"split.go": "package main\n\nfunc movedAway() {}\n",
	})
	f := &Forge{workDir: dir}
	got, err := f.locateSelfSource("replace:func movedAway():func movedAwayFixed()")
	if err != nil {
		t.Fatalf("唯一命中不应报错: %v", err)
	}
	if filepath.Base(got) != "split.go" {
		t.Errorf("定位 = %s, 期望 split.go(改造前会错定为 forge.go)", filepath.Base(got))
	}
}

// TestLocateSelfSource_AmbiguousRejected 是本改造最重要的保护:
// 匹配键同时出现在多个文件时必须拒绝, 绝不"取第一个"。
func TestLocateSelfSource_AmbiguousRejected(t *testing.T) {
	dir := mkLocateFixture(t, map[string]string{
		"a.go": "package main\n\nfunc same() {}\n",
		"b.go": "package main\n\nfunc same() {}\n",
	})
	f := &Forge{workDir: dir}
	got, err := f.locateSelfSource("replace:func same() {}:func same2() {}")
	if err == nil {
		t.Fatalf("歧义必须报错拒绝, 实际返回 %s —— 会改错文件", got)
	}
	if !strings.Contains(err.Error(), "歧义") {
		t.Errorf("错误信息未说明歧义: %v", err)
	}
}

func TestLocateSelfSource_NoMatchFallsBack(t *testing.T) {
	dir := mkLocateFixture(t, map[string]string{"forge.go": "package main\n"})
	f := &Forge{workDir: dir}
	got, err := f.locateSelfSource("replace:definitelyAbsentPhrase:replacement")
	if err != nil {
		t.Fatalf("零命中不应报错(交由 selfApplyReplace 报未命中): %v", err)
	}
	if filepath.Base(got) != "forge.go" {
		t.Errorf("零命中应回退 forge.go, 得到 %s", filepath.Base(got))
	}
}

func TestLocateSelfSource_MalformedReplaceFallsBack(t *testing.T) {
	dir := mkLocateFixture(t, map[string]string{"forge.go": "package main\n"})
	f := &Forge{workDir: dir}
	for _, action := range []string{"replace:noColonHere", "replace::newtext"} {
		got, err := f.locateSelfSource(action)
		if err != nil || filepath.Base(got) != "forge.go" {
			t.Errorf("%q 格式错应回退 forge.go, 得到 %s / %v", action, filepath.Base(got), err)
		}
	}
}

func TestLocateSelfSource_AppendUsesMarker(t *testing.T) {
	dir := mkLocateFixture(t, map[string]string{
		"forge.go": "package main\n\n" + selfAppendMarker + "\n}\n",
		"zzz.go":   "package main\n\nfunc zzz() {}\n",
	})
	f := &Forge{workDir: dir}
	got, err := f.locateSelfSource("append")
	if err != nil {
		t.Fatalf("append 定位失败: %v", err)
	}
	if filepath.Base(got) != "forge.go" {
		t.Errorf("append 应按 marker 定位 forge.go, 得到 %s", filepath.Base(got))
	}
}

func TestLocateSelfSource_SkipsTestFiles(t *testing.T) {
	// 片段只出现在 _test.go 里 -> 不得被当作目标(否则 self gate 会去改测试文件)
	dir := mkLocateFixture(t, map[string]string{
		"forge.go":     "package main\n",
		"only_test.go": "package main\n\n// uniquephraseXYZ\n",
	})
	f := &Forge{workDir: dir}
	got, err := f.locateSelfSource("replace:uniquephraseXYZ:x")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if filepath.Base(got) != "forge.go" {
		t.Errorf("_test.go 不应参与定位, 得到 %s", filepath.Base(got))
	}
}

// TestLocateSelfSource_RealRepo 用真仓库验证两条真实动作的定位结果。
func TestLocateSelfSource_RealRepo(t *testing.T) {
	f := &Forge{workDir: "."}
	got, err := f.locateSelfSource("append")
	if err != nil {
		t.Fatalf("真仓库 append 定位失败: %v", err)
	}
	if filepath.Base(got) != "forge.go" {
		t.Errorf("append 定位 = %s, 期望 forge.go", filepath.Base(got))
	}
	// 已搬离 forge.go 的函数签名做 replace 键 -> 必须定位到它现在所在的文件
	got2, err := f.locateSelfSource("replace:func (f *Forge) selfHostedSelf(:func (f *Forge) selfHostedSelfX(")
	if err != nil {
		t.Fatalf("真仓库 replace 定位失败: %v", err)
	}
	if filepath.Base(got2) != "forge_self.go" {
		t.Errorf("replace 定位 = %s, 期望 forge_self.go(selfHostedSelf 已搬到该文件)", filepath.Base(got2))
	}
	// 已搬离 forge.go 的函数: 定位必须跟随到新文件(证明能改到拆分后的文件)
	got3, err := f.locateSelfSource("replace:func tabComplete(line string) string:func tabCompleteX(line string) string")
	if err != nil {
		t.Fatalf("跨文件 replace 定位失败: %v", err)
	}
	if filepath.Base(got3) != "readline_editor_windows.go" {
		t.Errorf("redraw 定位 = %s, 期望 readline_editor_windows.go", filepath.Base(got3))
	}
}

// TestSelfGateWired_LocateCallSite 钉住 self gate 的定位必须走 locateSelfSource,
// 而不是写死 forge.go。写死路径的失效形态是 replace 未命中 —— 看起来像用户
// 传错了 old_text, 不会有人怀疑到定位逻辑本身, 属静默失效。
func TestSelfGateWired_LocateCallSite(t *testing.T) {
	// selfHostedSelf 已随拆分搬离 forge.go, 因此必须扫全包找它 ——
	// 写死单文件的扫描会在拆分后静默失效(实测踩过 7 次, 同一病灶)。
	body := extractFuncBody(prodGoSources(t), "func (f *Forge) selfHostedSelf(")
	if body == "" {
		t.Fatal("全包中找不到 selfHostedSelf 函数体")
	}
	if !strings.Contains(body, "locateSelfSource(") {
		t.Error("selfHostedSelf 未调用 locateSelfSource —— 定位可能又写死了 forge.go(已搬离的函数将改不动)")
	}
}

// TestSelfGateWired_MarkerAndTargetSameFile 钉住 selfAppendMarker 与
// ForgeToolSchema 必须【同文件】。
//
// locateSelfSource 用 marker 字符串在全包搜文件; 若常量住在别的文件, 就会有两个
// 文件含该子串 -> 定位歧义 -> self gate 的 append 动作被拒绝执行。
// 这是拆分 forge.go 时真实踩到的坑(常量一度被分到 forge_self.go)。
func TestSelfGateWired_MarkerAndTargetSameFile(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	markerFile, targetFile := "", ""
	targetRe := regexp.MustCompile(`(?m)^func ForgeToolSchema\(\) json\.RawMessage \{`)
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(name)
		if rerr != nil {
			continue
		}
		s := string(b)
		if strings.Contains(s, "const selfAppendMarker =") {
			markerFile = name
		}
		if targetRe.MatchString(s) {
			targetFile = name
		}
	}
	if markerFile == "" {
		t.Fatal("全包找不到 selfAppendMarker 定义")
	}
	if targetFile == "" {
		t.Fatal("全包找不到 ForgeToolSchema 顶层声明")
	}
	if markerFile != targetFile {
		t.Errorf("marker 常量在 %s, 目标声明在 %s —— 两文件都含 marker 字符串, append 定位会歧义",
			markerFile, targetFile)
	}
}
