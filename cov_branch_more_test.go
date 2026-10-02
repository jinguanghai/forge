package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── 分支/边界覆盖补充 (cov_branch_more) ────────────────────────
//
// 目的: 覆盖低频分支 (错误路径 / 分片截断 / 空输入), 把语句覆盖率推过 80%。
// 原则: 只测确定性逻辑与文件级隔离逻辑 —— 不引入真实进程 / 网络 / 音频设备。
// 隔离: 一律 t.TempDir(); 涉及包级全局态 (cacheStatPath) 的用 t.Cleanup 还原。

// decodeJSONString: 转义全表 + 流式分片截断 + 非法输入收尾 (含旧死循环修正点)
func TestDecodeJSONString_EscapesAndTruncation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		i    int
		want string
	}{
		{"普通字符遇闭合引号", `abc"`, 0, "abc"},
		{"转义n", `a\nb"`, 0, "a\nb"},
		{"转义t", `a\tb"`, 0, "a\tb"},
		{"转义r", `a\rb"`, 0, "a\rb"},
		{"转义反斜杠", `a\\b"`, 0, `a\b`},
		{"转义引号", `a\"b"`, 0, `a"b`},
		{"转义斜杠", `a\/b"`, 0, "a/b"},
		{"unicode合法", `\u0041"`, 0, "A"},
		{"unicode不完整就此收尾", `\u12"`, 0, ""},
		{"unicode非法hex就此收尾", `\uZZZZ"`, 0, ""},
		{"未知转义就此收尾", `a\x`, 0, "a"},
		{"末尾单个反斜杠", `ab\`, 0, "ab"},
		{"无闭合引号读完即止", `abc`, 0, "abc"},
		{"空串", ``, 0, ""},
		{"起始偏移", `xy"`, 1, "y"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decodeJSONString(c.in, c.i); got != c.want {
				t.Errorf("decodeJSONString(%q, %d) = %q, want %q", c.in, c.i, got, c.want)
			}
		})
	}
}

