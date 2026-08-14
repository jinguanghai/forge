package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestTCMGateIntegration(t *testing.T) {
	cfg := &Config{MaxConcurrent: 4, CacheMaxSize: 100, RetryMax: 0}
	f := NewForge("D:\\forge", cfg)
	defer f.Shutdown()

	cases := []struct{ name, text, wantRec string }{
		{"上热下寒", "市场与营销部门过度亢奋，承诺过多。核心研发部门动力不足。阳热亢进，阴寒不足，上下失交。", "控势"},
		{"虚实夹杂", "老旧城区基础设施薄弱。新兴开发区过度开发，商业体量过剩。虚实夹杂，动态失衡。", "造势"},
	}
	for _, c := range cases {
		_, res, err := f.Build(c.text, "tcm", "")
		if err != nil || res == nil || !res.OK {
			t.Fatalf("[%s] gate failed: err=%v res=%+v", c.name, err, res)
		}
		var dr map[string]interface{}
		if err := json.Unmarshal([]byte(res.Stdout), &dr); err != nil {
			t.Fatalf("[%s] bad json: %v", c.name, err)
		}
		got, _ := dr["recommendation"].(string)
		fmt.Printf("  %s → 卦象=%s 推荐=%s 六势态=%s\n", c.name, dr["dominant"], got, dr["stage"])
		if got != c.wantRec {
			t.Errorf("[%s] want %s got %s", c.name, c.wantRec, got)
		}
	}
	_, res2, err2 := f.Build("公司战略摇摆不定，表里不一，方向感缺失。", "tcm", "")
	if err2 != nil || res2 == nil || !res2.OK {
		t.Fatalf("plain auto-wrap failed: %v %+v", err2, res2)
	}
	fmt.Printf("  裸文本自动包装 → %s\n", res2.Stdout[:200])
}

func TestTCMVectorDiagnose(t *testing.T) {
	cfg := &Config{MaxConcurrent: 4, CacheMaxSize: 100, RetryMax: 0}
	f := NewForge("D:\\forge", cfg)
	defer f.Shutdown()
	req, _ := json.Marshal(map[string]interface{}{"type": "diagnose", "vector": []float64{8, 5, 7, 4, 3, 5, 4, 4}, "domain": "body"})
	_, res, err := f.Build(string(req), "tcm", "")
	if err != nil || res == nil || !res.OK {
		t.Fatalf("vector diagnose failed: %v %+v", err, res)
	}
	fmt.Printf("  向量诊断 → %s\n", res.Stdout[:250])
}
