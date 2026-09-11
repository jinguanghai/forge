package main

// guard_target_test.go — 三期 DMAIC I2 补: 第二道防线语义兜底 测试
// 覆盖: checkDangerousTarget 受保护目标×破坏谓词共现检测 正/反用例

import (
	"testing"
)

func TestCheckDangerousTarget(t *testing.T) {
	// 正面: 受保护目标 + 破坏谓词 → 应拦
	blockCases := []struct {
		name string
		code string
		want string
	}{
		{"删记忆", "os.remove(\"memory.json\")", "memory.json"},
		{"删源码config", "os.unlink(\"config.go\")", "config.go"},
		{"覆盖forge源码", "os.WriteFile(\"forge.go\", data)", "forge.go"},
		{"数组rm删记忆", "subprocess.run(['rm','-rf','memory.json'])", "memory.json"},
		{"rmtree删记忆", "shutil.rmtree(\"memory.json\")", "memory.json"},
		{"改名config", "os.rename(\"config.go\", \"config.go.bak\")", "config.go"},
		{"删env", "os.remove(\".env\")", ".env"},
	}
	for _, c := range blockCases {
		t.Run(c.name, func(t *testing.T) {
			kind, hit, danger := checkDangerousTarget(c.code)
			if !danger {
				t.Fatalf("应拦: %q 实际未命中, kind=%q hit=%q", c.code, kind, hit)
			}
			if hit != c.want {
				t.Fatalf("受保护目标: got %q want %q", hit, c.want)
			}
			if kind != "保护目标" {
				t.Fatalf("类别: got %q want %q", kind, "保护目标")
			}
		})
	}

	// 反面: 只读/非受保护目标 → 不误报
	passCases := []struct {
		name string
		code string
	}{
		{"删普通临时文件", "os.Remove('old.txt')"},
		{"只读记忆", "json.load(open('memory.json'))"},
		{"存在性检查", "os.path.exists('memory.json')"},
		{"写普通临时文件", "os.WriteFile('/tmp/out.txt', data, 0644)"},
		{"普通push", "git push origin main"},
		{"打印删除词", "print('remove file name')"},
		{"正常python", "print(sum(range(10)))"},
	}
	for _, c := range passCases {
		t.Run(c.name, func(t *testing.T) {
			kind, hit, danger := checkDangerousTarget(c.code)
			if danger {
				t.Fatalf("不应拦: %q 命中 kind=%q hit=%q", c.code, kind, hit)
			}
		})
	}
}