// discoverImagesInDir: 不存在 / 非目录 / 空目录 / 混合目录(只收 magic 通过的真图)
func TestDiscoverImagesInDir_Branches(t *testing.T) {
	base := t.TempDir()

	if _, err := discoverImagesInDir(filepath.Join(base, "nope")); err == nil {
		t.Error("不存在的目录应报错")
	}

	filePath := filepath.Join(base, "notadir.txt")
	if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverImagesInDir(filePath); err == nil {
		t.Error("非目录路径应报错")
	}

	empty := filepath.Join(base, "empty")
	if err := os.Mkdir(empty, 0755); err != nil {
		t.Fatal(err)
	}
	if parts, err := discoverImagesInDir(empty); err != nil || len(parts) != 0 {
		t.Errorf("空目录应返回 0 张且无错误, got parts=%d err=%v", len(parts), err)
	}

	mixed := filepath.Join(base, "mixed")
	if err := os.Mkdir(mixed, 0755); err != nil {
		t.Fatal(err)
	}
	const onePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	png, err := base64.StdEncoding.DecodeString(onePNG)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixed, "a.png"), png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixed, "b.txt"), []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(mixed, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	parts, err := discoverImagesInDir(mixed)
	if err != nil {
		t.Fatalf("混合目录不应报错: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("应只收 1 张真图 (magic 校验 + 跳子目录), got %d", len(parts))
	}
	if !strings.HasPrefix(parts[0].URL, "data:image/png;base64,") {
		t.Errorf("MIME 应为 png, got %q", parts[0].URL[:min(40, len(parts[0].URL))])
	}
}

// compactFoldedIndex: 无 memory.json / 无 folded_memory / 三种状态标记 + 摘要截断
func TestCompactFoldedIndex_Branches(t *testing.T) {
	wd := t.TempDir()
	if got := compactFoldedIndex(wd); got != "" {
		t.Errorf("无 memory.json 应返回空串, got %q", got)
	}

	memPath := filepath.Join(wd, "memory.json")
	if err := os.WriteFile(memPath, []byte(`{"identity":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := compactFoldedIndex(wd); got != "" {
		t.Errorf("无 folded_memory 字段应返回空串, got %q", got)
	}

	long := strings.Repeat("长", 60)
	mem := `{"folded_memory":{"items":[` +
		`{"id":"1","name":"A","status":"doing","summary":"短"},` +
		`{"id":"2","name":"B","status":"shelved","summary":"` + long + `"},` +
		`{"id":"3","name":"C","status":"done","summary":"x"}]}}`
	if err := os.WriteFile(memPath, []byte(mem), 0644); err != nil {
		t.Fatal(err)
	}
	got := compactFoldedIndex(wd)
	if got == "" {
		t.Fatal("有 folded 项时应非空")
	}
	for _, want := range []string{"🔵", "⏸", "✅", "A — 短", "C — x"} {
		if !strings.Contains(got, want) {
			t.Errorf("输出缺 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, long) {
		t.Error("超长摘要应被 truncateCN 截断")
	}
}

// printQualityAlerts / readQualityAlerts: 无文件 / 有告警(high+warn+info+坏行) / 同 key 去重
func TestPrintQualityAlerts_Branches(t *testing.T) {
	wd := t.TempDir()
	printQualityAlerts(wd) // 无文件 → "无告警"分支, 且不得 panic

	dir := filepath.Join(wd, ".forge")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Format(time.RFC3339)
	body := `{"ts":"` + now + `","sev":"high","metric":"m1","key":"k1","msg":"高危A"}` + "\n" +
		`{"ts":"` + now + `","sev":"warn","metric":"m2","key":"k2","msg":"告警B"}` + "\n" +
		`{"ts":"` + now + `","sev":"info","metric":"m3","key":"k3","msg":"信息C"}` + "\n" +
		"not json line\n" + "\n"
	if err := os.WriteFile(filepath.Join(dir, "alerts.jsonl"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	printQualityAlerts(wd) // 有告警 → high 红 / 其它黄 分支

	got := readQualityAlerts(wd, 7*24*time.Hour)
	if len(got) != 2 {
		t.Fatalf("应保留 high+warn 两条 (info 与坏行跳过), got %d", len(got))
	}

	dup := `{"ts":"` + now + `","sev":"warn","metric":"m9","key":"k9","msg":"旧"}` + "\n" +
		`{"ts":"` + now + `","sev":"high","metric":"m9","key":"k9","msg":"新"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "alerts.jsonl"), []byte(dup), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readQualityAlerts(wd, 7*24*time.Hour); len(got) != 1 {
		t.Fatalf("同 metric|key 应去重为 1 条, got %d", len(got))
	}
}

// selfBackupSource: 源不可读 → 拒绝改动; 源可读 → 备份逐字节一致
func TestSelfBackupSource_Branches(t *testing.T) {
	wd := t.TempDir()
	cfg := DefaultConfig()
	cfg.WorkDir = wd
	f := NewForge(wd, cfg)
	defer f.Shutdown()

	_, res, failed := f.selfBackupSource(filepath.Join(wd, "missing.go"), time.Now())
	if !failed {
		t.Error("源文件不可读应 failed=true (拒绝无备份改动)")
	}
	if res.OK || res.ExitCode != -1 {
		t.Errorf("失败结果应 OK=false/ExitCode=-1, got OK=%v exit=%d", res.OK, res.ExitCode)
	}

	src := filepath.Join(wd, "forge.go")
	content := []byte("package main\n\n// self backup 探针\n")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}
	bp, res2, failed2 := f.selfBackupSource(src, time.Now())
	if failed2 {
		t.Fatalf("正常源文件应备份成功, res=%+v", res2)
	}
	if bp == "" {
		t.Fatal("成功时 backupPath 不应为空")
	}
	// 2S 归位(20260928): 备份必须落 .forge/backups/, 不得再写源文件旁(根目录)。
	// 这条断言是归位的哨兵 —— 少了它, 路径被改回根目录也测不出来。
	if want := selfBackupDir(wd); filepath.Dir(bp) != want {
		t.Errorf("备份应落 %s, 实得 %s", want, filepath.Dir(bp))
	}
	got, err := os.ReadFile(bp)
	if err != nil {
		t.Fatalf("备份文件不可读: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("备份内容与源不一致:\n got %q\nwant %q", got, content)
	}
}

// auditFilePath: 有效目录 / 空目录+env / 空目录无env / 点目录
func TestAuditFilePath_Branches(t *testing.T) {
	// 测试进程里 FORGE_AUDIT_PATH 无条件优先于 WorkDir (隔离契约, 20260930),
	// 故先清空 env 才能测生产分支的优先级语义。
	t.Setenv("FORGE_AUDIT_PATH", "")
	dir := t.TempDir()
	if got, want := auditFilePath(dir), filepath.Join(dir, "gate_audit.jsonl"); got != want {
		t.Errorf("有效目录: got %q want %q", got, want)
	}

	envPath := filepath.Join(t.TempDir(), "isolated.jsonl")
	t.Setenv("FORGE_AUDIT_PATH", envPath)
	if got := auditFilePath(""); got != envPath {
		t.Errorf("空目录+env: got %q want %q", got, envPath)
	}
	if got := auditFilePath("."); got != envPath {
		t.Errorf("点目录+env: got %q want %q", got, envPath)
	}
	// 判据收窄 (20260930): 只有"生产根"强制隔离, 非生产根的有效目录仍优先 ——
	// 否则用 t.TempDir() 写自己审计的合法测试会被打断。
	if got := auditFilePath(dir); got != filepath.Join(dir, "gate_audit.jsonl") {
		t.Errorf("非生产根有效目录应优先: got %q want %q", got, filepath.Join(dir, "gate_audit.jsonl"))
	}
	if cwd, err := os.Getwd(); err == nil {
		if got := auditFilePath(cwd); got != envPath {
			t.Errorf("生产根应强制隔离: got %q want %q", got, envPath)
		}
	}

	t.Setenv("FORGE_AUDIT_PATH", "")
	if got, want := auditFilePath(""), filepath.Join(".", "gate_audit.jsonl"); got != want {
		t.Errorf("空目录无env: got %q want %q", got, want)
	}
	if got, want := auditFilePath("."), filepath.Join(".", "gate_audit.jsonl"); got != want {
		t.Errorf("点目录无env: got %q want %q", got, want)
	}
}

// cacheStatPathForWrite: 显式设置 / 未设置+env / 未设置无env / 默认名视为未设置
func TestCacheStatPathForWrite_Branches(t *testing.T) {
	old := cacheStatPath
	t.Cleanup(func() { cacheStatPath = old })

	wd := t.TempDir()
	setCacheStatPath(wd)
	if got, want := cacheStatPathForWrite(), filepath.Join(wd, defaultCacheStatName); got != want {
		t.Errorf("显式设置: got %q want %q", got, want)
	}

	cacheStatPath = ""
	envPath := filepath.Join(wd, "isolated.jsonl")
	t.Setenv("FORGE_CACHE_STATS_PATH", envPath)
	if got := cacheStatPathForWrite(); got != envPath {
		t.Errorf("未设置+env: got %q want %q", got, envPath)
	}

	t.Setenv("FORGE_CACHE_STATS_PATH", "")
	if got := cacheStatPathForWrite(); got != defaultCacheStatName {
		t.Errorf("未设置无env: got %q want %q", got, defaultCacheStatName)
	}

	cacheStatPath = defaultCacheStatName
	t.Setenv("FORGE_CACHE_STATS_PATH", envPath)
	if got := cacheStatPathForWrite(); got != envPath {
		t.Errorf("默认名(视为未设置)+env: got %q want %q", got, envPath)
	}
}
