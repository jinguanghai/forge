package main

// tool_ref.go: 超大工具输出"落盘引用" (六西格玛根治 P0①, TRIZ 时间/空间分离解)。

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const goalAnchorRefOutputThreshold = 12000

func goalAnchorRef(output, taskAnchor string, turn int, workDir string) string {
	runes := []rune(output)
	if len(runes) <= goalAnchorRefOutputThreshold {
		return goalAnchor(output, taskAnchor, turn)
	}
	ref := storeToolOutputRef(output, turn, workDir)
	headTxt := string(runes[:2000])
	tailTxt := string(runes[len(runes)-1200:])
	omit := len(runes) - 2000 - 1200
	if omit < 0 {
		omit = 0
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("<工具输出过大(%d rune)，全文已落盘保存到: %s\n", len(runes), ref))
	sb.WriteString("以下为头/尾摘要：\n")
	sb.WriteString("───── 头 ─────\n")
	sb.WriteString(headTxt)
	sb.WriteString(fmt.Sprintf("\n\n...(省略 %d rune)...\n\n", omit))
	sb.WriteString("───── 尾 ─────\n")
	sb.WriteString(tailTxt)
	sb.WriteString("\n───── 摘要结束 ─────\n")
	sb.WriteString("> 如需完整内容，请用 read 工具读取上述文件（文件已自动生成）。\n\n")
	sb.WriteString(fmt.Sprintf("🎯 原始任务: %s\n📍 第%d轮", taskAnchor, turn+1))
	return sb.String()
}

func storeToolOutputRef(output string, turn int, workDir string) string {
	dir := filepath.Join(workDir, ".forge", "toolcache")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ""
	}
	h := sha256.Sum256([]byte(output))
	name := fmt.Sprintf("tool_%03d_%s.md", turn+1, hex.EncodeToString(h[:6]))
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(output), 0644); err != nil {
		return ""
	}
	return p
}
