// commands_diag.go: 诊断类子命令 (自检/识图/记忆诊断/健康/锚点/门同步)

package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func cmdMemhealth(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(memHealthReport(cfg.WorkDir))
	fmt.Println()
}

func cmdGatesync(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(gateSyncCheck(cfg.WorkDir, cfg.PluginReleaseDir))
	fmt.Println()
}

func cmdAnchor(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("锚点写入审计 (七期护栏):"))
	fmt.Println(anchorAuditSummary(cfg.WorkDir))
	fmt.Println()
}

func cmdMemdiag(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("☯ 记忆节律诊断 (π-φ):"))
	md := MemDiagMetrics(cfg.WorkDir)
	if md == nil {
		fmt.Println("  ✗ 诊断失败")
	} else {
		fmt.Printf("  %s\n", md.Summary)
		if len(md.Vector) == 8 {
			req, _ := json.Marshal(map[string]interface{}{"type": "diagnose", "vector": md.Vector, "domain": "memory"})
			_, res, errB := agent.forge.Build(string(req), "tcm", "")
			if errB != nil || res == nil || !res.OK {
				emsg := "诊断失败"
				if errB != nil {
					emsg = errB.Error()
				} else if res != nil && res.Error != "" {
					emsg = res.Error
				}
				fmt.Println("  " + color(ansi.red, "✗") + " " + emsg)
			} else {
				var dr map[string]interface{}
				if err := json.Unmarshal([]byte(res.Stdout), &dr); err != nil {
					fmt.Println("  " + color(ansi.red, "✗") + " 解析失败: " + err.Error())
				} else {
					fmt.Printf("  %s %s\n", bold("卦象:"), dr["dominant"])
					fmt.Printf("  %s %s (%s)\n", bold("六势态:"), dr["stage"], dr["stage_desc"])
					fmt.Printf("  %s %s\n", bold("优先战略:"), dr["recommendation"])
					if sc, ok := dr["strategies"].([]interface{}); ok && len(sc) > 0 {
						fmt.Print("  战略排序: ")
						for i, s := range sc {
							if i >= 3 {
								break
							}
							if m, ok := s.(map[string]interface{}); ok {
								fmt.Printf("%s(%.2f) ", m["name"], m["score"].(float64))
							}
						}
						fmt.Println()
					}
				}
			}
		}
		for _, a := range md.Advice {
			fmt.Println("  " + color(ansi.yellow, "•") + " " + a)
		}
	}
	fmt.Println()
}

func cmdDiagnose(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	fmt.Println(bold("☯ 铸剑炉自诊断 (八极八势·六势态):"))
	fs := agent.forge.Stats()
	builds, _ := fs["builds"].(int64)
	errs, _ := fs["errors"].(int64)
	hits, _ := fs["cache_hits"].(int64)
	size, _ := fs["cache_size"].(int64)

	// 运行指标 → 八极向量 [阳,阴,表,里,寒,热,虚,实]
	// 阳: 工具执行活跃度(近builds) 阴: 缓存稳定度 表: 外部接口 里: 内部协作
	// 寒: 错误抑制(反向) 热: 负载热度 虚: 资源空缺(缓存薄) 实: 积压占用
	yang := clampF(2+float64(builds%10), 0, 10)            // 构建活跃
	yin := clampF(4+float64(size%8), 0, 10)                // 缓存积累=承载稳定
	biao := clampF(3+float64(len(agent.history)%8), 0, 10) // 外部交互面
	li := clampF(5+float64(len(agent.history)%6), 0, 10)   // 内部上下文
	cold := clampF(10-float64(errs), 0, 10)                // 错误低=寒少
	heat := clampF(2+float64(errs%5), 0, 10)               // 错误高=热亢
	xu := clampF(10-float64(hits%10), 0, 10)               // 缓存命中低=虚
	shi := clampF(2+float64(size%6), 0, 10)                // 缓存占用=实
	vec := []float64{yang, yin, biao, li, cold, heat, xu, shi}

	fmt.Printf("  指标: builds=%d errors=%d cache_hits=%d cache_size=%d\n", builds, errs, hits, size)
	req, _ := json.Marshal(map[string]interface{}{"type": "diagnose", "vector": vec, "domain": "system"})
	_, res, errB := agent.forge.Build(string(req), "tcm", "")
	if errB != nil || res == nil || !res.OK {
		emsg := "诊断失败"
		if errB != nil {
			emsg = errB.Error()
		} else if res != nil && res.Error != "" {
			emsg = res.Error
		}
		fmt.Println("  " + color(ansi.red, "✗") + " " + emsg)
	} else {
		var dr map[string]interface{}
		if err := json.Unmarshal([]byte(res.Stdout), &dr); err != nil {
			fmt.Println("  " + color(ansi.red, "✗") + " 解析失败: " + err.Error())
		} else {
			fmt.Printf("  %s %s\n", bold("卦象:"), dr["dominant"])
			fmt.Printf("  %s %s (%s)\n", bold("六势态:"), dr["stage"], dr["stage_desc"])
			fmt.Printf("  %s %s\n", bold("优先战略:"), dr["recommendation"])
			if w, _ := dr["warning"].(string); w != "" {
				fmt.Println("  " + color(ansi.yellow, "⚠") + " " + w)
			}
			if sc, ok := dr["strategies"].([]interface{}); ok {
				fmt.Print("  战略排序: ")
				for i, s := range sc {
					if i >= 4 {
						break
					}
					if m, ok := s.(map[string]interface{}); ok {
						fmt.Printf("%s(%.2f) ", m["name"], m["score"].(float64))
					}
				}
				fmt.Println()
			}
		}
	}
	fmt.Println()
}

