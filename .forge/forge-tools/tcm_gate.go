package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// ============ Data model (eight-extreme eight-posture meta-model + six-state general model) ============

// eight extremes: minimal complete dimension set of system state [yang,yin,exterior,interior,cold,heat,deficiency,excess]
var poleNames = []string{"阳", "阴", "表", "里", "寒", "热", "虚", "实"}
var baguaNames = []string{"乾", "坤", "艮", "兑", "坎", "离", "震", "巽"}

// eight postures: situation-based strategic interventions (ideal state vector = corresponding dimension prominent, others moderate)
type Strategy struct {
	Name    string
	Target  string // targeted eight-extreme dimension
	Essence string // strategy essence
	Ideal   [8]float64
}

var strategies = []Strategy{
	{"识势", "表", "明察秋毫，知微见著", [8]float64{5, 5, 10, 5, 5, 5, 5, 5}},
	{"造势", "虚", "从无到有，创造机遇", [8]float64{5, 5, 5, 5, 5, 5, 10, 5}},
	{"运势", "里", "调和内部，优化配置", [8]float64{5, 5, 5, 10, 5, 5, 5, 5}},
	{"控势", "热", "控制火候，防止过亢", [8]float64{5, 5, 5, 5, 5, 10, 5, 5}},
	{"借势", "寒", "借力打力，化险为夷", [8]float64{5, 5, 5, 5, 10, 5, 5, 5}},
	{"乘势", "阳", "把握时机，顺势而为", [8]float64{10, 5, 5, 5, 5, 5, 5, 5}},
	{"破势", "实", "破除障碍，疏通道路", [8]float64{5, 5, 5, 5, 5, 5, 5, 10}},
	{"定势", "阴", "巩固根本，厚积薄发", [8]float64{5, 10, 5, 5, 5, 5, 5, 5}},
}

// six states: six stages of a complex system coping with disturbance
type Stage struct {
	Name     string
	Label    string
	Desc     string
	Warning  string
}

var stages = []Stage{
	{"平和", "阴阳既济", "八极均衡、无过亢过衰，系统处于健康稳态，阴阳和谐", "系统状态平和：保持当前节律，以定势固本、运势调中，防微杜渐。"},
	{"太阳", "积极抗御", "驱动能量强盛且作用于系统边界，高响应强度，边界开放", "系统处于启动/抗御期：对外反应快，注意保持边界开放但不过度消耗。"},
	{"阳明", "内部炽盛", "驱动能量极强但在内部壅堵，无法有效输出，高内压", "系统内部炽盛壅堵：能量无法输出，需破势疏通，防止内耗加剧。"},
	{"少阳", "枢机不利", "驱动能量与承载结构在枢纽处沟通不畅，状态摇摆", "系统枢机不利：沟通不畅导致摇摆，需运势调和内部枢纽。"},
	{"太阴", "运化失职", "驱动能量不足，承载结构运化生产功能衰退", "系统运化失职：功能衰退、负担累积，需造势注入新能量。"},
	{"少阴", "核心衰微", "驱动能量核心源衰竭，承载结构面临解体", "系统核心衰微：存在崩溃风险，需借势转化或定势固本。"},
	{"厥阴", "寒热错杂", "驱动能量与承载结构秩序崩溃，内在矛盾，混沌边缘", "系统秩序崩坏：局部与整体矛盾，需重新定义秩序（重组/终结）。"},
}

// ============ Request / Response ============

type TCMRequest struct {
	Type   string    `json:"type"` // "diagnose" (default) | "self"
	Text   string    `json:"text,omitempty"` // natural-language description (optional; mutually exclusive with Vector)
	Vector []float64 `json:"vector,omitempty"` // explicit 8-dim vector [yang,yin,exterior,interior,cold,heat,deficiency,excess] 0-10
	Domain string    `json:"domain,omitempty"` // optional domain hint (e.g. company/city/body/system)
}

