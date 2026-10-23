package main

// hygiene_forge_denylist_test.go —— .forge 清单「内容禁则」哨兵 (T56, 20261015)
//
// 为什么需要 (实测互斥):
//   credential_copy_sentinel_test.go 判定「除根 .env 外, 工作区任何文件都不得含
//   活跃凭据值」—— 它扫整个工作区, 含 .forge/。而 hygiene_forge_manifest.json
//   决定「.forge/ 下允许存在什么」。两条判据本来各管一层, 但清单若把 .env.bak_*
//   登记成合法 pattern, 就变成「为必然被判红的东西授权位置」: 判据互斥。
//   实测 (20261015): B4 为 .env 建的备份被凭据哨兵判红, 而同一形态刚被登记进
//   manifest.patterns —— 处置只能二选一 (删备份 + 撤 pattern, 代价是回滚点失效)。
//
// 判据形态: 清单的任何登记项 (entries[].name / patterns[].glob, 含 subdirs 内部)
//   都不得匹配 manifest.forbidden_globs。命中 = 清单不可用 (与「缺字段」同级),
//   不是「内容违规」: 清单本身失去可信度时, 它给出的其它判定也不该被信。
//
// 有意排除的形态 (必须钉住, 否则「有意排除」与「忘了写」在代码里长得一样):
//   env.bak_* / proxy_env_backup.json —— 去掉前导点后与合法项不可区分, 判据若写成
//   *env* 会误伤真实存在的 proxy_env_backup.json。故只钉 .env / .env.* 两式,
//   并把这两个对照组写进向量 (defense_system/hygiene_forge_forbidden_vectors.json)。
//   边界: 只按【文件名】判, 不判内容 —— 内容判据无法对「尚未出现的文件」求值;
//   工作区其它位置的凭据副本仍归 credential_copy_sentinel_test.go。
//
// 两侧同源: 向量外置一份, 本文件与 hygiene_forge_check.py --selftest 跑同一批
//   (先例: hygiene_redcard.py --selftest 与 TestHygieneDirVectors 共用 hygiene_vectors.json)。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const forgeForbiddenVectorsFile = "defense_system/hygiene_forge_forbidden_vectors.json"

// forgeForbiddenCase 是向量的一条。Globs 用 []interface{} 而非 []string:
// 有一条向量故意放非字符串 (123) 来钉「字段非法 -> 清单不可用」——
// 声明成 []string 会让整份向量解析失败, 自检自己先炸。
type forgeForbiddenCase struct {
	Kind          string        `json:"kind"`
	Name          string        `json:"name"`
	Where         string        `json:"where"`
	Value         string        `json:"value"`
	ExpectHit     bool          `json:"expect_hit"`
	WhyContains   string        `json:"why_contains"`
	Field         string        `json:"field"`
	Globs         []interface{} `json:"globs"`
	ExpectProblem bool          `json:"expect_problem"`
}

type forgeForbiddenVectors struct {
	Version int                  `json:"version"`
	Doc     string               `json:"doc"`
	Cases   []forgeForbiddenCase `json:"cases"`
}

