package main

// ============================================================================
// snapshot_refresh_test.go —— 快照生成臂与时效判据的行为哨兵 (20261005)
//
// 背景 (现场核对): defense_system/snapshots/ 里只有 1 份 6 天前的快照, 而生成动作
// 此前只挂在人工按钮上 (selfheal.py snapshot), 无任何调度触发 —— 判据看得见的
// (份数/体积) 与真正重要的 (抗体新鲜度) 不是同一件事。
//
// 本文件把三件事钉死 (端到端跑真实脚本, 不 mock):
//   1. 生成臂的决策边界 (恰好等于阈值不刷新 / 超过才刷新 / 空库必须刷新)
//   2. --apply 与自定义 --snapdir 互斥 (防「报告刷新了 A 目录、实际写到 B 目录」)
//   3. 只读判据的时效硬判据: 最新一份超 refresh_days -> rc=1, 原因指向时效
//
// 为什么是 Go 测试而非 Python 自检: 接线与阈值是包级属性, 必须进 go test 回归
// 才有人跑; Python 侧自检函数无人调用就是「备而未用」。
// ============================================================================

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	snapRefreshRel = "defense_system/snapshot_refresh.py"
	snapCheckRel   = "defense_system/hygiene_snapshot_check.py"
	snapNameStub   = "snap_20261005_120000"
)

// snapPyRun 跑 python 脚本并返回 (rc, 合并输出)。
// FORGE_ALERT_LOG 把告警日志重定向到临时文件: 判据跑出真违规时不得把测试噪声
// 写进生产告警日志 (否则「告警日志里都是真的」这条前提就断了)。
func snapPyRun(t *testing.T, rel string, args ...string) (int, string) {
	t.Helper()
	py, err := exec.LookPath(guardGatePython())
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	cmd := exec.Command(py, append([]string{rel}, args...)...)
	cmd.Env = append(pythonUTF8Env(),
		"FORGE_ALERT_LOG="+filepath.Join(t.TempDir(), "alerts.jsonl"))
	out, rerr := cmd.CombinedOutput()
	if rerr == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(rerr, &ee) {
		return ee.ExitCode(), string(out)
	}
	t.Fatalf("执行 %s 失败: %v (%s)", rel, rerr, out)
	return -1, ""
}

// snapFixture 造一个只含一份快照的快照库, 其 mtime = now - ageDays。
// 必须带一个子目录文件: 只读判据有一条「子目录落盘失效」硬判据
// (defense_system/*.py 不落盘 -> 这类文件永远 [NO-SNAP]), 夹具不合规会让
// 「新鲜快照不该报违规」这条断言恒假 —— 测的必须是时效判据本身, 不是夹具。
func snapFixture(t *testing.T, ageDays float64) string {
	t.Helper()
	dir := t.TempDir()
	snap := filepath.Join(dir, snapNameStub)
	sub := filepath.Join(snap, "defense_system")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("建快照目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "dummy.py"), []byte("# stub\n"), 0o644); err != nil {
		t.Fatalf("写快照子目录文件失败: %v", err)
	}
	// Chtimes 必须放在最后: 建子目录/写文件都会刷新父目录 mtime。
	mt := time.Now().Add(-time.Duration(ageDays * 24 * float64(time.Hour)))
	if err := os.Chtimes(snap, mt, mt); err != nil {
		t.Fatalf("设置快照 mtime 失败: %v", err)
	}
	return dir
}

// TestSnapshotRefreshSelftest 生成臂的 fail-closed 与边界必须自证 (纯函数自检)。
func TestSnapshotRefreshSelftest(t *testing.T) {
	rc, out := snapPyRun(t, snapRefreshRel, "--selftest")
	if rc != 0 {
		t.Fatalf("生成臂判据自检应通过 (rc=0), 实际 rc=%d:\n%s", rc, out)
	}
	if !strings.Contains(out, "selftest 通过") {
		t.Errorf("自检输出缺少通过标记:\n%s", out)
	}
}

// TestSnapshotRefreshDecidesByAge 决策: 新鲜不动 / 陈旧要刷 / 空库要刷。
// 反向断言不可省: 只测「陈旧要刷」会让「一律刷新」这种每小时生成也全绿。
func TestSnapshotRefreshDecidesByAge(t *testing.T) {
	rc, out := snapPyRun(t, snapRefreshRel, "--snapdir", snapFixture(t, 0.5))
	if rc != 0 || !strings.Contains(out, "无需刷新") {
		t.Errorf("新鲜快照应判无需刷新 (rc=0), 实际 rc=%d:\n%s", rc, out)
	}
	rc, out = snapPyRun(t, snapRefreshRel, "--snapdir", snapFixture(t, 3))
	if rc != 0 || !strings.Contains(out, "需要刷新") {
		t.Errorf("陈旧快照应判需要刷新 (dry-run rc=0), 实际 rc=%d:\n%s", rc, out)
	}
	rc, out = snapPyRun(t, snapRefreshRel, "--snapdir", t.TempDir())
	if rc != 0 || !strings.Contains(out, "需要刷新") {
		t.Errorf("空快照库应判需要刷新, 实际 rc=%d:\n%s", rc, out)
	}
}

// TestSnapshotRefreshApplyRejectsCustomSnapdir --apply 与自定义目录互斥 (fail-closed)。
// 允许这个组合 = 「报告刷新了 A 目录、实际写到 B 目录」的静默失效。
func TestSnapshotRefreshApplyRejectsCustomSnapdir(t *testing.T) {
	rc, out := snapPyRun(t, snapRefreshRel, "--apply", "--snapdir", snapFixture(t, 3))
	if rc != 2 {
		t.Errorf("--apply + 自定义 --snapdir 必须 fail-closed (rc=2), 实际 rc=%d:\n%s", rc, out)
	}
	if !strings.Contains(out, "互斥") {
		t.Errorf("未给出互斥原因:\n%s", out)
	}
}

// TestSnapshotCheckFlagsStaleLatest 时效硬判据: 最新一份超 refresh_days -> rc=1。
// 这是「生成臂失效」的唯一可见信号 —— 没有它, 生成臂悄悄不跑就无人知晓。
func TestSnapshotCheckFlagsStaleLatest(t *testing.T) {
	rc, out := snapPyRun(t, snapCheckRel, "--snapdir", snapFixture(t, 3))
	if rc != 1 {
		t.Errorf("最新快照超 refresh_days 应报硬违规 (rc=1), 实际 rc=%d:\n%s", rc, out)
	}
	if !strings.Contains(out, "时效上限") {
		t.Errorf("违规原因未指向时效判据:\n%s", out)
	}
	rc, out = snapPyRun(t, snapCheckRel, "--snapdir", snapFixture(t, 0.2))
	if rc != 0 {
		t.Errorf("新鲜快照不应报违规 (rc=0), 实际 rc=%d:\n%s", rc, out)
	}
}
