package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// credentialNameRe 判定「哪些配置项算凭据」。
var credentialNameRe = regexp.MustCompile(`(?i)(key|token|secret|password)`)

// activeCredentialValues 从 env 文件解析出非空的凭据项 (值长度 >= 16)。
func activeCredentialValues(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if credentialNameRe.MatchString(k) && len(v) >= 16 {
			out[k] = v
		}
	}
	return out
}

// scanCredentialCopies 扫描 root 下所有文本文件, 返回含 secrets 任一值的文件清单。
// 根 .env 自身是合法存储位置, 排除; 二进制与 >2MB 文件跳过。
//
// 判据刻意「按内容」而非「按文件名」: 真实形态是值被写在注释行里
// (# DEEPSEEK_API_KEY = <另一个 key 的值>), 只认 .env* 文件名的判据会漏。
func scanCredentialCopies(root string, secrets map[string]string) []string {
	var hits []string
	skipDirs := map[string]bool{".git": true, "node_modules": true, "deno": true}
	rootEnv, _ := filepath.Abs(filepath.Join(root, ".env"))
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 单个条目读不到不该让整条判据失效
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if abs, _ := filepath.Abs(p); abs == rootEnv {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || info.Size() > 2<<20 {
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil || bytes.IndexByte(raw, 0) >= 0 {
			return nil
		}
		s := string(raw)
		for name, val := range secrets {
			if strings.Contains(s, val) {
				hits = append(hits, p+" (含 "+name+" 副本)")
				break
			}
		}
		return nil
	})
	return hits
}

// TestNoActiveCredentialCopies 哨兵: 除根 .env 外, 工作区内任何文件都不得含
// 当前活跃凭据的值。
//
// 动机 (20261004 实测): _archive 下两份 .env.bak_*(20260911 / 20260923) 含与
// 生产逐字节相同的 DEEPSEEK_API_KEY / MINIMAX_API_KEY, 而四套守卫全绿 ——
// 双重盲区: (1) secret_scan.py 按 basename 前缀 ".env" 跳过 (为根 .env 设计,
// 误伤 .env.bak_*); (2) _archive 在 SKIP_DIR_NAMES 里。守卫管「已知模式的已知
// 位置」, 而「凭据的副本」是跨位置属性 —— 需要独立判据。
//
// 有效性证据: 处置前本判据报红 (命中上述 2 处), 脱敏后转绿 (同一代码, 见
// t20261004_124600 / t20261004_124620 两次 task 日志)。
//
// 语义: 只拦「当前活跃凭据」的副本; 已轮换的旧 key 副本风险低, 不报。
func TestNoActiveCredentialCopies(t *testing.T) {
	secrets := activeCredentialValues(".env")
	if len(secrets) == 0 {
		t.Fatal("根 .env 未解析出任何非空凭据, 判据失效 (可能 .env 缺失或格式已变)")
	}
	if hits := scanCredentialCopies(".", secrets); len(hits) > 0 {
		t.Errorf("发现活跃凭据副本 %d 处 —— 除根 .env 外, 工作区任何文件都不得含活跃凭据值:\n  %s",
			len(hits), strings.Join(hits, "\n  "))
	}
}

// TestScanCredentialCopies_DetectsInjectedCopy 是判据自身的判据 (公理三: 判据
// 自身也要有判据)。用合成场景钉住三条: (a) 干净工作区不误报; (b) 注释行里藏值
// 的副本必被检出 (真实形态); (c) 根 .env 自身不算副本。
func TestScanCredentialCopies_DetectsInjectedCopy(t *testing.T) {
	dir := t.TempDir()
	const secret = "sk-test-1234567890abcdef"
	if err := os.WriteFile(filepath.Join(dir, ".env"),
		[]byte("DEEPSEEK_API_KEY="+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := activeCredentialValues(filepath.Join(dir, ".env"))
	if len(secrets) != 1 {
		t.Fatalf("解析根 .env 失败: %v", secrets)
	}

	// (a) 干净文件不误报
	if err := os.WriteFile(filepath.Join(dir, "clean.txt"), []byte("nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hits := scanCredentialCopies(dir, secrets); len(hits) != 0 {
		t.Errorf("干净工作区误报: %v", hits)
	}

	// (b) 注释行藏值的副本 —— 必须检出 (真实形态, 行级脱敏正是漏在这里)
	sub := filepath.Join(dir, "_archive", "bak")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".env.bak"),
		[]byte("# DEEPSEEK_API_KEY = "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := scanCredentialCopies(dir, secrets)
	if len(hits) != 1 {
		t.Fatalf("注释行藏值的副本未被检出 (判据漏检): %v", hits)
	}

	// (c) 根 .env 自身不得被算作副本
	for _, h := range hits {
		if filepath.Base(strings.SplitN(h, " ", 2)[0]) == ".env" {
			t.Errorf("根 .env 被误判为副本: %v", hits)
		}
	}
}
