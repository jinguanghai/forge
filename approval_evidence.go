// approval_evidence.go: 审批取证通道 —— B 路「审得清」的第一块砖。
//
// 背景(2026-10 安全测试实测): 审批留痕只有 {hit, verdict}, 代码正文不进任何持久通道 ——
// guard_log.jsonl 的 input 被截到前 80 字(617 条中 93.7% 被截断; 中位代码 1293 字只能看见 6.2%),
// events.jsonl 的审批事件连代码都没有。后果: 主人按 y 之后, 无从证明当时批准的究竟是哪段代码。
//
// 本通道做三件事:
//
//	① 算 sha256 指纹(十六进制前 12 位), 同时写进审批事件 —— 跨流可对照
//	② 完整代码落盘 <workDir>/.forge/approvals/<时间戳>_<指纹>.code (tmp+rename 原子替换)
//	③ 元数据追加 index.jsonl; 轮转时同步剔除被删文件的记录
//
// 为什么另起通道而不往 guard_log 存全文: 那是完整性守卫的日志(617 条历史 + 无轮转),
// 混进全文会让它体积失控; 且「审批取证」与「守卫事件」是两种语义, 混流后各自都判不准。
//
// 落盘失败必须返回 error 并显示在审批提示里 —— 静默失败 = 没取证却以为有, 比不取证更坏。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// approvalDirName 取证目录(相对 .forge)。
	approvalDirName = "approvals"
	// approvalKeepMax 保留份数上限。与 hygiene_forge_manifest.json 里
	// subdirs[approvals].patterns["*.code"].keep 必须一致 —— 由测试断言两者相等,
	// 否则程序闸门与卫生判据各说各话(教训: 两套实现必然漂移)。
	approvalKeepMax = 200
	// approvalFingerprintLen sha256 十六进制前缀长度。12 位 = 48 bit, 人工核对够用且短。
	approvalFingerprintLen = 12
)

// approvalFingerprint 代码指纹: sha256 十六进制前 12 位。
// 空代码也返回指纹(空串的 sha256), 不返回 "" —— 否则调用方无法区分「没算」与「空代码」。
func approvalFingerprint(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])[:approvalFingerprintLen]
}

// approvalEvidenceDir 取证目录绝对路径。
func approvalEvidenceDir(workDir string) string {
	return filepath.Join(workDir, ".forge", approvalDirName)
}

// stageApprovalEvidence 把代码全文落盘(提问之前调用), 返回 (指纹, 落盘绝对路径, 错误)。
// 先落盘后提问: 主人按 y 之前就该看到指纹与全文路径 —— 事后取证对「按的那一刻」无用。
func stageApprovalEvidence(workDir, code string) (string, string, error) {
	fp := approvalFingerprint(code)
	if workDir == "" {
		return fp, "", fmt.Errorf("workDir 为空, 取证无处可落")
	}
	dir := approvalEvidenceDir(workDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fp, "", err
	}
	base := time.Now().Format("20060102T150405") + "_" + fp
	target := filepath.Join(dir, base+".code")
	// 同秒同指纹重名(极少): 加序号, 绝不覆盖已有取证 —— 覆盖即销毁证据。
	for i := 1; ; i++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		}
		target = filepath.Join(dir, fmt.Sprintf("%s_%d.code", base, i))
	}
	// tmp + rename: 崩在中间只留 .tmp, 不会留半截「看起来像证据」的文件。
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, []byte(code), 0644); err != nil {
		return fp, "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fp, "", err
	}
	return fp, target, nil
}

// 审批索引的 mode 取值: 谁做的裁决。
// 缺口(20261002): 索引原无 mode 字段 —— 事后从索引看不出「主人按了 y」还是「委托档
// 代批」, 与 events.jsonl 口径不一致(那里有 mode=auto)。审计口径必须统一: 19 条历史
// 「自杀」记录全部只写 allow, 谁批的无从查证; 而事故那条正是委托代批。
const (
	modeHuman    = "human"        // 主人在终端按 y/N
	modeAuto     = "auto"         // 委托档代批
	modeRejected = "irreversible" // 不可逆自毁类: 直接拒绝(无批准入口)
)

// recordApprovalEvidence 追加一条 index 记录(裁决已知后调用), 并做轮转。
// 返回 (写入错误, 本次轮转删除份数)。索引写失败不致命(全文已落盘), 但必须上报。
func recordApprovalEvidence(workDir, kind, hit, verdict, mode, code, codePath string) (error, int) {
	if workDir == "" {
		return fmt.Errorf("workDir 为空, 无法写审批索引"), 0
	}
	sum := sha256.Sum256([]byte(code))
	dir := approvalEvidenceDir(workDir)
	rec := map[string]interface{}{
		"ts":      time.Now().Format("2006-01-02T15:04:05"),
		"kind":    kind,
		"hit":     hit,
		"verdict": verdict,
		"mode":    mode,
		"fp":      hex.EncodeToString(sum[:])[:approvalFingerprintLen],
		"sha256":  hex.EncodeToString(sum[:]),
		"bytes":   len(code),
		"file":    filepath.Base(codePath),
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return err, 0
	}
	f, err := os.OpenFile(filepath.Join(dir, "index.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err, 0
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if werr != nil {
		return werr, 0
	}
	if cerr != nil {
		return cerr, 0
	}
	return nil, pruneApprovalEvidence(dir, approvalKeepMax)
}

// pruneApprovalEvidence 保留最近 max 份 .code, 删最旧, 并同步从 index.jsonl 剔除
// 对应行 —— 否则索引变成指向已删文件的悬空引用(与清单「悬置条目」同类腐化)。
// 文件名以时间戳开头, 字典序即时间序。返回实际删除份数。
func pruneApprovalEvidence(dir string, max int) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var codes []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".code") {
			codes = append(codes, e.Name())
		}
	}
	if len(codes) <= max {
		return 0
	}
	sort.Strings(codes)
	removed := map[string]bool{}
	for _, n := range codes[:len(codes)-max] {
		if os.Remove(filepath.Join(dir, n)) == nil {
			removed[n] = true
		}
	}
	if len(removed) > 0 {
		pruneApprovalIndex(dir, removed)
	}
	return len(removed)
}

// pruneApprovalIndex 从 index.jsonl 剔除 file 命中 removed 的行(重写, tmp+rename)。
// 重写失败不回滚已删的 .code —— 索引多一行悬空引用, 好过证据文件复活。
func pruneApprovalIndex(dir string, removed map[string]bool) {
	p := filepath.Join(dir, "index.jsonl")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var kept []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec struct {
			File string `json:"file"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && removed[rec.File] {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0644); err != nil {
		return
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
	}
}

// approvalEvidenceNotice 生成审批提示里的取证信息行, 同时完成落盘。
// 返回 (提示文本, 指纹, 落盘路径)。落盘失败时提示文本明写失败原因 ——
// 静默失败会让主人以为「有留痕」, 而事后追查时什么都没有。
func approvalEvidenceNotice(workDir, code string) (string, string, string) {
	fp, path, err := stageApprovalEvidence(workDir, code)
	if err != nil {
		return fmt.Sprintf("  ⚠ 取证落盘失败: %v (本次审批无全文留痕)\n", err), fp, ""
	}
	rel := path
	if r, rerr := filepath.Rel(workDir, path); rerr == nil {
		rel = r
	}
	return fmt.Sprintf("  📄 指纹 %s · 完整代码 %s\n", fp, rel), fp, path
}
