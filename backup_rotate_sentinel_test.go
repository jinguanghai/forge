package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBackupRotate_NoHardcodedThreshold 哨兵: 备份/快照轮转阈值必须走 backupKeepCount,
// 生产源码中不得出现裸数字实参。
//
// 背景(20260924 实测): 20260915 的阈值收口(10 -> backupKeepCount=3)漏掉了 forge.go 的
// deploySelfExe(f.workDir, targetPath, 10, realSmokeRunner) —— self gate 部署路径仍按 10 保留,
// 09-22~09-24 六次部署后根目录残留 6 份 forge.exe.bak_*, 轮转等于没生效。
// 判据必须是死程序(源码扫描): 靠人记得同步五个调用点, 必然再漏。
func TestBackupRotate_NoHardcodedThreshold(t *testing.T) {
	pat := regexp.MustCompile(`\b(?:deploySelfExe|pruneExeBackups|pruneCheckpoints|pruneSelfBackups)\s*\([^)\n]*,\s*[0-9]+\s*[,)]`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("未发现源码文件, 哨兵失效")
	}
	scanned := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		scanned++
		if m := pat.FindString(string(src)); m != "" {
			t.Errorf("%s: 备份轮转阈值硬编码: %q -- 应使用 backupKeepCount", f, strings.TrimSpace(m))
		}
	}
	if scanned == 0 {
		t.Fatal("未扫描到任何生产源码, 哨兵失效")
	}
	t.Logf("已扫描 %d 个生产源码文件", scanned)
}
