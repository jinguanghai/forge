package main

// guard_approval_test.go — 三期 DMAIC I2 危险代码审批测试
// 覆盖: 危险模式检测 / 批准输入解析 / 摘要截断

import (
	"strings"
	"testing"
)

func TestCheckDangerousCode(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string // 期望类别; 空 = 不危险
	}{
		{"rm -rf", "rm -rf /tmp/x", "删除"},
		{"rm -fr", "rm -fr data", "删除"},
		{"del /s", "del /s /q C:\\temp", "删除"},
		{"Remove-Item", "Remove-Item -Recurse -Force C:\\x", "删除"},
		{"os.RemoveAll", "os.RemoveAll(dir)", "删除"},
		{"shutil.rmtree", "shutil.rmtree('/tmp/x')", "删除"},
		{"覆盖memory", "open('memory.json','w')", "覆盖"},
		{"覆盖forge源码", "os.WriteFile('forge.go', data)", "覆盖"},
		{"format c:", "format c:", "磁盘"},
		{"Format-Volume", "Format-Volume -DriveLetter C", "磁盘"},
		{"mkfs", "mkfs.ext4 /dev/sda", "磁盘"},
		{"taskkill forge", "taskkill /f /im forge.exe", "自杀"},
		{"git push force", "git push origin main --force", "强推"},
		{"git reset hard", "git reset --hard HEAD~1", "强推"},
		{"fork bomb", ":(){ :|:& };:", "炸弹"},

		// 正常代码不应误伤 (宁缺毋滥)
		{"正常python", "print(sum(range(10)))", ""},
		{"正常go", "fmt.Println(\"hello\")", ""},
		{"正常写临时文件", "os.WriteFile('/tmp/out.txt', data, 0644)", ""},
		{"正常git push", "git push origin main", ""},
		{"正常删除单文件", "os.Remove('old.txt')", ""},
		{"正常读memory", "json.load(open('memory.json'))", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kind, hit, danger := checkDangerousCode(c.code)
			if c.want == "" {
				if danger {
					t.Fatalf("不应判定危险: kind=%s hit=%s", kind, hit)
				}
				return
			}
			if !danger {
				t.Fatalf("应判定危险 [%s], 未命中", c.want)
			}
			if kind != c.want {
				t.Fatalf("类别: got %q want %q (hit=%q)", kind, c.want, hit)
			}
			if hit == "" {
				t.Fatal("命中模式为空")
			}
		})
	}
}

func TestParseApproval(t *testing.T) {
	approve := []string{"y", "Y", "yes", "是", "批准", "ok", "OK", "允许", " 是 "}
	deny := []string{"n", "no", "否", "拒绝", "", "cancel", "yesss", "不是"}

	for _, in := range approve {
		if !parseApproval(in) {
			t.Fatalf("应批准: %q", in)
		}
	}
	for _, in := range deny {
		if parseApproval(in) {
			t.Fatalf("不应批准: %q", in)
		}
	}
}

func TestSummarizeCode(t *testing.T) {
	long := strings.Repeat("a", 300)
	s := summarizeCode(long)
	if len([]rune(s)) != 121 { // 120 + …
		t.Fatalf("长摘要长度: %d", len([]rune(s)))
	}
	if !strings.HasSuffix(s, "…") {
		t.Fatal("长摘要应以 … 结尾")
	}

	multi := "line1\nline2\ttab"
	s2 := summarizeCode(multi)
	if strings.Contains(s2, "\n") || strings.Contains(s2, "\t") {
		t.Fatalf("摘要应折叠空白: %q", s2)
	}
}
