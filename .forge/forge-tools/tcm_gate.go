package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
)

// ============ 数据模型（八极八势元模型 + 六势态通用模型） ============

// 八极：系统状态的最小完备维度集 [阳,阴,表,里,寒,热,虚,实]
var poleNames = []string{"阳", "阴", "表", "里", "寒", "热", "虚", "实"}
var baguaNames = []string{"乾", "坤", "艮", "兑", "坎", "离", "震", "巽"}

// 八势：基于态势的战略干预集（理想状态向量 = 对应维度突出，其余中庸）
type Strategy struct {
	Name    string
	Target  string // 对治的八极维度
	Essence string // 战略精要
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

// 六势态：复杂系统应对扰动的六阶段
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

// ============ 请求 / 响应 ============

type TCMRequest struct {
	Type   string    `json:"type"`              // "diagnose"（默认）| "self"
	Text   string    `json:"text,omitempty"`    // 自然语言描述（可选，与 Vector 二选一）
	Vector []float64 `json:"vector,omitempty"`  // 显式 8 维向量 [阳,阴,表,里,寒,热,虚,实] 0-10
	Domain string    `json:"domain,omitempty"`  // 可选领域提示（如 company/city/body/system）
	Herb1  string    `json:"herb1,omitempty"`  // 药对检索: 药1
	Herb2  string    `json:"herb2,omitempty"`  // 药对检索: 药2
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
	Dominant   string          `json:"dominant"`   // 主导卦象（上卦+下卦）
	Strategies []StrategyScore `json:"strategies"` // 八势匹配度排序（降序）
	Recommend  string          `json:"recommendation"`
	Stage      string          `json:"stage"`
	StageDesc  string          `json:"stage_desc"`
	Warning    string          `json:"warning,omitempty"`
	Confidence float64         `json:"confidence"`
	Summary    string          `json:"summary"`
	Error      string          `json:"error,omitempty"`
}

// ============ 文本解析（关键词打分 → 八维向量） ============

var keywordMap = map[string][]string{
	"阳": {"阳", "驱动", "主动", "创新", "展开", "进攻", "扩张", "积极", "动能", "热情", "冲劲", "激进", "进取", "开拓", "上升", "增长", "冲刺", "进展", "活跃", "蓬勃", "奋发", "昂扬", "井喷"},
	"阴": {"阴", "稳定", "承载", "结构", "收敛", "基础", "物质", "沉稳", "积累", "防御", "保守", "固守", "沉淀", "底蕴", "下滑", "回落", "收缩", "扎实", "稳固", "深厚", "内卷"},
	"表": {"表", "边界", "接口", "防御", "外部", "感知", "表面", "外层", "市场", "客户", "前端", "界面", "入口", "对外", "渠道", "流量", "获客", "营销", "品牌", "曝光", "门面"},
	"里": {"里", "内部", "核心", "深层", "协作", "信息处理", "内在", "研发", "后端", "机制", "组织", "内功", "管理", "流程", "团队", "协同", "制度", "文化", "供应链"},
	"寒": {"寒", "抑制", "低迷", "停滞", "衰退", "冷", "萧条", "萎缩", "迟缓", "冻结", "冷却", "失活", "动力不足", "产品迭代缓慢", "下寒", "缓慢", "低落", "士气", "拖延", "卡壳", "冷清", "冰点", "冬眠", "停滞不前"},
	"热": {"热", "亢进", "过载", "过热", "火爆", "激增", "暴涨", "疯狂", "炽盛", "高压", "透支", "过度", "超负荷", "亢奋", "虚火", "上热", "消耗大量资源", "紧张", "焦虑", "加班", "饱和", "满负荷", "赶工", "爆发", "白热化"},
	"虚": {"虚", "匮乏", "薄弱", "空虚", "短缺", "缺", "弱", "不稳", "落后", "贫乏", "疲软", "乏力", "老旧", "资源匮乏", "不足", "紧缺", "疲惫", "倦怠", "增长乏力", "资金紧张", "人才不足", "青黄不接"},
	"实": {"实", "壅堵", "郁结", "阻塞", "过剩", "积压", "拥堵", "淤积", "冗余", "堵塞", "臃肿", "沉疴", "赘余", "商业体量过剩", "瓶颈", "堆积", "卡顿", "僵化", "堆砌", "冗杂", "过时包袱"},
}

func textToVector(text string) [8]float64 {
	var counts [8]float64
	for i, pole := range poleNames {
		for _, kw := range keywordMap[pole] {
			c := strings.Count(text, kw)
			counts[i] += float64(c) * 1.5 // 每命中一次加权
		}
	}
	// 归一化到 0-10（饱和式）
	var v [8]float64
	for i := 0; i < 8; i++ {
		v[i] = math.Min(10, counts[i]*2)
	}
	// 全零则给中庸基线 5（避免除零）
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

// ============ 核心计算 ============

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

// diagnose 返回八势匹配度（降序）与推荐
func diagnose(v [8]float64) ([]StrategyScore, string) {
	scores := make([]StrategyScore, len(strategies))
	for i, s := range strategies {
		sc := cosineSimilarity(v, s.Ideal)
		scores[i] = StrategyScore{Name: s.Name, Score: sc, Target: s.Target, Essence: s.Essence}
	}
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].Score > scores[j].Score
	})
	// 推荐：匹配度最高且 > 0.7 的战略；若都低（均衡态）→ 定势/运势固本
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

