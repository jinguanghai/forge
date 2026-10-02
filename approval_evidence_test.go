// approval_evidence_test.go — 审批取证通道的判据 (B 路: 拦住靠不住, 审得清兜底)。
//
// 覆盖三层: 纯函数(指纹/轮转) · 落盘契约(内容逐字节/索引可回读/不覆盖) · 接线(端到端 + AST 哨兵)。
// 隔离: workDir 一律 t.TempDir(); stdin 用临时文件; stderr 落临时文件; 事件日志指向临时路径。
// 绝不触碰生产 .forge/approvals —— 判据自己污染证据库比没有判据更坏。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApprovalFingerprint(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"普通代码", `os.remove("x.txt")`},
		{"空代码", ""},
		{"多字节", "删除 memory.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := approvalFingerprint(c.code)
			if len(got) != approvalFingerprintLen {
				t.Fatalf("指纹长度应为 %d, 实际 %d (%q)", approvalFingerprintLen, len(got), got)
			}
			if got == "" {
				t.Fatal("指纹不得为空串 —— 调用方无法区分「没算」与「空代码」")
			}
			if again := approvalFingerprint(c.code); again != got {
				t.Fatalf("同代码两次指纹不一致: %q vs %q", got, again)
			}
		})
	}
	if approvalFingerprint("a") == approvalFingerprint("b") {
		t.Fatal("不同代码指纹不应相同")
	}
	// 已知向量: sha256("") = e3b0c442... —— 钉住算法与截断口径, 防「换了哈希函数也全绿」
	if got, want := approvalFingerprint(""), "e3b0c44298fc"; got != want {
		t.Fatalf("空串指纹应为 %q, 实际 %q", want, got)
	}
}

func TestStageApprovalEvidence_WritesExactCode(t *testing.T) {
	work := t.TempDir()
	code := "import os\nos.system('rm -rf /tmp/x')\n"
	fp, path, err := stageApprovalEvidence(work, code)
	if err != nil {
		t.Fatalf("落盘失败: %v", err)
	}
	if want := filepath.Join(work, ".forge", "approvals"); filepath.Dir(path) != want {
		t.Fatalf("落盘目录应为 %q, 实际 %q", want, filepath.Dir(path))
	}
	if !strings.Contains(filepath.Base(path), fp) {
		t.Fatalf("文件名应含指纹 %q, 实际 %q", fp, filepath.Base(path))
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取取证文件失败: %v", err)
	}
	if string(b) != code {
		t.Fatalf("取证内容必须逐字节等于原代码:\n got %q\nwant %q", string(b), code)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("残留临时文件 %q —— tmp+rename 未清理", e.Name())
		}
	}
}

func TestStageApprovalEvidence_WorkDirEmpty(t *testing.T) {
	fp, path, err := stageApprovalEvidence("", "x")
	if err == nil {
		t.Fatal("workDir 为空必须返回错误, 不得静默成功")
	}
	if path != "" {
		t.Fatalf("失败时路径应为空, 实际 %q", path)
	}
	if fp == "" {
		t.Fatal("失败时仍应返回指纹, 便于调用方留痕")
	}
}

