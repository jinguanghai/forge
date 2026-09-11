package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestGofmtClean 强制守卫: 根目录主包 .go 必须全部 gofmt 干净 (提交前防线)
func TestGofmtClean(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") {
			files = append(files, name)
		}
	}
	args := append([]string{"-l"}, files...)
	out, err := exec.Command("gofmt", args...).Output()
	if err != nil {
		t.Fatalf("gofmt -l: %v", err)
	}
	list := strings.TrimSpace(string(out))
	if list != "" {
		t.Fatalf("以下文件未 gofmt:\n%s", list)
	}
}