// 六势态定位：E(驱动能量≈阳) S(承载结构≈阴)
func locateStage(v [8]float64) Stage {
	yang, yin := v[0], v[1]
	heat, cold := v[5], v[4]
	shi, xu := v[7], v[6]
	biao, li := v[2], v[3]

	// 0. 平和：八极均衡（极差小且无过亢过衰）→ 健康稳态
	minV, maxV := 10.0, 0.0
	for _, x := range v {
		if x < minV { minV = x }
		if x > maxV { maxV = x }
	}
	if maxV-minV <= 2.5 && maxV <= 7 && minV >= 3 {
		return stages[0] // 平和
	}

	// 1. 厥阴：寒热错杂（矛盾状态）
	if heat >= 7 && cold >= 7 {
		return stages[6] // 厥阴
	}
	if xu >= 7 && shi >= 7 {
		return stages[6] // 虚实夹杂 → 内在矛盾
	}
	// 2. 少阴：核心衰微（E 极弱，S 也弱）
	if yang <= 3 && yin <= 3.5 {
		return stages[5]
	}
	// 3. 太阴：运化失职（E 弱，S 尚存）
	if yang <= 4 && yin >= 5 {
		return stages[4]
	}
	// 4. 阳明：内部炽盛（E 强但壅堵在里）
	if yang >= 6 && shi >= 6 && li >= 5 {
		return stages[2]
	}
	// 5. 太阳：积极抗御（E 强，作用于表）
	if yang >= 6 && biao >= 6 && li < 6 {
		return stages[1]
	}
	// 6. 少阳：枢机不利（E 与 S 沟通不畅，表里相近摇摆）
	if math.Abs(yang-yin) <= 2.5 && math.Abs(biao-li) <= 2.5 {
		return stages[3] // 少阳
	}
	// 兜底：取 E/S 对比
	if yang >= yin {
		return stages[1]
	}
	return stages[3]
}

// 卦象：取最高两维对应八卦（上卦=次高，下卦=最高）
func dominantBagua(v [8]float64) string {
	idx := []int{0, 1, 2, 3, 4, 5, 6, 7}
	sort.SliceStable(idx, func(i, j int) bool { return v[idx[i]] > v[idx[j]] })
	top := baguaNames[idx[0]]
	second := baguaNames[idx[1]]
	return second + "上" + top + "下"
}

