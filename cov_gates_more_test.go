package main

// cov_gates_more_test.go — gate 执行路径/缓存/归档解压 分支补测
// 安全边界: 不触碰 self gate (需人工审批), 不下载外部工具链, 全部走临时目录。

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newCovForge(t *testing.T) *Forge {
	t.Helper()
	_, cfg, _ := newHandleCmdAgent(t)
	return NewForge(t.TempDir(), cfg)
}

func TestCovGates_Meta(t *testing.T) {
	f := newCovForge(t)
	if f.Ping() == "" {
		t.Error("Ping 为空")
	}
	if f.Stats() == nil {
		t.Error("Stats 为 nil")
	}
	if len(ForgeToolSchema()) == 0 {
		t.Error("ForgeToolSchema 为空")
	}
	if p, err := f.findGoCommand(); err != nil {
		t.Logf("findGoCommand 失败(可接受): %v", err)
	} else if p == "" {
		t.Error("findGoCommand 返回空路径")
	}
}

func TestCovGates_BuildLangs(t *testing.T) {
	if testing.Short() {
		t.Skip("short: 跳过 gate 端到端测试(起真实 gate 子进程)")
	}
	f := newCovForge(t)
	cases := []struct{ lang, code string }{
		{"python", "print('COV_OK')"},
		{"math", "1+1"},
		{"sh", "echo COV_SH"},
		{"node", "console.log('COV_NODE')"},
		{"regex", `{"type":"match","pattern":"^a$","positive":["a"],"negative":["b"]}`},
		{"logic", "x > 0"},
		{"chain", `{"stages":[{"gate":"math","input":{"code":"1+1"}}]}`},
		{"fortran", "print *, 1"},
		{"go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"COV_GO\") }"},
	}
	for _, c := range cases {
		out, res, err := f.Build(c.code, c.lang, "")
		if res == nil {
			t.Errorf("lang=%s 返回 nil 结果 (err=%v out=%q)", c.lang, err, out)
			continue
		}
		t.Logf("lang=%-8s ok=%-5v stage=%-8s err=%v", c.lang, res.OK, res.Stage, err)
	}
}

func TestCovGates_CacheAndSkip(t *testing.T) {
	f := newCovForge(t)
	r1 := f.forgeGate("print('CACHE_COV')", "python", "")
	r2 := f.forgeGate("print('CACHE_COV')", "python", "")
	if r1.OK != r2.OK {
		t.Errorf("缓存命中前后结果不一致: %+v vs %+v", r1.OK, r2.OK)
	}
	r3 := f.forgeGateSkipCache("print('CACHE_COV')", "python", "", true)
	if r3.OK != r1.OK {
		t.Errorf("skipCache 结果不一致")
	}
	_ = f.forgeGate("", "python", "")
	_ = f.forgeGate("   \n  ", "python", "")
}

func TestCovGates_DisabledAndUnknown(t *testing.T) {
	_, cfg, _ := newHandleCmdAgent(t)
	cfg.GatesEnabled = []string{"python"}
	f := NewForge(t.TempDir(), cfg)
	if r := f.forgeGate("1+1", "math", ""); r.OK {
		t.Error("gate 未启用时 math 不应执行成功")
	}
	if r := f.forgeGate("print(1)", "python", ""); r.Error == "" && !r.OK {
		t.Logf("python 未启用结果: %+v", r)
	}
}

func TestCovGates_ArchiveExtract(t *testing.T) {
	wd := t.TempDir()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("hello archive")
	if err := tw.WriteHeader(&tar.Header{Name: "a.txt", Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	tgz := filepath.Join(wd, "a.tar.gz")
	if err := os.WriteFile(tgz, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if err := forgeUntarGz(tgz, filepath.Join(wd, "out_tar")); err != nil {
		t.Errorf("forgeUntarGz: %v", err)
	} else if b, err := os.ReadFile(filepath.Join(wd, "out_tar", "a.txt")); err != nil || string(b) != "hello archive" {
		t.Errorf("tar 解压内容不符: %q %v", b, err)
	}

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	fw, err := zw.Create("b.txt")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("zip content"))
	zw.Close()
	zp := filepath.Join(wd, "b.zip")
	if err := os.WriteFile(zp, zbuf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	if err := forgeUnzip(zp, filepath.Join(wd, "out_zip")); err != nil {
		t.Errorf("forgeUnzip: %v", err)
	} else if b, err := os.ReadFile(filepath.Join(wd, "out_zip", "b.txt")); err != nil || string(b) != "zip content" {
		t.Errorf("zip 解压内容不符: %q %v", b, err)
	}

	_ = forgeUntarGz(filepath.Join(wd, "nope.tar.gz"), filepath.Join(wd, "x1"))
	_ = forgeUnzip(filepath.Join(wd, "nope.zip"), filepath.Join(wd, "x2"))
	bad := filepath.Join(wd, "bad.tar.gz")
	os.WriteFile(bad, []byte("not gzip"), 0644)
	_ = forgeUntarGz(bad, filepath.Join(wd, "x3"))
	badz := filepath.Join(wd, "bad.zip")
	os.WriteFile(badz, []byte("not zip"), 0644)
	_ = forgeUnzip(badz, filepath.Join(wd, "x4"))
}

func TestCovGates_FileGate(t *testing.T) {
	f := newCovForge(t)
	for _, lang := range []string{"go", "node", "python"} {
		cd, ok := 铸剑炉_COMPILERS[lang]
		if !ok {
			t.Logf("编译器表缺少 %s", lang)
			continue
		}
		r := f.forgeGateFile("", lang, cd, "", time.Now())
		t.Logf("forgeGateFile(%s) ok=%v stage=%s err=%s", lang, r.OK, r.Stage, r.Error)
	}
}