func TestStageApprovalEvidence_NoOverwrite(t *testing.T) {
	work := t.TempDir()
	code := "same code"
	fp1, p1, err := stageApprovalEvidence(work, code)
	if err != nil {
		t.Fatalf("首次落盘失败: %v", err)
	}
	fp2, p2, err := stageApprovalEvidence(work, code)
	if err != nil {
		t.Fatalf("二次落盘失败: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("同代码指纹应相同: %q vs %q", fp1, fp2)
	}
	if p1 == p2 {
		t.Fatalf("同秒同指纹二次落盘必须另存, 不得覆盖已有取证 (%q)", p1)
	}
	for _, p := range []string{p1, p2} {
		if b, err := os.ReadFile(p); err != nil || string(b) != code {
			t.Fatalf("取证文件 %q 内容异常: %v / %q", p, err, string(b))
		}
	}
}

func TestRecordApprovalEvidence_IndexReadable(t *testing.T) {
	work := t.TempDir()
	code := `os.remove("memory.json")`
	fp, path, err := stageApprovalEvidence(work, code)
	if err != nil {
		t.Fatalf("落盘失败: %v", err)
	}
	if recErr, _ := recordApprovalEvidence(work, "保护目标", "memory.json", "deny", modeHuman, code, path); recErr != nil {
		t.Fatalf("索引写入失败: %v", recErr)
	}
	b, err := os.ReadFile(filepath.Join(approvalEvidenceDir(work), "index.jsonl"))
	if err != nil {
		t.Fatalf("读取索引失败: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 {
		t.Fatalf("索引应 1 行, 实际 %d", len(lines))
	}
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("索引行非法 JSON: %v (%s)", err, lines[0])
	}
	for _, k := range []string{"ts", "kind", "hit", "verdict", "mode", "fp", "sha256", "bytes", "file"} {
		if _, ok := rec[k]; !ok {
			t.Fatalf("索引缺字段 %q: %s", k, lines[0])
		}
	}
	if rec["fp"] != fp {
		t.Fatalf("索引指纹 %v 与落盘指纹 %q 不符", rec["fp"], fp)
	}
	sum := sha256.Sum256([]byte(code))
	if rec["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("索引 sha256 与代码实际哈希不符: %v", rec["sha256"])
	}
	if rec["verdict"] != "deny" {
		t.Fatalf("裁决应为 deny, 实际 %v", rec["verdict"])
	}
	// mode 必须落进索引(20261002 补): 否则事后无法区分「主人按的 y」与「委托代批」——
	// 事故那条自杀记录正是这种无从查证的状态(19 条历史记录全部只有 allow)。
	if rec["mode"] != modeHuman {
		t.Fatalf("索引 mode 应为 %q, 实际 %v", modeHuman, rec["mode"])
	}
	// 索引指向的文件必须真实存在 —— 悬空引用与清单「悬置条目」同类腐化
	if _, err := os.Stat(filepath.Join(approvalEvidenceDir(work), rec["file"].(string))); err != nil {
		t.Fatalf("索引指向的文件不存在: %v", err)
	}
}

func TestPruneApprovalEvidence_KeepsNewestAndIndexInSync(t *testing.T) {
	work := t.TempDir()
	dir := approvalEvidenceDir(work)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	const total = approvalKeepMax + 5
	var idx strings.Builder
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("20260101T%06d_%012d.code", i, i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(map[string]interface{}{"file": name, "verdict": "allow"})
		idx.Write(append(line, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, "index.jsonl"), []byte(idx.String()), 0644); err != nil {
		t.Fatal(err)
	}
	removed := pruneApprovalEvidence(dir, approvalKeepMax)
	if removed != 5 {
		t.Fatalf("应删 5 份, 实际 %d", removed)
	}
	ents, _ := os.ReadDir(dir)
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".code") {
			n++
		}
	}
	if n != approvalKeepMax {
		t.Fatalf("应保留 %d 份, 实际 %d", approvalKeepMax, n)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("20260101T%06d_%012d.code", 0, 0))); !os.IsNotExist(err) {
		t.Fatal("最旧一份应被删除")
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("20260101T%06d_%012d.code", total-1, total-1))); err != nil {
		t.Fatalf("最新一份必须保留: %v", err)
	}
	// 索引必须同步剔除 —— 否则指向已删文件
	ib, _ := os.ReadFile(filepath.Join(dir, "index.jsonl"))
	rows := strings.Split(strings.TrimSpace(string(ib)), "\n")
	if len(rows) != approvalKeepMax {
		t.Fatalf("索引应同步剩 %d 行, 实际 %d", approvalKeepMax, len(rows))
	}
	gone := fmt.Sprintf("20260101T%06d_%012d.code", 0, 0)
	if strings.Contains(string(ib), gone) {
		t.Fatalf("索引仍含已删文件 %q —— 悬空引用", gone)
	}
}