// 置信度：基于文本/向量信息的完整度（显式向量=高置信；文本解析=中）
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
	if req.Type == "herb_pair" {
		res := herbPairSearch(req.Herb1, req.Herb2)
		if res.Count > 0 {
			res.OK = true
		}
		b, _ := json.Marshal(res)
		fmt.Println(string(b))
		return
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


// ============ 药对检索（双药同现 → 方剂库 formula_db.json） ============

// FormulaEntry 方剂条目（对应 formula_db.json）
type FormulaEntry struct {
	Fang    string   `json:"fang"`
	Book    string   `json:"book"`
	Compose []string `json:"compose"`
	DoseRaw string   `json:"dose_raw"`
	Effect  []string `json:"effect"`
	Pair    string   `json:"pair"`
	Grade   string   `json:"grade"`
}

// HerbPairResult 药对检索结果
type HerbPairResult struct {
	OK       bool            `json:"ok"`
	Herb1    string          `json:"herb1"`
	Herb2    string          `json:"herb2"`
	Count    int             `json:"count"`
	Formulas []FormulaEntry  `json:"formulas"`
	Error    string          `json:"error,omitempty"`
}

var (
	formulaDBOnce sync.Once
	formulaDB     []FormulaEntry
	formulaDBErr  error
)

func loadFormulaDB() ([]FormulaEntry, error) {
	formulaDBOnce.Do(func() {
		paths := []string{
			`D:\\forge\\knowledge\\古籍库\\formula_db.json`,
			"knowledge/古籍库/formula_db.json",
			"formula_db.json",
		}
		for _, p := range paths {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(b, &formulaDB); err == nil && len(formulaDB) > 0 {
				return
			}
		}
		formulaDBErr = fmt.Errorf("formula_db.json 未找到（预期路径: D:\\forge\\knowledge\\古籍库\\formula_db.json）")
	})
	return formulaDB, formulaDBErr
}

// herbAlias 药名归一（别名/异体字 → 标准名）
var herbAlias = map[string][]string{
	"白芍": {"白芍", "白芍药", "芍药", "白芍藥", "赤芍", "赤芍药", "赤芍藥"},
	"枳实": {"枳实", "枳實"},
	"枳壳": {"枳壳", "枳殼"},
	"干姜": {"干姜", "乾薑", "炮姜", "干薑", "炮干姜"},
	"附子": {"附子", "附片", "白附子"},
	"川芎": {"川芎", "芎藭", "芎穷", "抚芎"},
	"黄芪": {"黄芪", "黃芪", "黄耆", "黃耆"},
	"当归": {"当归", "當歸", "全当归", "当归尾"},
	"甘草": {"甘草", "炙甘草", "生甘草", "粉甘草"},
	"人参": {"人参", "人參", "党参", "党參"},
	"白术": {"白术", "白朮", "於术"},
	"茯苓": {"茯苓", "云苓", "白茯苓"},
	"柴胡": {"柴胡", "北柴胡"},
	"黄芩": {"黄芩", "黃芩", "条芩", "枯芩"},
	"黄连": {"黄连", "黃連"},
	"麻黄": {"麻黄", "麻黃"},
	"桂枝": {"桂枝", "桂"},
	"半夏": {"半夏", "法半夏", "制半夏"},
	"陈皮": {"陈皮", "橘皮", "陳皮"},
	"桃仁": {"桃仁", "桃核仁"},
	"红花": {"红花", "紅花", "紅藍花"},
	"大黄": {"大黄", "大黃", "川大黄"},
	"芒硝": {"芒硝", "朴硝", "玄明粉"},
	"金银花": {"金银花", "金銀花", "忍冬"},
	"连翘": {"连翘", "連翹"},
	"生地黄": {"生地黄", "生地"},
	"玄参": {"玄参", "玄參"},
	"枸杞": {"枸杞", "枸杞子", "杞子"},
	"菊花": {"菊花", "甘菊"},
	"山药": {"山药", "山藥", "薯蓣", "薯蕷"},
	"山茱萸": {"山茱萸", "山萸肉"},
	"龙骨": {"龙骨", "龍骨"},
	"牡蛎": {"牡蛎", "牡蠣", "煅牡蛎"},
	"丹参": {"丹参", "丹參"},
	"檀香": {"檀香", "白檀香"},
	"瓜蒌": {"瓜蒌", "栝蒌", "栝樓"},
	"薤白": {"薤白", "薤白"},
	"荆芥": {"荆芥", "荊芥"},
	"防风": {"防风", "防風"},
	"桑叶": {"桑叶", "桑葉"},
	"酸枣仁": {"酸枣仁", "酸棗仁", "枣仁"},
	"柏子仁": {"柏子仁", "柏仁"},
	"郁金": {"郁金", "鬱金", "玉金"},
	"香附": {"香附", "香附子", "制香附"},
	"熟地黄": {"熟地黄", "熟地"},
	"麦冬": {"麦冬", "麥冬", "麦门冬", "麥門冬"},
	"五味子": {"五味子", "五味"},
	"细辛": {"细辛", "細辛"},
	"吴茱萸": {"吴茱萸", "吳茱萸"},
	"苍术": {"苍术", "蒼朮"},
	"厚朴": {"厚朴", "濃朴", "厚樸"},
	"杏仁": {"杏仁", "苦杏仁"},
	"石膏": {"石膏", "生石膏"},
	"知母": {"知母"},
	"葛根": {"葛根"},
	"升麻": {"升麻"},
	"薄荷": {"薄荷"},
	"桔梗": {"桔梗", "苦桔梗"},
	"竹叶": {"竹叶", "竹葉", "淡竹叶"},
	"滑石": {"滑石"},
	"木通": {"木通"},
	"车前子": {"车前子", "車前子"},
	"泽泻": {"泽泻", "澤瀉"},
	"猪苓": {"猪苓", "豬苓"},
	"茵陈": {"茵陈", "茵陳", "绵茵陈"},
	"栀子": {"栀子", "梔子", "山栀", "山栀子"},
	"黄柏": {"黄柏", "黃柏", "黄蘖"},
	"杜仲": {"杜仲"},
	"牛膝": {"牛膝"},
	"枸杞子": {"枸杞子", "枸杞"},
	"菟丝子": {"菟丝子", "菟絲子"},
	"益智仁": {"益智仁"},
	"砂仁": {"砂仁", "縮砂"},
	"木香": {"木香", "广木香"},
	"青皮": {"青皮"},
	"大枣": {"大枣", "大棗", "红枣"},
	"生姜": {"生姜", "生薑", "姜"},
	"葱白": {"葱白", "蔥白"},
}

func herbAliases(name string) []string {
	if a, ok := herbAlias[name]; ok {
		return a
	}
	return []string{name}
}

// herbPairSearch 在方剂库中检索同时含两药的方剂
func herbPairSearch(h1, h2 string) HerbPairResult {
	db, err := loadFormulaDB()
	res := HerbPairResult{Herb1: h1, Herb2: h2}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	al1, al2 := herbAliases(strings.TrimSpace(h1)), herbAliases(strings.TrimSpace(h2))
	contain := func(comp []string, aliases []string) bool {
		for _, c := range comp {
			for _, a := range aliases {
				if c == a || strings.Contains(c, a) || strings.Contains(a, c) {
					return true
				}
			}
		}
		return false
	}
	for _, f := range db {
		if contain(f.Compose, al1) && contain(f.Compose, al2) {
			res.Formulas = append(res.Formulas, f)
		}
	}
	res.Count = len(res.Formulas)
	if len(res.Formulas) > 20 {
		res.Formulas = res.Formulas[:20]
	}
	return res
}
