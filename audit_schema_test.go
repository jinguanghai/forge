package main

// audit_schema_test.go — 审计 schema 契约哨兵 (20260930)。
//
// 动机: gate_audit.jsonl 混装 7 类事件, 而"什么才算一条 gate 记录"此前由 4 个
// 消费者各自实现, 实测同一份数据得出 3 个不同数字 (28773/28967/31895)。
// 判据漂移之所以长期无人发现, 是因为没有任何判据盯着"契约"本身 ——
// 容器(文件存在/大小/时效)有 watchlist 管, 内容没有。
//
// 本哨兵钉住四件事:
//   1. 写入侧: 源码里出现的 event 字面量必须在 audit_schema.go 注册 (防私开事件)
//   2. 注册表: 每个登记的 event 必须有写入点 (防孤儿登记/文档腐化)
//   3. 读取侧: Python 三消费者必须镜像 event=="gate" 判据, 且旧隐式判据已消除
//   4. 数据侧: 真实 gate_audit.jsonl 每行 event 必须已注册

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// scanEventLiterals 扫描全包非测试 .go 源码, 返回 {文件:行 -> event值}。
// 扫描式而非写死文件清单 —— 判据必须随包内新增文件自动生效。
func scanEventLiterals(t *testing.T) map[string]string {
	t.Helper()
	ents, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读目录失败: %v", err)
	}
	re := regexp.MustCompile(`"event"\s*:\s*(?:"([^"]+)"|(auditGateEvent))`)
	out := map[string]string{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		src := string(b)
		for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
			val := auditGateEvent
			if m[2] >= 0 {
				val = src[m[2]:m[3]]
			}
			pos := fmt.Sprintf("%s:%d", name, strings.Count(src[:m[0]], "\n")+1)
			out[pos] = val
		}
	}
	return out
}

// 1. 写入侧字面量必须已注册 —— 防"私开事件"绕过契约。
func TestAuditSchemaEventLiteralsRegistered(t *testing.T) {
	lits := scanEventLiterals(t)
	if len(lits) == 0 {
		t.Fatal("未扫到任何 event 写入点 —— 扫描器失效, 判据本身不可信")
	}
	known := auditEventNames()
	for pos, ev := range lits {
		if !known[ev] {
			t.Errorf("写入点 %s 的 event=%q 未在 audit_schema.go 注册", pos, ev)
		}
	}
	t.Logf("扫到 %d 个 event 写入点, 全部已注册", len(lits))
}

// 2. 注册表每个 event 必须有写入点 —— 防孤儿登记 (只删代码不删登记 = 判据指向空气)。
func TestAuditSchemaEveryEventHasWriter(t *testing.T) {
	written := map[string]bool{}
	for _, ev := range scanEventLiterals(t) {
		written[ev] = true
	}
	for _, spec := range auditEvents {
		if !written[spec.Name] {
			t.Errorf("注册表 event=%q 无任何写入点 (孤儿登记, Writer=%q)", spec.Name, spec.Writer)
		}
		if spec.Writer == "" {
			t.Errorf("event=%q 未声明 Writer", spec.Name)
		}
		if len(spec.Required) == 0 {
			t.Errorf("event=%q 未声明必填字段", spec.Name)
		}
	}
	if len(auditEvents) < 7 {
		t.Errorf("注册表仅 %d 类事件, 少于已知的 7 类 —— 疑似被误删", len(auditEvents))
	}
}

// 3. 判据向量 —— 钉住 auditIsGateEvent 的行为边界。
func TestAuditIsGateEventVectors(t *testing.T) {
	cases := map[string]bool{
		"gate":           true,
		"gate_attempt":   false,
		"compact":        false,
		"compact_failed": false,
		"nosword":        false,
		"nosword_probe":  false,
		"nosword_expr":   false,
		"":               false,
		"GATE":           false,
		" gate":          false,
		"gate ":          false,
	}
	for in, want := range cases {
		if got := auditIsGateEvent(in); got != want {
			t.Errorf("auditIsGateEvent(%q) = %v, 期望 %v", in, got, want)
		}
	}
}

//  4. Python 三消费者必须镜像同一判据, 且旧隐式判据已消除。
//     "缺 event 即 gate" 是判据漂移的源头, 必须钉死不得复活。
func TestAuditPythonConsumersMirrorGateJudgement(t *testing.T) {
	files := []string{
		filepath.Join("defense_system", "forge_watchdog.py"),
		filepath.Join("defense_system", "report_cn.py"),
		filepath.Join("quality", "quality_report.py"),
	}
	legacy := []string{
		`__gate__`,                   // report_cn 旧桶名 (隐式推断)
		`o.get("event", "gate")`,     // quality_report 旧默认值
		`r.get('event', 'gate')`,     // 同上 (单引号)
		`("lang" in r or "ok" in r)`, // report_cn 旧推断表达式
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Errorf("读不到 %s: %v", f, err)
			continue
		}
		s := string(b)
		if !strings.Contains(s, `"gate"`) && !strings.Contains(s, `'gate'`) {
			t.Errorf("%s 未见 event==gate 判据 —— 消费者可能已脱离单一数据源", f)
		}
		for _, pat := range legacy {
			if strings.Contains(s, pat) {
				t.Errorf("%s 仍含旧隐式判据 %q (缺字段即语义), 判据漂移会复发", f, pat)
			}
		}
	}
}

// 5. 真实数据: 每行 event 必须已注册; 迁移时刻之后的 gate 记录必须含必填字段。
func TestAuditSchemaRealDataConforms(t *testing.T) {
	path := filepath.Join(os.Getenv("FORGE_WORKDIR_OVERRIDE"), "gate_audit.jsonl")
	if path == "gate_audit.jsonl" {
		path = filepath.Join("D:\\forge", "gate_audit.jsonl")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("无审计文件 (跳过): %v", err)
	}
	known := auditEventNames()
	n, bad, noEvent, legacy := 0, 0, 0, 0
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		var o map[string]interface{}
		if json.Unmarshal([]byte(l), &o) != nil {
			t.Errorf("不可解析的行: %.100s", l)
			continue
		}
		n++
		ev, _ := o["event"].(string)
		if ev == "" {
			// 过渡期遗留 (ts 早于契约生效时刻) 只计数提示; 契约生效后仍缺 = 真违规。
			ts, _ := o["ts"].(string)
			if ts != "" && ts >= auditSchemaEpoch {
				noEvent++
				if noEvent <= 2 {
					t.Errorf("契约生效后 (%s) 仍缺 event 字段: %.120s", auditSchemaEpoch, l)
				}
			} else {
				legacy++
			}
			continue
		}
		if !known[ev] {
			bad++
			if bad <= 3 {
				t.Errorf("未注册 event=%q: %.120s", ev, l)
			}
		}
	}
	if bad > 0 || noEvent > 0 {
		t.Errorf("共 %d/%d 条记录不合规 (未注册 %d, 契约生效后缺 event %d)", bad+noEvent, n, bad, noEvent)
	}
	if legacy > 0 {
		t.Logf("提示: %d 条过渡期遗留 (ts < %s 且缺 event), 不判违规", legacy, auditSchemaEpoch)
	}
	t.Logf("真实数据 %d 行通过 schema 校验", n)
}
