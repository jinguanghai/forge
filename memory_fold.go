// 折叠展开记忆系统 v2.1 —— π-φ 折展节律引擎（memory_fold.go）
//
// π(展开)↔φ(折叠) 循环节律
//   M1 schema v2.1: 加 pi_expansions / phi_folds / keywords / last_hinted
//   M2 半显化: "展开<名>"→摘要先行; "深入<名>"→读全文
//   M3 相关性触发: 对话关键词命中折叠项→静默提示一次(24h去重)
//   M4 循环回写: 展开更新π+last_unfolded; 折叠更新φ(再浓缩)
//   M5 节律控制: π≥3高频🔥标记; Tier-2 建议清理
//   M6 /memdiag: π/φ指标→八极向量→tcm_gate 卦象
//
// 定位: 单窗口内的注意力管理机制。跨窗口不解决（跨窗口靠 memory.json 锚点）。
// 三公理: 记忆是锚不是包袱——已完成任务折叠成一行索引，细节进 _archive\folded_memory\。

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FoldedItem 折叠索引条目（schema v2.1）
type FoldedItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"` // done | doing | shelved
	Summary      string `json:"summary"`
	Archive      string `json:"archive"`
	FoldedAt     string `json:"folded_at"`
	LastUnfolded string `json:"last_unfolded"` // 最近展开时间；"" 表示从未展开
	Tier         int    `json:"tier"`          // 1=正常, 2=超60天未展开可清理
	// ── π-φ 节律字段 (v2.1) ──
	PiExpansions int      `json:"pi_expansions"`         // π: 累计展开次数
	PhiFolds     int      `json:"phi_folds"`             // φ: 累计折叠次数
	Keywords     []string `json:"keywords,omitempty"`    // 相关性触发关键词
	LastHinted   string   `json:"last_hinted,omitempty"` // 最近自动提示时间(24h去重)
}

func memoryFilePath(workDir string) string {
	return filepath.Join(workDir, "memory.json")
}

func nowStamp() string {
	return time.Now().Format("20060102_150405")
}

// foldedItems 读取 memory.json 折叠索引
func foldedItems(workDir string) ([]FoldedItem, error) {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	fmRaw, ok := m["folded_memory"]
	if !ok {
		return nil, nil
	}
	var fm struct {
		Items []FoldedItem `json:"items"`
	}
	if err := json.Unmarshal(fmRaw, &fm); err != nil {
		return nil, err
	}
	return fm.Items, nil
}

// updateFoldedItems 通用回写: 只允许改字段，不允许增删条目。原子写(tmp+rename)。
// 实现: 折叠段按 map 解析, 只替换 items, 其余字段(含未来新增)原样保留 ——
// 避免固定结构体重写把未知字段静默丢弃(schema 演进安全)。
func updateFoldedItems(workDir string, fn func(items []FoldedItem) []FoldedItem) error {
	data, _, err := LoadMemory(workDir)
	if err != nil {
		return err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	fmRaw, ok := m["folded_memory"]
	if !ok {
		return fmt.Errorf("memory.json 缺少 folded_memory 段")
	}
	var fm map[string]json.RawMessage
	if err := json.Unmarshal(fmRaw, &fm); err != nil {
		return err
	}
	var items []FoldedItem
	if raw, ok := fm["items"]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return err
		}
	}
	before := len(items)
	items = fn(items)
	if len(items) != before {
		return fmt.Errorf("updateFoldedItems 不允许增删条目")
	}
	newItems, err := json.Marshal(items)
	if err != nil {
		return err
	}
	fm["items"] = newItems
	newFm, err := json.Marshal(fm)
	if err != nil {
		return err
	}
	m["folded_memory"] = newFm
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return SaveMemory(workDir, out)
}

// findFoldedItem 按名称匹配：等名或互相包含（容忍"展开输入端修复" vs "输入端修复+死代码清理"）
func findFoldedItem(items []FoldedItem, name string) *FoldedItem {
	name = strings.TrimSpace(name)
	for i := range items {
		if items[i].Name == name || strings.Contains(items[i].Name, name) || strings.Contains(name, items[i].Name) {
			return &items[i]
		}
	}
	return nil
}

// recordUnfold 回写 π 展开节律: pi_expansions+1, last_unfolded=now
func recordUnfold(workDir, id string) {
	_ = updateFoldedItems(workDir, func(items []FoldedItem) []FoldedItem {
		for i := range items {
			if items[i].ID == id {
				items[i].PiExpansions++
				items[i].LastUnfolded = nowStamp()
			}
		}
		return items
	})
}