func cmdHealth(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	fmt.Println()
	pong := agent.forge.Ping()
	fmt.Printf("  %s %s\n", bold("Forge:"), color(ansi.green, pong))
	if agent.llm.IsHealthy() {
		fmt.Printf("  %s %s\n", bold("LLM:"), color(ansi.green, "healthy"))
	} else {
		fmt.Printf("  %s %s\n", bold("LLM:"), color(ansi.red, "degraded"))
	}
	printQualityAlerts(cfg.WorkDir)
	fmt.Println()
}

func cmdVision(agent *AgentRunner, cfg *Config, parts []string, historyFile string, showReasoning *bool, cmd string) {
	arg := ""
	diag := false
	if len(parts) > 1 {
		arg = strings.TrimSpace(strings.Join(parts[1:], " "))
		for _, flag := range []string{"-d", "--diag", "诊断", "diag", "体检", "分析"} {
			if strings.Contains(arg, flag) {
				diag = true
				arg = strings.ReplaceAll(arg, flag, "")
			}
		}
		arg = strings.TrimSpace(arg)
	}
	if arg == "" {
		fmt.Println()
		fmt.Printf("  %s 用法: /看图 <目录> [-d]  — 识别目录下全部图片; 追加 -d 为诊断模式(识别+分析+建议)\n", color(ansi.yellow, "⚠"))
		fmt.Println()
		return
	}
	dir := arg
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cfg.WorkDir, dir)
	}
	imgs, err := discoverImagesInDir(dir)
	if err != nil {
		fmt.Printf("  %s 识图失败: %v\n", color(ansi.red, "✗"), err)
		fmt.Println()
		return
	}
	if len(imgs) == 0 {
		fmt.Printf("  %s 目录 %s 未发现图片 (JPEG/PNG/GIF/WebP)\n", color(ansi.yellow, "⚠"), dir)
		fmt.Println()
		return
	}
	mode := "描述"
	if diag {
		mode = "诊断"
	}
	fmt.Printf("  %s %s\n", color(ansi.blue, "🖼"), dim(fmt.Sprintf("识图 %d 张 (%s) → %s [%s模式]", len(imgs), dir, cfg.ModelVision, mode)))
	fmt.Println()
	// 构造带图的多模态 user 消息 (无工具), 强制视觉模型, 流式输出
	sys := ChatMessage{Role: "system", Content: buildSystemPromptStable(cfg.WorkDir, cfg.GatesEnabled)}
	prompt := "请识别以下图片，并逐张描述主要内容。"
	if diag {
		prompt = "你是视觉诊断专家。请对以下图片做三件事：\n" +
			"① 识别 — 完整转述看到的内容(文字/布局/对象/颜色/层级);\n" +
			"② 诊断 — 系统性列出问题点并分级 🔴高/🟡中/🟢低, 涵盖可读性/对齐/色彩对比/信息密度/层级/操作路径; 若是 CLI 界面再额外审视提示符/日志/状态栏/命令列表/配色;\n" +
			"③ 建议 — 每个问题给一句可立即执行的改法(调整对齐/加间距/改颜色/删冗余/移位置/补提示); \n" +
			"只依据图中真实存在的元素判断, 不编造; 若图片正常无明显问题, 明确说'未发现明显问题'。"
	}
	user := ChatMessage{Role: "user", Content: prompt, Images: imgs}
	msgs := []ChatMessage{sys, user}
	spin := startSpinner("铸剑炉识图中…")
	ch := agent.llm.ChatCompletionStream(agent.Context(), msgs, nil, cfg.ModelVision)
	var contentBuf, reasoningBuf strings.Builder
	var toolCalls []ToolCall
	streamErr := agent.processStream(ch, &contentBuf, &reasoningBuf, &toolCalls, cfg.ShowReasoning, spin.stop)
	spin.stop()
	if streamErr != nil {
		fmt.Printf("  %s %v\n", color(ansi.red, "✗"), streamErr)
	}
	fmt.Println()
}
