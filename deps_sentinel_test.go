package main

// deps_sentinel_test.go — 外部依赖面判据 (20261004 六西格玛·消除静默错误)
//
// 背景: 铸剑炉的「零外部依赖」此前只是约定, 没有判据 —— 加一个 import 没人拦。
// 同型事故: tcm_gate.exe 落后源码两个提交(09-25 产物 vs 09-28 源码), 源码里的
// 方剂库路径修正从未生效, 而四套守卫全绿。
//
// 判据与 defense_system/dep_check.py 同源(那边给巡检用, 这边给 go test 用)。

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestGoModHasNoThirdPartyDeps: 本体只许依赖标准库。
func TestGoModHasNoThirdPartyDeps(t *testing.T) {
	b, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("读 go.mod: %v", err)
	}
	var deps []string
	inBlock := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(strings.Split(raw, "//")[0])
		if line == "" {
			continue
		}
		switch {
		case inBlock:
			if line == ")" {
				inBlock = false
			} else {
				deps = append(deps, line)
			}
		case strings.HasPrefix(line, "require ("):
			inBlock = true
		case strings.HasPrefix(line, "require "):
			deps = append(deps, strings.TrimPrefix(line, "require "))
		}
	}
	if len(deps) > 0 {
		t.Errorf("go.mod 出现第三方依赖(本体应为零): %v\n"+
			"若确需引入, 请同步更新 memory.json gates 字段 / README / 发布包, 并说明为何不能用标准库", deps)
	}
}

// TestRequirementsTxtPinned: Python 侧外部依赖必须钉死版本。
func TestRequirementsTxtPinned(t *testing.T) {
	b, err := os.ReadFile("requirements.txt")
	if err != nil {
		t.Fatalf("读 requirements.txt: %v (gate 依赖必须显式登记)", err)
	}
	pinned := regexp.MustCompile(`^[A-Za-z0-9_.\-]+==[^\s;]+$`)
	n := 0
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(strings.Split(raw, "#")[0])
		if line == "" {
			continue
		}
		if !pinned.MatchString(line) {
			t.Errorf("requirements.txt 未钉版本: %q", line)
			continue
		}
		n++
	}
	if n == 0 {
		t.Error("requirements.txt 没有任何钉版本的依赖项")
	}
}

// TestGateArtifactsMatchSources: 自托管 gate 的产物必须由当前源码编译而来。
// 慢(要编译 5 个 gate), 只在非 short 档跑。
func TestGateArtifactsMatchSources(t *testing.T) {
	if testing.Short() {
		t.Skip("short 档跳过: 需编译 5 个 gate")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH: %v", err)
	}
	tools := filepath.Join(".forge", "forge-tools")
	for _, name := range []string{"math_gate", "logic_gate", "regex_gate", "knowledge_gate", "tcm_gate"} {
		src := filepath.Join(tools, name+".go")
		exe := filepath.Join(tools, name+".exe")
		if _, err := os.Stat(src); err != nil {
			continue // 该 gate 未在此仓库维护
		}
		if _, err := os.Stat(exe); err != nil {
			t.Errorf("%s.exe 缺失(源码在但产物不在)", name)
			continue
		}
		out := filepath.Join(t.TempDir(), name+".exe")
		// 包参数用「相对 cmd.Dir 的文件名」: 传仓库相对路径会变成
		// "no required module provides package .forge/forge-tools/x.go"(实测)
		cmd := exec.Command("go", "build", "-o", out, name+".go")
		cmd.Dir = tools
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s 重建失败: %v\n%s", name, err, b)
			continue
		}
		want, err1 := fileSHA(exe)
		got, err2 := fileSHA(out)
		if err1 != nil || err2 != nil {
			t.Errorf("%s 计算 sha256 失败: %v %v", name, err1, err2)
			continue
		}
		if want != got {
			t.Errorf("%s.exe 与源码不一致(现役 %s != 重建 %s): 源码已改但未重编译",
				name, want[:12], got[:12])
		}
	}
}

func fileSHA(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