// forgeForbiddenGlobs 解析清单的内容禁则区块, 返回 (禁则, 问题描述)。
// 问题非空 = 清单不可用 (fail-closed)。
//
// 独立解析而非给 forgeManifest 加字段: 该 struct 所在文件已 496/500 行 (F2 上限),
// 加字段会把结构判据顶到天花板 —— 留白比风格整齐值钱。
func forgeForbiddenGlobs(raw []byte) ([]string, string) {
	var doc struct {
		Forbidden *struct {
			Globs []string `json:"globs"`
		} `json:"forbidden_globs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "清单解析失败 (fail-closed): " + err.Error()
	}
	if doc.Forbidden == nil {
		return nil, "forbidden_globs 缺失 —— 内容禁则失去对照, 为凭据形态授权位置将无人拦"
	}
	if len(doc.Forbidden.Globs) == 0 {
		return nil, "forbidden_globs.globs 为空 —— 禁则无对照 (判据失效)"
	}
	for _, g := range doc.Forbidden.Globs {
		if strings.TrimSpace(g) == "" {
			return nil, fmt.Sprintf("forbidden_globs.globs 含空白项 %q (须非空字符串)", g)
		}
	}
	return doc.Forbidden.Globs, ""
}

// forgeForbiddenHits 扫描清单全部登记项, 返回命中禁则的项 (格式 "位置=名字")。
// 大小写: 两侧都先转小写再比 —— Windows 文件系统不区分大小写, 只认小写等于给
// '.ENV.BAK' 留后门 (filepath.Match 与 fnmatchcase 本身都大小写敏感)。
func forgeForbiddenHits(m forgeManifest, globs []string) []string {
	var out []string
	lows := make([]string, 0, len(globs))
	for _, g := range globs {
		lows = append(lows, strings.ToLower(g))
	}
	chk := func(where, name string) {
		if name == "" {
			return
		}
		n := strings.ToLower(name)
		for _, g := range lows {
			if ok, _ := filepath.Match(g, n); ok {
				out = append(out, where+"="+name)
				return
			}
		}
	}
	for _, e := range m.Entries {
		chk("entries", e.Name)
	}
	for _, p := range m.Patterns {
		chk("patterns", p.Glob)
	}
	for _, sd := range m.Subdirs {
		for _, e := range sd.Entries {
			chk(sd.Path+"/entries", e.Name)
		}
		for _, p := range sd.Patterns {
			chk(sd.Path+"/patterns", p.Glob)
		}
	}
	return out
}

// forgeForbiddenProblem 是接进 forgeManifestProblem 的入口 (无字段依赖: 自读清单)。
func forgeForbiddenProblem(m forgeManifest) string {
	raw, err := os.ReadFile(forgeManifestFile)
	if err != nil {
		return "清单读取失败 (fail-closed): " + err.Error()
	}
	globs, prob := forgeForbiddenGlobs(raw)
	if prob != "" {
		return prob
	}
	if hits := forgeForbiddenHits(m, globs); len(hits) > 0 {
		return "登记项命中内容禁则 " + strings.Join(hits, "; ") +
			" —— 该形态的副本会被凭据副本判据判红, 为它授权位置 = 两条判据互斥 " +
			"(处置: 撤掉该登记项, 理由见 manifest.forbidden_globs.doc)"
	}
	return ""
}

// cloneForgeManifest 深拷贝清单的登记项切片 —— 向量用合成清单, 绝不碰真文件。
func cloneForgeManifest(m forgeManifest) forgeManifest {
	c := m
	c.Entries = append([]forgeEntry(nil), m.Entries...)
	c.Patterns = append([]forgePattern(nil), m.Patterns...)
	c.Vectors = append([]forgeVector(nil), m.Vectors...)
	c.AllowEmptyDirs = append([]string(nil), m.AllowEmptyDirs...)
	c.Subdirs = make([]forgeSubdir, len(m.Subdirs))
	for i, sd := range m.Subdirs {
		sd.Entries = append([]forgeEntry(nil), sd.Entries...)
		sd.Patterns = append([]forgePattern(nil), sd.Patterns...)
		c.Subdirs[i] = sd
	}
	return c
}

// TestHygieneForgeForbiddenReal 哨兵: 真实清单必须字段齐备且零命中。
// 这条同时是 fail-closed 的落点 (清单缺 forbidden_globs 时, loadForgeManifest
// 已在 forgeManifestProblem 里拦下; 此处再独立核一次, 防那条接线被摘掉后静默放行)。
func TestHygieneForgeForbiddenReal(t *testing.T) {
	m := loadForgeManifest(t)
	raw, err := os.ReadFile(forgeManifestFile)
	if err != nil {
		t.Fatalf("清单读取失败: %v", err)
	}
	globs, prob := forgeForbiddenGlobs(raw)
	if prob != "" {
		t.Fatalf("真实清单内容禁则区块不可用: %s", prob)
	}
	if hits := forgeForbiddenHits(m, globs); len(hits) > 0 {
		t.Errorf("真实清单登记项命中内容禁则 %v —— 为凭据形态授权位置 = 判据互斥 "+
			"(清单: %s, 禁则: %v)", hits, forgeManifestFile, globs)
	}
	if p := forgeForbiddenProblem(m); p != "" {
		t.Errorf("forgeForbiddenProblem 与独立复算不一致: %s", p)
	}
	t.Logf("内容禁则 %v | 登记项 %d(含子目录 %d) | 命中 0", globs,
		len(m.Entries)+len(m.Patterns), len(m.Subdirs))
}

// TestHygieneForgeForbiddenVectors 判据的判据: 与 Python 侧跑同一份向量。
// 正例 (T56 原形态/容器内部/大小写) + 对照组 (含 env 的合法项不得误伤)
// + 字段族 (缺失/空/非字符串/空白 -> 清单不可用)。
func TestHygieneForgeForbiddenVectors(t *testing.T) {
	base := loadForgeManifest(t)
	rawManifest, err := os.ReadFile(forgeManifestFile)
	if err != nil {
		t.Fatalf("清单读取失败: %v", err)
	}
	globs, prob := forgeForbiddenGlobs(rawManifest)
	if prob != "" {
		t.Fatalf("真实禁则不可用, 自检失去对照: %s", prob)
	}
	rawVec, err := os.ReadFile(forgeForbiddenVectorsFile)
	if err != nil {
		t.Fatalf("向量读取失败 (fail-closed, 不放行): %v", err)
	}
	var vec forgeForbiddenVectors
	if err := json.Unmarshal(rawVec, &vec); err != nil {
		t.Fatalf("向量解析失败: %v", err)
	}
	if len(vec.Cases) < 12 {
		t.Fatalf("向量仅 %d 条, 覆盖不足 (自检会恒绿)", len(vec.Cases))
	}

	var rawDoc map[string]interface{}
	if err := json.Unmarshal(rawManifest, &rawDoc); err != nil {
		t.Fatalf("清单解析为 map 失败: %v", err)
	}

	bad, hitCases := 0, 0
	for _, c := range vec.Cases {
		if c.Kind == "field" {
			synth := map[string]interface{}{}
			for k, v := range rawDoc {
				synth[k] = v
			}
			if c.Field == "missing" {
				delete(synth, "forbidden_globs")
			} else {
				synth["forbidden_globs"] = map[string]interface{}{
					"doc": "selftest", "globs": c.Globs,
				}
			}
			b, merr := json.Marshal(synth)
			if merr != nil {
				t.Fatalf("合成清单序列化失败: %v", merr)
			}
			_, got := forgeForbiddenGlobs(b)
			if (got != "") != c.ExpectProblem {
				t.Errorf("字段向量不符: %q (field=%s) 期望 problem=%v, 实得 %q",
					c.Name, c.Field, c.ExpectProblem, got)
				bad++
			}
			continue
		}

		synth := cloneForgeManifest(base)
		switch c.Where {
		case "entries":
			synth.Entries = append(synth.Entries, forgeEntry{
				Name: c.Value, Kind: "file", Category: "backup", Owner: "selftest", Note: "selftest"})
		case "patterns":
			synth.Patterns = append(synth.Patterns, forgePattern{
				Glob: c.Value, Kind: "file", Category: "backup", Keep: 1, Note: "selftest"})
		case "subdir_entries":
			synth.Subdirs[0].Entries = append(synth.Subdirs[0].Entries, forgeEntry{
				Name: c.Value, Kind: "file", Category: "backup", Owner: "selftest", Note: "selftest"})
		case "subdir_patterns":
			synth.Subdirs[0].Patterns = append(synth.Subdirs[0].Patterns, forgePattern{
				Glob: c.Value, Kind: "file", Category: "backup", Keep: 1, Note: "selftest"})
		default:
			t.Errorf("未知 where: %q (向量与判定函数漂移)", c.Where)
			bad++
			continue
		}
		hits := forgeForbiddenHits(synth, globs)
		if (len(hits) > 0) != c.ExpectHit {
			t.Errorf("向量不符: %q (where=%s value=%s) 期望 hit=%v, 实得 %v",
				c.Name, c.Where, c.Value, c.ExpectHit, hits)
			bad++
			continue
		}
		if c.ExpectHit {
			hitCases++
			if c.WhyContains != "" && !strings.Contains(strings.Join(hits, ";"), c.WhyContains) {
				t.Errorf("向量归类不符: %q 命中项应含 %q, 实得 %v", c.Name, c.WhyContains, hits)
				bad++
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d/%d 条向量不符 —— 判据与向量漂移", bad, len(vec.Cases))
	}
	if hitCases == 0 {
		t.Errorf("向量中零命中项 —— 自检失去检出能力 (恒绿)")
	}
	t.Logf("内容禁则向量 %d 条全部一致 (Go 侧; 命中项 %d 条)", len(vec.Cases), hitCases)
}

// TestHygieneForgeForbiddenWiredIntoManifestProblem 钉住「接线」本身。
//
// 为什么单列: 判定函数测通了 != 它被接进了统一入口。把 forgeManifestProblem 末尾的
// return forgeForbiddenProblem(m) 摘掉, 上面三条用例照样全绿 (真实清单本来就干净) ——
// 「判据写了但没接上」在测试里长得和「判据没写」一模一样。
// 钉法: 合成一条含 .env 登记项的清单喂给统一入口, 必须被拦下且原因是内容禁则;
// 再用干净清单反向核对 (防「一律报错」式的假拦截)。
func TestHygieneForgeForbiddenWiredIntoManifestProblem(t *testing.T) {
	base := loadForgeManifest(t)
	if p := forgeManifestProblem(base); p != "" {
		t.Fatalf("干净清单被误判为不可用 (假拦截): %s", p)
	}
	synth := cloneForgeManifest(base)
	synth.Patterns = append(synth.Patterns, forgePattern{
		Glob: ".env.bak_*", Kind: "file", Category: "backup", Keep: 1, Note: "selftest"})
	got := forgeManifestProblem(synth)
	if got == "" {
		t.Fatalf("含 .env.bak_* 的清单被统一入口放行 —— 内容禁则未接进 forgeManifestProblem " +
			"(接线被摘掉? 判定函数本身可能是好的)")
	}
	if !strings.Contains(got, "内容禁则") {
		t.Errorf("拦下了但归因不对 (应报内容禁则): %s", got)
	}
	t.Logf("统一入口拦下脏清单: %s", got)
}

// TestHygieneForgeForbiddenPySelftest 钉住两侧同源: Python 侧跑同一份向量必须一致。
// 为什么必须联调: 判定逻辑两侧各写一份 (Go 哨兵管进门, Python 管无人值守巡检),
// 只测 Go 侧等于默认 Python 侧跟得上 —— 实测教训是「同构」会静默漂移。
func TestHygieneForgeForbiddenPySelftest(t *testing.T) {
	script := filepath.Join("defense_system", "hygiene_forge_check.py")
	if _, err := os.Stat(script); err != nil {
		t.Skipf("巡检脚本未安装: %v", err)
	}
	cmd := exec.Command(guardGatePython(), script, "--selftest")
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("Python 侧判据自检失败 (%v) —— 两侧判定漂移:\n%s", err, out)
	}
	if !strings.Contains(string(out), "全部一致") {
		t.Errorf("自检输出未见一致结论 (判据可能静默退化):\n%s", out)
	}
}