type StrategyScore struct {
	Name    string  `json:"name"`
	Score   float64 `json:"score"`
	Target  string  `json:"target"`
	Essence string  `json:"essence"`
}

type TCMResult struct {
	OK         bool            `json:"ok"`
	Vector     []float64       `json:"vector"`
	Dominant   string          `json:"dominant"` // dominant hexagram (upper+lower trigrams)
	Strategies []StrategyScore `json:"strategies"` // eight-posture match scores (descending)
	Recommend  string          `json:"recommendation"`
	Stage      string          `json:"stage"`
	StageDesc  string          `json:"stage_desc"`
	Warning    string          `json:"warning,omitempty"`
	Confidence float64         `json:"confidence"`
	Summary    string          `json:"summary"`
	Error      string          `json:"error,omitempty"`
}

// ============ Text parsing (keyword scoring → eight-dimension vector) ============

var keywordMap = map[string][]string{
	"阳": {"阳", "驱动", "主动", "创新", "展开", "进攻", "扩张", "积极", "动能", "热情", "冲劲", "激进", "进取", "开拓"},
	"阴": {"阴", "稳定", "承载", "结构", "收敛", "基础", "物质", "沉稳", "积累", "防御", "保守", "固守", "沉淀", "底蕴"},
	"表": {"表", "边界", "接口", "防御", "外部", "感知", "表面", "外层", "市场", "客户", "前端", "界面", "入口"},
	"里": {"里", "内部", "核心", "深层", "协作", "信息处理", "内在", "研发", "后端", "机制", "组织", "内功"},
	"寒": {"寒", "抑制", "低迷", "停滞", "衰退", "冷", "萧条", "萎缩", "迟缓", "冻结", "冷却", "失活", "动力不足", "产品迭代缓慢", "下寒"},
	"热": {"热", "亢进", "过载", "过热", "火爆", "激增", "暴涨", "疯狂", "炽盛", "高压", "透支", "过度", "超负荷", "亢奋", "虚火", "上热", "消耗大量资源"},
	"虚": {"虚", "匮乏", "薄弱", "空虚", "短缺", "缺", "弱", "不稳", "落后", "贫乏", "疲软", "乏力", "老旧", "资源匮乏"},
	"实": {"实", "壅堵", "郁结", "阻塞", "过剩", "积压", "拥堵", "淤积", "冗余", "堵塞", "臃肿", "沉疴", "赘余", "商业体量过剩"},
}

func textToVector(text string) [8]float64 {
	var counts [8]float64
	for i, pole := range poleNames {
		for _, kw := range keywordMap[pole] {
			c := strings.Count(text, kw)
			counts[i] += float64(c) * 1.5 // weight per hit
		}
	}
	// normalize to 0-10 (saturating)
	var v [8]float64
	for i := 0; i < 8; i++ {
		v[i] = math.Min(10, counts[i]*2)
	}
	// all-zero falls back to the moderate baseline 5 (avoid division by zero)
	allZero := true
	for _, x := range v {
		if x > 0 {
			allZero = false
			break
		}
	}
	if allZero {
		for i := range v {
			v[i] = 5
		}
	}
	return v
}

// ============ Core computation ============

func cosineSimilarity(a, b [8]float64) float64 {
	var dot, na, nb float64
	for i := 0; i < 8; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// diagnose returns eight-posture match scores (descending) with recommendations
func diagnose(v [8]float64) ([]StrategyScore, string) {
	scores := make([]StrategyScore, len(strategies))
	for i, s := range strategies {
		sc := cosineSimilarity(v, s.Ideal)
		scores[i] = StrategyScore{Name: s.Name, Score: sc, Target: s.Target, Essence: s.Essence}
	}
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})
	// recommendation: the strategy with the highest match > 0.7; if all low (equilibrium) → posture-consolidation
	rec := scores[0].Name
	if scores[0].Score < 0.72 {
		rec = "定势"
		for _, s := range scores {
			if s.Name == "运势" {
				rec = "运势"
			}
		}
	}
	return scores, rec
}

