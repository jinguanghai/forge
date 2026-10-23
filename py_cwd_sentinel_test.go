package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPyCwdSentinel：拉起 Python 哨兵脚本，断言全绿
// 与 verifyClaim 哨兵同型：subprocess 走 cmd.Dir 显式绑仓库根，
// 不允许仅凭 go build 干净就判通过——必须由本进程亲跑同型验证。
func TestPyCwdSentinel(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("获取仓库根失败: %v", err)
	}
	py := "python"
	if v := strings.TrimSpace(os.Getenv("FORGE_PYTHON")); v != "" {
		py = v
	}
	script := filepath.Join(root, "hygiene", "py_cwd_sentinel.py")

	// 关键：cmd.Dir 显式绑仓库根（教训 20261007：subprocess 不绑 cwd 会让守卫输出错位）
	cmd := exec.Command(py, script)
	cmd.Dir = root
	cmd.Env = pythonUTF8Env() // 中文输出必须按 UTF-8 读, 否则判据恒假
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("py_cwd 哨兵失败（exit=%v）:\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "全绿") {
		t.Fatalf("py_cwd 哨兵未返回全绿:\n%s", string(out))
	}
	t.Logf("py_cwd 哨兵: %s", strings.TrimSpace(string(out)))
}
