package main

// ============================================================================
// fractal_repo_hygiene_test.go —— 仓库级字节卫生判据的接线哨兵 (T13, 20261005)
//
// 缺陷 (20261005 实测): fractal_check.py 的字节卫生预检只扫主包 .go ——
//   defense_system/*.py / 判据 json / 文档全在盲区。实测 Python open(p,'w')
//   把 6 个仓库文本文件写成 CRLF 时, 预检照样报「主包 .go 无 NUL/CRLF」全绿,
//   污染从盲区漏过。
//
// 修法: fractal_check.py 新增 repo_hygiene 判据 —— 规范权威是 .gitattributes
//   的 eol 属性(经 `git ls-files --eol` 读取, 不自己实现扩展名规则: 两处规则
//   必然漂移), 只判 eol=lf 的文本文件; cmd/bat/ps1 的 eol=crlf 属规范, 不报。
//   与 source_hygiene(主包 .go, 编译单元)分键: 文案与严重度名实相符。
//
// 本文件钉住三件事 (接线只能由死程序判定):
//   1. 真实工作区干净, 且清单必须取到 (note 非空 = git 不可用 -> Fatal:
//      判据不得静默当全绿)
//   2. 沙箱 git 仓库: CRLF 报出精确「文件:行」, 改回 LF 全绿;
//      eol=crlf 的 .cmd 不得被误报 (误报合规文件 = 判据与规范脱节)
//   3. main() 里两个键同时在场 (新键不得顶掉编译单元语义)
// ============================================================================

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const repoHygieneProbe = `
import sys, json
sys.path.insert(0, 'defense_system')
import fractal_check as fc
root = json.loads(sys.argv[1])
bad, note = fc.scan_repo_hygiene(root)
print(json.dumps({'bad': ['%s:%d %s' % h for h in bad], 'note': note}, ensure_ascii=False))
`

type repoHygResult struct {
	Bad  []string `json:"bad"`
	Note string   `json:"note"`
}

// repoHygieneScan 跑 Python 探针调 scan_repo_hygiene(root) —— fail-closed:
// 探针跑不起来即 Fatal (拿不到结论 != 结论为空)。
func repoHygieneScan(t *testing.T, root string) repoHygResult {
	t.Helper()
	rj, _ := json.Marshal(root)
	cmd := exec.Command(guardGatePython(), "-c", repoHygieneProbe, string(rj))
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("探针执行失败: %v\n%s", err, out)
	}
	var r repoHygResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("探针输出解析失败: %v\n%s", err, out)
	}
	return r
}

func gitRunIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
}

// TestFractalRepoHygieneRealWorkspace 真实工作区对账: 清单必须取到且干净。
func TestFractalRepoHygieneRealWorkspace(t *testing.T) {
	r := repoHygieneScan(t, ".")
	if r.Note != "" {
		t.Fatalf("仓库清单取不到 (%s) —— 判据不得静默当全绿", r.Note)
	}
	if len(r.Bad) != 0 {
		t.Fatalf("真实工作区有 %d 处仓库文本字节污染: %v", len(r.Bad), r.Bad)
	}
}

// TestFractalRepoHygieneCatchesCRLF 沙箱仓库: 报红 -> 改回 -> 全绿, 且不误报 .cmd。
func TestFractalRepoHygieneCatchesCRLF(t *testing.T) {
	dir := t.TempDir()
	gitRunIn(t, dir, "init", "-q")
	write := func(rel, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(data), 0o644); err != nil {
			t.Fatalf("写 %s: %v", rel, err)
		}
	}
	write(".gitattributes", "*.py text eol=lf\n*.cmd text eol=crlf\n")
	write("a.py", "x = 1\ny = 2\n")
	write("run.cmd", "@echo off\r\necho hi\r\n") // eol=crlf 属规范: 不得被误报
	gitRunIn(t, dir, "add", "-A")

	if r := repoHygieneScan(t, dir); len(r.Bad) != 0 {
		t.Fatalf("全 LF 合规仓库(含合规 CRLF 的 .cmd)被判红: %v", r.Bad)
	}
	write("a.py", "x = 1\r\ny = 2\r\n")
	r := repoHygieneScan(t, dir)
	if len(r.Bad) != 1 || !strings.HasPrefix(r.Bad[0], "a.py:1 ") || !strings.Contains(r.Bad[0], "CRLF") {
		t.Fatalf("CRLF 污染未被精确报出 (期望 a.py:1 CRLF...): %v", r.Bad)
	}
	write("a.py", "x = 1\ny = 2\n")
	if r := repoHygieneScan(t, dir); len(r.Bad) != 0 {
		t.Fatalf("改回 LF 后仍报红: %v", r.Bad)
	}
}

// TestFractalRepoHygieneWiredInMain 接线 + 语义分离 (文案名实相符)。
func TestFractalRepoHygieneWiredInMain(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("defense_system", "fractal_check.py"))
	if err != nil {
		t.Fatalf("读 fractal_check.py 失败: %v", err)
	}
	src := string(raw)
	for _, want := range []string{"'key': 'repo_hygiene'", "'key': 'source_hygiene'",
		"'key': 'repo_hygiene_scan'", "scan_repo_hygiene()", "非编译单元"} {
		if !strings.Contains(src, want) {
			t.Fatalf("fractal_check.py 缺少 %q —— 仓库级判据接线断线", want)
		}
	}
	seg := src[strings.Index(src, "'key': 'repo_hygiene'"):]
	if j := strings.Index(seg, "if rc != 0"); j > 0 {
		seg = seg[:j]
	}
	if strings.Contains(seg, "go test 必失败") {
		t.Fatalf("repo_hygiene 文案混用了编译单元语义 (名实不符 = 告警疲劳根源): %s", seg)
	}
}

// TestFractalRepoHygieneSelftest Python 侧鉴别力自检 (死程序判定)。
func TestFractalRepoHygieneSelftest(t *testing.T) {
	cmd := exec.Command(guardGatePython(), filepath.Join("defense_system", "fractal_check.py"), "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--selftest 失败: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "向量通过") || strings.Contains(string(out), "FAIL") {
		t.Fatalf("--selftest 输出异常: %s", out)
	}
}
