package main

// bench_datasets_test.go — 十期: 离线题库判据 (manifest ↔ 实例双向对照)
//
// 动机 (教训「登记动作本身会给对象刷引用数」+「清单与实例必须双向对照」):
//   只写 manifest 不校验实例 → 清单是愿望; 只校验实例不看清单 → 孤儿题无从发现。
//   本哨兵把两边钉在一起, 并复算答案指纹 (防答案被改坏/被覆盖)。
//
// 数据来源: bench/fetch_datasets.py 拉取 (harbor-datasets), bench/judge_datasets.py 判分。
// 未拉取题库时本测试 skip (新克隆仓库没有 datasets/ 是正常的, 不算失败)。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const benchDatasetsDir = "bench/datasets"
const benchManifestRel = "bench/datasets/manifest.json"
const benchJudgeRel = "bench/judge_datasets.py"

type dsManifest struct {
	Sets map[string]map[string]struct {
		SourcePath   string `json:"source_path"`
		SourceCommit string `json:"source_commit"`
		Judge        string `json:"judge"`
		AnswerSHA256 string `json:"answer_sha256"`
	} `json:"sets"`
}

// TestDatasetManifestIntegrity 清单 ↔ 实例双向对照 + 答案指纹复算
func TestDatasetManifestIntegrity(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(benchDatasetsDir); err != nil {
		t.Skip("未拉取题库 (bench/datasets 不存在): 跑 python bench/fetch_datasets.py --set <name>")
	}
	raw, err := os.ReadFile(benchManifestRel)
	if err != nil {
		t.Fatalf("题库目录存在但 manifest 缺失 (fail-closed): %v", err)
	}
	var man dsManifest
	if err := json.Unmarshal(raw, &man); err != nil {
		t.Fatalf("manifest 解析失败: %v", err)
	}
	if len(man.Sets) == 0 {
		t.Fatal("manifest 无任何数据集条目")
	}

	registered := map[string]bool{}
	for set, tasks := range man.Sets {
		if len(tasks) == 0 {
			t.Errorf("数据集 %s 条目为空", set)
		}
		for task, info := range tasks {
			registered[filepath.Join(set, task)] = true
			// ① 未兑现: manifest 说有的, 磁盘必须有
			adir := filepath.Join(benchDatasetsDir, set, task, "answer.txt")
			b, err := os.ReadFile(adir)
			if err != nil {
				t.Errorf("manifest 登记但实例缺失: %s (%v)", adir, err)
				continue
			}
			// ② 指纹复算: 答案不得被静默改写
			sum := sha256.Sum256(b) // 指纹按「磁盘字节」复算, 不做任何归一化
			if info.AnswerSHA256 != "" && hex.EncodeToString(sum[:]) != info.AnswerSHA256 {
				t.Errorf("答案指纹不符: %s (manifest=%s 实际=%s)",
					adir, info.AnswerSHA256[:12], hex.EncodeToString(sum[:])[:12])
			}
			// ③ 结构: 来源与判分方式必须留痕
			if info.SourceCommit == "" || info.Judge == "" || info.SourcePath == "" {
				t.Errorf("%s/%s 缺 source_commit/judge/source_path", set, task)
			}
			if info.Judge != "exact" && info.Judge != "normalized" {
				t.Errorf("%s/%s judge 值非法: %q", set, task, info.Judge)
			}
		}
	}

	// ④ 悬置: 磁盘上的任务目录必须在 manifest 登记 (孤儿 = 不可复现)
	entries, err := os.ReadDir(benchDatasetsDir)
	if err != nil {
		t.Fatalf("读题库目录失败: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		setDir := filepath.Join(benchDatasetsDir, e.Name())
		tasks, err := os.ReadDir(setDir)
		if err != nil {
			continue
		}
		for _, tk := range tasks {
			if !tk.IsDir() {
				continue
			}
			key := filepath.Join(e.Name(), tk.Name())
			if !registered[key] {
				t.Errorf("悬置实例 (目录存在但 manifest 未登记): %s", key)
			}
		}
	}
	t.Logf("题库校验: %d 数据集 / %d 题", len(man.Sets), len(registered))
}

// TestJudgeDatasetsSelftest 判据自身可测: 判分器向量必须全过。
// 判据错了比没有判据更危险 (会给出「像真的」全假阳性)。
func TestJudgeDatasetsSelftest(t *testing.T) {
	t.Parallel()
	if _, err := os.Stat(benchJudgeRel); err != nil {
		t.Skip("判分器未就位")
	}
	cmd := exec.Command(guardGatePython(), benchJudgeRel, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("判分器 --selftest 失败: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "0 失败") {
		t.Errorf("判分器向量未报「0 失败」: %s", out)
	}
}