// UnfoldPreview 半显化(M2): 摘要+关键词+π/φ元数据，不读档案全文，提示"深入<名>"。
// 同时回写 π（被想起=一次π活动）。
func UnfoldPreview(workDir, name string) (string, error) {
	items, err := foldedItems(workDir)
	if err != nil {
		return "", err
	}
	if strings.Contains(name, "任务总账") {
		return listFoldedText(items), nil
	}
	it := findFoldedItem(items, name)
	if it == nil {
		return "", fmt.Errorf("未找到折叠任务 %q（可用 /folded 或\"展开任务总账\"查看清单）", name)
	}
	recordUnfold(workDir, it.ID)
	kw := "—"
	if len(it.Keywords) > 0 {
		kw = strings.Join(it.Keywords, " / ")
	}
	hot := ""
	if it.PiExpansions+1 >= 3 {
		hot = " 🔥高频"
	}
	tierMark := ""
	if it.Tier >= 2 {
		tierMark = " ⚠Tier-2"
	}
	return fmt.Sprintf("📎 折叠记忆【%s】%s%s\n  摘要: %s\n  关键词: %s\n  π展开×%d | φ折叠×%d | 最近展开: %s\n  ── 输入「深入%s」查看全文",
		it.Name, hot, tierMark, it.Summary, kw, it.PiExpansions+1, it.PhiFolds, orDash(it.LastUnfolded), it.Name), nil
}

// UnfoldDeep 深入(M2): 读档案全文 + 回写 π。
func UnfoldDeep(workDir, name string) (string, error) {
	items, err := foldedItems(workDir)
	if err != nil {
		return "", err
	}
	it := findFoldedItem(items, name)
	if it == nil {
		return "", fmt.Errorf("未找到折叠任务 %q（可用 /folded 或\"展开任务总账\"查看清单）", name)
	}
	recordUnfold(workDir, it.ID)
	p := it.Archive
	if !filepath.IsAbs(p) {
		p = filepath.Join(workDir, p)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", fmt.Errorf("档案读取失败 %s: %v", p, err)
	}
	return fmt.Sprintf("📂 深入任务【%s】\n%s", it.Name, string(data)), nil
}

