package main

// audit_consumer_test.go — 埋点必须有消费者 (20261004)。
//
// 根因: strip / metric_claim 两个埋点上线当天就建成, 而唯一的质量消费者
// quality_report.py 只读 gate/gate_attempt —— "埋点 → 消费者 → 度量可见"
// 只做了第一层, 且零判据盯着"埋点有没有人用"。实测代价: 埋点上线后 25 次
// 真实收尾, 报告里连一个 strip / metric_claim 数字都没有 (可静默退化)。
//
// 判据三件事:
//   1. 每个登记事件都必须声明 Consumer (空 = 没人用) —— 新增埋点时强制回答"谁消费";
//   2. 声明了就必须真的在那个文件里读它 (文本里没有事件名 = 假闭环);
//   3. 端到端: 造最小审计流跑真实报告脚本, 断言数字真的出现在报告里
//      —— 源码里有字面量挡不住"读了但没用"。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 1. 每个登记事件都必须声明消费者。
func TestAuditEveryEventHasConsumer(t *testing.T) {
	if len(auditEvents) == 0 {
		t.Fatal("注册表为空 —— 判据本身不可信")
	}
	for _, spec := range auditEvents {
		if strings.TrimSpace(spec.Consumer) == "" {
			t.Errorf("event=%q 未声明 Consumer —— 埋点建了没人用 (Writer=%s)", spec.Name, spec.Writer)
		}
	}
}

// 2. 声明了消费者, 就必须真的在那个文件里读它。
func TestAuditConsumerActuallyReadsEvent(t *testing.T) {
	for _, spec := range auditEvents {
		if strings.TrimSpace(spec.Consumer) == "" {
			continue // 由 TestAuditEveryEventHasConsumer 报红, 这里不重复
		}
		path := filepath.FromSlash(spec.Consumer)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("event=%q 的消费者 %s 读不到: %v", spec.Name, spec.Consumer, err)
			continue
		}
		s := string(b)
		if !strings.Contains(s, `"`+spec.Name+`"`) && !strings.Contains(s, `'`+spec.Name+`'`) {
			t.Errorf("event=%q 声明消费者 %s, 但该文件里没有事件名字面量 —— 假闭环",
				spec.Name, spec.Consumer)
		}
	}
}

// 3. 端到端: 埋点 → 消费者 → 度量可见。
//
// 造一份最小审计流 (每类事件各一行, 数字刻意选成可辨识的), 跑真实报告脚本,
// 断言每个事件的数字都出现在报告里。--audit 注入临时文件 = 不写生产审计流。
func TestQualityReportConsumesSinkEvents(t *testing.T) {
	py, err := exec.LookPath(guardGatePython())
	if err != nil {
		t.Skipf("python 不可用: %v", err)
	}
	ts := "2026-10-04T10:00:00+08:00"
	lines := []string{
		`{"event":"gate","ts":"` + ts + `","lang":"python","ok":true,"duration_ms":5,"code_len":3,"input_len":0,"retries":0}`,
		`{"event":"strip","ts":"` + ts + `","chars":30,"tools":2,"turn":1,"head":"x"}`,
		`{"event":"metric_claim","ts":"` + ts + `","hit":true,"weak":true,"has_source":false,"sample":"成功率提升到 95%"}`,
		`{"event":"nosword","ts":"` + ts + `","anchors":3,"fresh":2,"skipped":1,"corrected":1,"completed":1,"exprs":[]}`,
		`{"event":"nosword_probe","ts":"` + ts + `","enabled":true,"rounds":0,"anchors":2,"fresh":1,"source":"plain"}`,
		`{"event":"nosword_expr","ts":"` + ts + `","enabled":true,"marks":4,"rejected":1}`,
		`{"event":"compact","ts":"` + ts + `","compressed_msgs":3,"summary_len":10,"before_tokens":100,"after_tokens":60,"saved_tokens":40,"saved_pct":"40.0%"}`,
		`{"event":"compact_failed","ts":"` + ts + `","err":"boom","est_tokens":100}`,
	}
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.jsonl")
	if err := os.WriteFile(audit, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("写临时审计流: %v", err)
	}
	cmd := exec.Command(py, "-u", filepath.Join("quality", "quality_report.py"), "--audit", audit)
	cmd.Env = pythonUTF8Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("quality_report.py 执行失败: %v\n%s", err, out)
	}
	s := string(out)
	wants := []string{
		"规则下沉闭环",
		"剥离 1 轮",        // strip
		"收尾 1 轮",        // metric_claim 分母
		"强判据命中 1",       // metric_claim 分子
		"无剑: 触发 1 轮",    // nosword
		"无剑表达式: 标记 4 次", // nosword_expr
		"上下文压缩: 成功 1 次", // compact
		"失败 1 次",        // compact_failed
	}
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Errorf("报告未消费到 %q —— 埋点没进度量", w)
		}
	}
}
