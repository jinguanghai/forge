package main

// audit_isolation_test.go — 审计隔离 TestMain + 隔离有效性哨兵 (20260913)
//
// 问题 (实测): 测试进程的 AgentRunner/Forge 若 WorkDir 为 "" 或 ".", 审计行
// 会写回仓库根的 gate_audit.jsonl。实测一次全量测试注入 33 行
// (28 条 gate + 4 条 compact + 1 条 compact_failed, 其中 compact_failed 的
// err 是测试自己造的 "boom") —— 生产审计日志被假数据污染, 而 gate 失败率/
// 耗时/压缩成功率这些治理结论正建立在该文件上。
//
// 本文件:
//   ① 隔离: TestMain 把 FORGE_AUDIT_PATH 指向临时文件, 承接所有 WorkDir 无效
//      的测试写入 (auditFilePath 的回退分支)。已有用 t.TempDir() 的测试走
//      WorkDir 分支, 不受影响。
//   ② 哨兵: 两条死程序判定 —— (a) auditFilePath 优先级语义;
//      (b) 端到端: 无效 WorkDir 的写入必须落在隔离文件, 且生产文件里
//      不得出现本次写入的特征串 (用唯一特征串, 故不受外部会话写入干扰)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "forge_audit_test_")
	if err != nil {
		tmp = os.TempDir()
	}
	os.Setenv("FORGE_AUDIT_PATH", filepath.Join(tmp, "gate_audit.jsonl"))
	code := m.Run()
	_ = os.RemoveAll(tmp)
	os.Exit(code)
}

// 哨兵 a: auditFilePath 的优先级语义 (隔离生效的前提)。
func TestAuditIsolation_PathPriority(t *testing.T) {
	iso := os.Getenv("FORGE_AUDIT_PATH")
	if iso == "" {
		t.Fatal("TestMain 未设置 FORGE_AUDIT_PATH, 审计隔离失效")
	}
	for _, dir := range []string{"", "."} {
		if got := auditFilePath(dir); got != iso {
			t.Fatalf("WorkDir=%q 应回退隔离路径, 实际 %s", dir, got)
		}
	}
	// 有效 WorkDir 必须优先 (否则会破坏已有的 t.TempDir() 类测试)
	valid := filepath.Join("C:", "tmp", "somewhere")
	if got := auditFilePath(valid); got != filepath.Join(valid, "gate_audit.jsonl") {
		t.Fatalf("有效 WorkDir 应优先, 实际 %s", got)
	}
}

// 哨兵 b: 端到端 —— 无效 WorkDir 的审计写入不得落到仓库根的生产文件。
func TestAuditIsolation_WriteDoesNotReachRepoRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	prod := filepath.Join(cwd, "gate_audit.jsonl")

	// 唯一特征串: 只可能由本次写入产生, 故外部会话的并发写入不会干扰判据。
	// 特征串取算式本身 (val 会被 nswFmtNum 规范化, 不能依赖等号右侧形态)
	const marker = "1234567*7654321"
	asst := "结果 " + marker + "=9449778912807"
	all := nswEvaluate(asst)
	fresh := nswFilterEchoed(asst, all)
	if len(fresh) == 0 {
		t.Skipf("嗅探器未识别本用例算式, 哨兵不适用 (all=%d)", len(all))
	}
	a := &AgentRunner{cfg: &Config{}} // WorkDir 空 = 触发回退分支
	nswAudit(a, asst, fresh, len(all))

	if b, err := os.ReadFile(prod); err == nil && strings.Contains(string(b), marker) {
		t.Fatalf("审计写入落到了生产文件 %s —— 隔离失效 (测试正在污染 gate_audit.jsonl)", prod)
	}
	iso := os.Getenv("FORGE_AUDIT_PATH")
	if b, err := os.ReadFile(iso); err != nil || !strings.Contains(string(b), marker) {
		t.Fatalf("隔离文件 %s 未收到写入 (err=%v) —— 埋点被误关", iso, err)
	}
}
