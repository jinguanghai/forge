//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAsrFile_E2E(t *testing.T) {
	os.Setenv("FORGE_ASR_DIR", filepath.Join(os.Getenv("WORKDIR"), ".forge", "asr_tool"))
	pl := filepath.Join(os.Getenv("WORKDIR"), ".forge-temp", "asr_test_audio.mp3")
	if _, err := os.Stat(pl); err != nil {
		t.Skipf("测试音频不存在: %v", err)
	}
	txt, err := asrFile(pl)
	if err != nil {
		t.Fatalf("asrFile 失败: %v", err)
	}
	txt = strings.TrimSpace(txt)
	if !strings.Contains(txt, "心慌") {
		t.Errorf("识别结果未含关键词「心慌」: %q", txt)
	}
	t.Logf("识别结果: %q", txt)
}
