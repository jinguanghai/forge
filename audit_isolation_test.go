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
	// 同构隔离: 缓存统计的写入出口 (见 cache_stats.go cacheStatPathForWrite)。
	// 缺此设置时, 测试里直接调 parseSSE/doStream 会把假模型记录写进仓库根。
	os.Setenv("FORGE_CACHE_STATS_PATH", filepath.Join(tmp, "cache_stats.jsonl"))
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
	// 测试进程: 生产根强制隔离 (20260930 收紧)。此前只覆盖"WorkDir 无效",
	// 用真实 WorkDir 构造 Forge 的测试 (TestForgeGateHost_*) 会绕过隔离写生产文件。
	for _, dir := range []string{"", "."} {
		if got := auditFilePath(dir); got != iso {
			t.Fatalf("测试进程里 WorkDir=%q 应回退隔离路径, 实际 %s", dir, got)
		}
	}
	// 判据收窄到生产根: 非生产根的有效目录 (t.TempDir) 仍优先 —— 一刀切会打断
	// 用临时目录写自己审计的合法测试 (实测 4 个用例红)。
	valid := filepath.Join("C:", "tmp", "somewhere")
	if got := auditFilePath(valid); got != filepath.Join(valid, "gate_audit.jsonl") {
		t.Fatalf("非生产根的有效 WorkDir 应优先, 实际 %s", got)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := auditFilePath(cwd); got != iso {
		t.Fatalf("生产根 (%s) 必须强制隔离, 实际 %s", cwd, got)
	}
}

// 哨兵 c: 用真实 WorkDir 的写入同样必须被隔离 (20260930 补)。
//
// 缺口: 旧隔离只覆盖"WorkDir 为空/点"的回退分支, 而跑真实 gate 的测试
// (TestForgeGateHost_*) 用的是真实 WorkDir —— 实测一次全量测试往生产
// gate_audit.jsonl 注入数十条带 event 的假记录 (含 approval_wait_ms 等构造值),
// 直接污染 gate 失败率/耗时/重试率的统计基础。
func TestAuditIsolation_RealWorkdirStillIsolated(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	prod := filepath.Join(cwd, "gate_audit.jsonl")
	const marker = "isolated_realwd_probe_9384756"
	before := 0
	if b, err := os.ReadFile(prod); err == nil {
		before = strings.Count(string(b), marker)
	}
	f := &Forge{workDir: cwd} // 真实 WorkDir —— 旧实现会直接写生产文件
	appendAuditJSONL(auditFilePath(f.workDir), map[string]interface{}{"event": "gate", "probe": marker})
	if b, err := os.ReadFile(prod); err == nil && strings.Count(string(b), marker) > before {
		t.Fatalf("真实 WorkDir 的写入落到了生产文件 %s —— 隔离仍有缺口", prod)
	}
	iso := os.Getenv("FORGE_AUDIT_PATH")
	if b, err := os.ReadFile(iso); err != nil || !strings.Contains(string(b), marker) {
		t.Fatalf("隔离文件未收到写入 (err=%v) —— 隔离过宽, 埋点被误关", err)
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