// six-state positioning: E (driving energy≈yang) S (carrying structure≈yin)
func locateStage(v [8]float64) Stage {
	yang, yin := v[0], v[1]
	heat, cold := v[5], v[4]
	shi, xu := v[7], v[6]
	biao, li := v[2], v[3]

	// 0. balance: eight extremes in equilibrium (small spread, no excess/deficiency) → healthy steady state
	minV, maxV := 10.0, 0.0
	for _, x := range v {
		if x < minV { minV = x }
		if x > maxV { maxV = x }
	}
	if maxV-minV <= 2.5 && maxV <= 7 && minV >= 3 {
		return stages[0] // balance
	}

	// 1. jueyin: cold-heat intermingled (contradictory state)
	if heat >= 7 && cold >= 7 {
		return stages[6] // jueyin
	}
	if xu >= 7 && shi >= 7 {
		return stages[6] // deficiency-excess mixed → internal contradiction
	}
	// 2. shaoyin: core decline (E very weak, S also weak)
	if yang <= 3 && yin <= 3.5 {
		return stages[5]
	}
	// 3. taiyin: transformation failure (E weak, S still present)
	if yang <= 4 && yin >= 5 {
		return stages[4]
	}
	// 4. yangming: internal excess (E strong but congested interior)
	if yang >= 6 && shi >= 6 && li >= 5 {
		return stages[2]
	}
	// 5. taiyang: active defense (E strong, acting on the exterior)
	if yang >= 6 && biao >= 6 && li < 6 {
		return stages[1]
	}
	// 6. shaoyang: pivot dysfunction (E-S communication poor, exterior/interior similar, swaying)
	if math.Abs(yang-yin) <= 2.5 && math.Abs(biao-li) <= 2.5 {
		return stages[3] // shaoyang
	}
	// fallback: E/S comparison
	if yang >= yin {
		return stages[1]
	}
	return stages[3]
}

// hexagram: top two dimensions map to trigrams (upper=second-highest, lower=highest)
func dominantBagua(v [8]float64) string {
	idx := []int{0, 1, 2, 3, 4, 5, 6, 7}
	sort.SliceStable(idx, func(i, j int) bool { return v[idx[i]] > v[idx[j]] })
	top := baguaNames[idx[0]]
	second := baguaNames[idx[1]]
	return second + "上" + top + "下"
}

// confidence: completeness of text/vector information (explicit vector=high; text parsing=medium)
func main() {
	if len(os.Args) < 2 {
		fmt.Println(`{"ok":false,"error":"usage: tcm_gate.exe '<json>' "}`)
		os.Exit(1)
	}
	var req TCMRequest
	if err := json.Unmarshal([]byte(os.Args[1]), &req); err != nil {
		fmt.Printf(`{"ok":false,"error":"%s"}`, err.Error())
		os.Exit(1)
	}
	if req.Type == "" {
		req.Type = "diagnose"
	}

	var v [8]float64
	confidence := 0.85
	if len(req.Vector) == 8 {
		for i, x := range req.Vector {
			v[i] = math.Max(0, math.Min(10, x))
		}
		confidence = 0.9
	} else {
		if strings.TrimSpace(req.Text) == "" {
			fmt.Println(`{"ok":false,"error":"需要 vector(8维) 或 text(自然语言描述)"}`)
			os.Exit(1)
		}
		v = textToVector(req.Text)
		confidence = 0.75
	}

	scores, rec := diagnose(v)
	stage := locateStage(v)
	res := TCMResult{
		OK:         true,
		Vector:     v[:],
		Dominant:   dominantBagua(v),
		Strategies: scores,
		Recommend:  rec,
		Stage:      stage.Name,
		StageDesc:  stage.Label + "：" + stage.Desc,
		Warning:    stage.Warning,
		Confidence: confidence,
		Summary:    fmt.Sprintf("卦象%s | 六势态【%s·%s】| 优先战略「%s」", dominantBagua(v), stage.Name, stage.Label, rec),
	}
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
}