// FindRelevantFold 相关性触发(M3): 输入命中折叠项关键词→返回该项；24h内提示过则忽略。
func FindRelevantFold(workDir, input string) *FoldedItem {
	items, err := foldedItems(workDir)
	if err != nil || len(items) == 0 {
		return nil
	}
	now := time.Now()
	for i := range items {
		if len(items[i].Keywords) == 0 {
			continue
		}
		hit := false
		for _, kw := range items[i].Keywords {
			if kw != "" && strings.Contains(input, kw) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		if items[i].LastHinted != "" {
			if t, err := time.Parse("20060102_150405", items[i].LastHinted); err == nil {
				if now.Sub(t) < 24*time.Hour {
					continue
				}
			}
		}
		return &items[i]
	}
	return nil
}

// MarkHinted 记录自动提示时间(M3 去重)
func MarkHinted(workDir, id string) {
	_ = updateFoldedItems(workDir, func(items []FoldedItem) []FoldedItem {
		for i := range items {
			if items[i].ID == id {
				items[i].LastHinted = nowStamp()
			}
		}
		return items
	})
}

// MemDiagResult /memdiag 输出(M6)
type MemDiagResult struct {
	Total       int       `json:"total"`
	NeverUnfold int       `json:"never_unfolded"`
	TotalPi     int       `json:"total_pi"`
	TotalPhi    int       `json:"total_phi"`
	AvgPi       float64   `json:"avg_pi"`
	Tier2Count  int       `json:"tier2_count"`
	Vector      []float64 `json:"vector"` // [阳,阴,表,里,寒,热,虚,实]
	Summary     string    `json:"summary"`
	Advice      []string  `json:"advice"`
}

// MemDiagMetrics 采集 π/φ 节律指标并映射八极向量(M6)。
// 八极映射: 阳=π活跃度 阴=φ承载 表=索引可读 里=循环深度 寒=僵化(反向) 热=近期活动 虚=Tier-2流失 实=记忆负担
func MemDiagMetrics(workDir string) *MemDiagResult {
	items, err := foldedItems(workDir)
	if err != nil || len(items) == 0 {
		if err != nil {
			return &MemDiagResult{Summary: "读取折叠索引失败: " + err.Error()}
		}
		return &MemDiagResult{Summary: "（暂无折叠任务）", Vector: []float64{2, 2, 8, 0, 10, 2, 0, 0}}
	}
	total := len(items)
	never, totalPi, tier2, totalPhi := 0, 0, 0, 0
	for _, it := range items {
		if it.LastUnfolded == "" {
			never++
		}
		totalPi += it.PiExpansions
		totalPhi += it.PhiFolds
		if it.Tier >= 2 {
			tier2++
		}
	}
	avgPi := 0.0
	if total > 0 {
		avgPi = float64(totalPi) / float64(total)
	}
	vec := []float64{
		clampF(2+float64(totalPi), 0, 10), // 阳
		clampF(4+float64(total), 0, 10),   // 阴
		8,                                 // 表(索引可读)
		clampF(2+avgPi*2, 0, 10),          // 里(循环深度)
		clampF(10-float64(never)*10/float64(total), 0, 10), // 寒(僵化反向)
		clampF(3+float64(totalPi), 0, 10),                  // 热(活跃)
		clampF(float64(tier2)*4, 0, 10),                    // 虚(Tier-2流失)
		clampF(float64(total)*2, 0, 10),                    // 实(负担)
	}
	advice := []string{}
	if never > 0 {
		advice = append(advice, fmt.Sprintf("%d 项折叠后从未展开（纯φ态）——建议\"展开任务总账\"回顾", never))
	}
	if totalPi == 0 {
		advice = append(advice, "π活性为零——记忆僵化态，多使用\"展开<名称>\"激活")
	}
	if tier2 > 0 {
		advice = append(advice, fmt.Sprintf("%d 项 Tier-2 可清理", tier2))
	}
	if avgPi >= 3 {
		advice = append(advice, "平均π≥3 高频——考虑把常展开项移出折叠区")
	}
	summary := fmt.Sprintf("折叠%d项 | 从未展开%d | π累计%d | 平均π%.1f | φ累计%d | Tier-2 %d",
		total, never, totalPi, avgPi, totalPhi, tier2)
	return &MemDiagResult{Total: total, NeverUnfold: never, TotalPi: totalPi, TotalPhi: totalPhi,
		AvgPi: avgPi, Tier2Count: tier2, Vector: vec, Summary: summary, Advice: advice}
}

// ListFoldedTasks 折叠总账（状态+摘要+π热度+展开指令）
func ListFoldedTasks(workDir string) string {
	items, err := foldedItems(workDir)
	if err != nil {
		return fmt.Sprintf("读取折叠索引失败: %v", err)
	}
	if len(items) == 0 {
		return "（暂无折叠任务）"
	}
	return listFoldedText(items)
}

func listFoldedText(items []FoldedItem) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("折叠展开记忆系统 v2.1 · 共 %d 项（单窗口）\n", len(items)))
	for _, it := range items {
		mark := "✅"
		switch it.Status {
		case "doing":
			mark = "🔵"
		case "shelved":
			mark = "⏸"
		}
		hot := ""
		if it.PiExpansions >= 3 {
			hot = " 🔥高频"
		}
		tier := ""
		if it.Tier >= 2 {
			tier = " ⚠Tier-2 可清理"
		}
		pi := ""
		if it.PiExpansions > 0 {
			pi = fmt.Sprintf(" π×%d", it.PiExpansions)
		}
		sb.WriteString(fmt.Sprintf("  %s %s — %s%s%s%s\n", mark, it.Name, it.Summary, hot, tier, pi))
		sb.WriteString(fmt.Sprintf("     展开: 展开%s | 全文: 深入%s\n", it.Name, it.Name))
	}
	return sb.String()
}

// matchFoldUnfoldCmd 检测显式"展开<名称>"(半显化) 与 "深入<名称>"(全文)。
// 返回 (mode, name)；mode: 1=预览 2=深入 0=未命中。
// "展开讲讲…"这类未命中索引的会 fall through 交给 LLM 当普通对话。
func matchFoldUnfoldCmd(input string) (int, string) {
	for _, prefix := range []string{"深入", "展开"} {
		if strings.HasPrefix(input, prefix) {
			name := strings.TrimSpace(strings.TrimPrefix(input, prefix))
			if name == "" {
				continue
			}
			if prefix == "深入" {
				return 2, name
			}
			return 1, name
		}
	}
	return 0, ""
}

func orDash(s string) string {
	if s == "" {
		return "从未"
	}
	return s
}

// truncateCN 按 rune 截断（中文安全），供相关性提示显示摘要
func truncateCN(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