// 单一数据源一致性: 程序侧轮转闸门 与 卫生清单判据 必须同值, 否则各说各话。
func TestApprovalEvidence_ManifestMatchesCode(t *testing.T) {
	b, err := os.ReadFile(forgeManifestFile)
	if err != nil {
		t.Fatalf("读取卫生清单失败: %v", err)
	}
	var m struct {
		Entries []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"entries"`
		Subdirs []struct {
			Path     string                  `json:"path"`
			Entries  []struct{ Name string } `json:"entries"`
			Patterns []struct {
				Glob string `json:"glob"`
				Keep int    `json:"keep"`
			} `json:"patterns"`
		} `json:"subdirs"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("卫生清单非法 JSON: %v", err)
	}
	found := false
	for _, e := range m.Entries {
		if e.Name == approvalDirName {
			found = true
			if e.Kind != "dir" {
				t.Fatalf(".forge/%s 应登记为 dir, 实际 %q", approvalDirName, e.Kind)
			}
		}
	}
	if !found {
		t.Fatalf(".forge/%s 未登记进 entries —— 容器内部也要有判据(否则又是盲区)", approvalDirName)
	}
	var container *struct {
		Path     string                  `json:"path"`
		Entries  []struct{ Name string } `json:"entries"`
		Patterns []struct {
			Glob string `json:"glob"`
			Keep int    `json:"keep"`
		} `json:"patterns"`
	}
	for i := range m.Subdirs {
		if m.Subdirs[i].Path == approvalDirName {
			container = &m.Subdirs[i]
		}
	}
	if container == nil {
		t.Fatalf("subdirs 缺 %q 容器 —— 该目录内部无任何判据", approvalDirName)
	}
	keepFound := false
	for _, p := range container.Patterns {
		if p.Glob == "*.code" {
			keepFound = true
			if p.Keep != approvalKeepMax {
				t.Fatalf("清单 keep=%d 与程序闸门 approvalKeepMax=%d 不一致 —— 两套实现必然漂移",
					p.Keep, approvalKeepMax)
			}
		}
	}
	if !keepFound {
		t.Fatal("subdirs 容器缺 *.code 轮转判据")
	}
}

// 接线哨兵: 取证写了但没接进 confirmDangerous = 等于没写。
// AST 判定, 不靠提交信息也不靠模型声称(教训: 接线只能由死程序判定)。
func TestApprovalEvidence_WiringSentinel(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "forge.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 forge.go 失败: %v", err)
	}
	var body *ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name.Name == "confirmDangerous" {
			body = fd.Body
			return false
		}
		return true
	})
	if body == nil {
		t.Fatal("未找到 confirmDangerous 函数 —— 审批门被改名或删除?")
	}
	calls := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if id, ok := ce.Fun.(*ast.Ident); ok {
				calls[id.Name] = true
			}
		}
		return true
	})
	for _, want := range []string{"approvalEvidenceNotice", "recordApprovalEvidence", "parseApproval"} {
		if !calls[want] {
			t.Errorf("confirmDangerous 未调用 %s —— 取证通道未接线(写了等于没写)", want)
		}
	}
}

// 端到端: 批准路径必须留下 (全文 + 索引 + 指纹), 拒绝路径同样取证(deny 也是证据)。
func TestConfirmDangerous_WritesEvidence(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		verdict string
	}{
		{"批准", "y\n", "allow"},
		{"拒绝", "n\n", "deny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			work := t.TempDir()
			isolateApprovalDelegate(t) // 测人工路径, 必须先关掉委托直通
			isolateEventsPath(t)
			withStdinText(t, c.input)
			f := sinkStderr(t)
			code := "os.remove(\"memory.json\") // e2e"
			if got := (&Forge{workDir: work}).confirmDangerous(code, "保护目标", "memory.json"); got != (c.verdict == "allow") {
				t.Fatalf("批准结果应为 %v, 实际 %v", c.verdict == "allow", got)
			}
			if _, err := f.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			sb, _ := os.ReadFile(f.Name())
			if !strings.Contains(string(sb), "指纹") {
				t.Fatalf("审批提示未显示指纹 —— 主人按 y 前看不到取证信息:\n%s", string(sb))
			}
			dir := approvalEvidenceDir(work)
			ents, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("取证目录不存在: %v", err)
			}
			var codeFiles int
			for _, e := range ents {
				if strings.HasSuffix(e.Name(), ".code") {
					codeFiles++
					if b, _ := os.ReadFile(filepath.Join(dir, e.Name())); string(b) != code {
						t.Fatalf("取证内容与代码不符: %q", string(b))
					}
				}
			}
			if codeFiles != 1 {
				t.Fatalf("应有 1 份全文取证, 实际 %d", codeFiles)
			}
			ib, err := os.ReadFile(filepath.Join(dir, "index.jsonl"))
			if err != nil {
				t.Fatalf("索引未写入: %v", err)
			}
			var rec map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(string(ib))), &rec); err != nil {
				t.Fatalf("索引行非法 JSON: %v (%s)", err, ib)
			}
			if rec["verdict"] != c.verdict {
				t.Fatalf("索引裁决应为 %q, 实际 %v", c.verdict, rec["verdict"])
			}
		})
	}
}
